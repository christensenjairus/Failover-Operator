package dynamodb

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// RetryableError defines which errors should be retried
type RetryableError interface {
	Retryable() bool
}

// RetryConfig defines the configuration for retry attempts
type RetryConfig struct {
	// MaxRetries is the maximum number of retry attempts
	MaxRetries int

	// InitialBackoff is the initial backoff duration in milliseconds
	InitialBackoffMs int

	// MaxBackoff is the maximum backoff duration in milliseconds
	MaxBackoffMs int

	// BackoffMultiplier is the multiplier applied to the backoff duration after each retry
	BackoffMultiplier float64

	// RetryableErrors is a list of error types that should be retried
	RetryableErrors []error
}

// DefaultRetryConfig provides reasonable default values for retry configuration
func DefaultRetryConfig() *RetryConfig {
	return &RetryConfig{
		MaxRetries:        5,
		InitialBackoffMs:  100,
		MaxBackoffMs:      5000,
		BackoffMultiplier: 2.0,
		RetryableErrors: []error{
			// Common AWS retryable errors
			&types.ProvisionedThroughputExceededException{},
			&types.InternalServerError{},
			&types.LimitExceededException{},
			&types.TransactionConflictException{},
		},
	}
}

// RetryingDynamoDBClient wraps a DynamoDB client with retry logic
type RetryingDynamoDBClient struct {
	client DynamoDBClient
	config *RetryConfig
}

// NewRetryingDynamoDBClient creates a new client with retry logic
func NewRetryingDynamoDBClient(client DynamoDBClient, config *RetryConfig) *RetryingDynamoDBClient {
	if config == nil {
		config = DefaultRetryConfig()
	}
	return &RetryingDynamoDBClient{
		client: client,
		config: config,
	}
}

// IsRetryable checks if an error should be retried
func (r *RetryingDynamoDBClient) IsRetryable(err error) bool {
	if err == nil {
		return false
	}

	// Check if the error implements the RetryableError interface
	if retryable, ok := err.(RetryableError); ok {
		return retryable.Retryable()
	}

	// Check if the error matches any of the retryable error types
	for _, retryableErr := range r.config.RetryableErrors {
		if errors.Is(err, retryableErr) {
			return true
		}
	}

	// Check for specific AWS error types by name comparison
	errType := fmt.Sprintf("%T", err)
	for _, retryableErr := range r.config.RetryableErrors {
		retryableType := fmt.Sprintf("%T", retryableErr)
		if errType == retryableType {
			return true
		}
	}

	return false
}

// ExtractTransactionErrors extracts detailed error information from a TransactionCanceledException
// Returns a map of item index to error information
func ExtractTransactionErrors(err error) map[int]string {
	itemErrors := make(map[int]string)

	// Check if this is a TransactionCanceledException
	var txErr *types.TransactionCanceledException
	if !errors.As(err, &txErr) || txErr.CancellationReasons == nil {
		return itemErrors
	}

	// Extract error info for each item in the transaction
	for i, reason := range txErr.CancellationReasons {
		if reason.Code == nil || *reason.Code == "" {
			continue // No error for this item
		}

		errMsg := *reason.Code
		if reason.Message != nil && *reason.Message != "" {
			errMsg += ": " + *reason.Message
		}

		itemErrors[i] = errMsg
	}

	return itemErrors
}

// calculateBackoff determines the backoff duration for a specific retry attempt
func (r *RetryingDynamoDBClient) calculateBackoff(attempt int) time.Duration {
	// Calculate exponential backoff with jitter
	backoffMs := float64(r.config.InitialBackoffMs) * math.Pow(r.config.BackoffMultiplier, float64(attempt))

	// Apply jitter (up to 20%)
	jitterFactor := 0.8 + (0.4 * math.Abs(math.Sin(float64(attempt))))
	backoffMs = backoffMs * jitterFactor

	// Cap at max backoff
	if backoffMs > float64(r.config.MaxBackoffMs) {
		backoffMs = float64(r.config.MaxBackoffMs)
	}

	return time.Duration(int(backoffMs)) * time.Millisecond
}

// retryOperation executes an operation with retries according to the retry policy
func (r *RetryingDynamoDBClient) retryOperation(ctx context.Context, operation string, fn func() (interface{}, error)) (interface{}, error) {
	logger := log.FromContext(ctx).WithValues(
		"operation", operation,
	)

	var lastErr error
	for attempt := 0; attempt <= r.config.MaxRetries; attempt++ {
		// If not the first attempt, apply backoff
		if attempt > 0 {
			backoff := r.calculateBackoff(attempt)
			logger.V(1).Info("Retrying operation",
				"attempt", attempt,
				"maxRetries", r.config.MaxRetries,
				"backoff", backoff.String())

			select {
			case <-ctx.Done():
				// Context cancelled or timed out
				return nil, fmt.Errorf("operation %s cancelled during retry: %w", operation, ctx.Err())
			case <-time.After(backoff):
				// Continue after backoff
			}
		}

		// Execute the operation
		result, err := fn()
		if err == nil {
			// Success!
			if attempt > 0 {
				logger.V(1).Info("Operation succeeded after retries", "attempts", attempt+1)
			}
			return result, nil
		}

		lastErr = err
		if !r.IsRetryable(err) {
			// Non-retryable error
			return nil, fmt.Errorf("non-retryable error in %s: %w", operation, err)
		}

		// Retryable error, log and continue
		logger.V(1).Info("Retryable error encountered",
			"error", err.Error(),
			"attempt", attempt+1,
			"maxRetries", r.config.MaxRetries)
	}

	// If we get here, we've exhausted our retries
	return nil, fmt.Errorf("operation %s failed after %d retries: %w",
		operation, r.config.MaxRetries, lastErr)
}

// Implement the DynamoDBClient interface methods with retry logic

func (r *RetryingDynamoDBClient) GetItem(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	result, err := r.retryOperation(ctx, "GetItem", func() (interface{}, error) {
		return r.client.GetItem(ctx, params, optFns...)
	})
	if err != nil {
		return nil, err
	}
	return result.(*dynamodb.GetItemOutput), nil
}

func (r *RetryingDynamoDBClient) PutItem(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	result, err := r.retryOperation(ctx, "PutItem", func() (interface{}, error) {
		return r.client.PutItem(ctx, params, optFns...)
	})
	if err != nil {
		return nil, err
	}
	return result.(*dynamodb.PutItemOutput), nil
}

func (r *RetryingDynamoDBClient) UpdateItem(ctx context.Context, params *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	result, err := r.retryOperation(ctx, "UpdateItem", func() (interface{}, error) {
		return r.client.UpdateItem(ctx, params, optFns...)
	})
	if err != nil {
		return nil, err
	}
	return result.(*dynamodb.UpdateItemOutput), nil
}

func (r *RetryingDynamoDBClient) DeleteItem(ctx context.Context, params *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	result, err := r.retryOperation(ctx, "DeleteItem", func() (interface{}, error) {
		return r.client.DeleteItem(ctx, params, optFns...)
	})
	if err != nil {
		return nil, err
	}
	return result.(*dynamodb.DeleteItemOutput), nil
}

func (r *RetryingDynamoDBClient) Query(ctx context.Context, params *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	result, err := r.retryOperation(ctx, "Query", func() (interface{}, error) {
		return r.client.Query(ctx, params, optFns...)
	})
	if err != nil {
		return nil, err
	}
	return result.(*dynamodb.QueryOutput), nil
}

func (r *RetryingDynamoDBClient) Scan(ctx context.Context, params *dynamodb.ScanInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	result, err := r.retryOperation(ctx, "Scan", func() (interface{}, error) {
		return r.client.Scan(ctx, params, optFns...)
	})
	if err != nil {
		return nil, err
	}
	return result.(*dynamodb.ScanOutput), nil
}

func (r *RetryingDynamoDBClient) TransactWriteItems(ctx context.Context, params *dynamodb.TransactWriteItemsInput, optFns ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	result, err := r.retryOperation(ctx, "TransactWriteItems", func() (interface{}, error) {
		return r.client.TransactWriteItems(ctx, params, optFns...)
	})
	if err != nil {
		// For transaction errors, extract detailed information about which items failed
		var txErr *types.TransactionCanceledException
		if errors.As(err, &txErr) {
			logger := log.FromContext(ctx)
			itemErrors := ExtractTransactionErrors(err)

			if len(itemErrors) > 0 {
				logger.Error(err, "Transaction failed with item-specific errors",
					"itemErrors", itemErrors)

				// Enhance the error message with item details
				return nil, fmt.Errorf("transaction failed: %w (item errors: %v)",
					err, itemErrors)
			}
		}
		return nil, err
	}
	return result.(*dynamodb.TransactWriteItemsOutput), nil
}

func (r *RetryingDynamoDBClient) BatchGetItem(ctx context.Context, params *dynamodb.BatchGetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.BatchGetItemOutput, error) {
	result, err := r.retryOperation(ctx, "BatchGetItem", func() (interface{}, error) {
		return r.client.BatchGetItem(ctx, params, optFns...)
	})
	if err != nil {
		return nil, err
	}
	return result.(*dynamodb.BatchGetItemOutput), nil
}

func (r *RetryingDynamoDBClient) BatchWriteItem(ctx context.Context, params *dynamodb.BatchWriteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error) {
	result, err := r.retryOperation(ctx, "BatchWriteItem", func() (interface{}, error) {
		return r.client.BatchWriteItem(ctx, params, optFns...)
	})
	if err != nil {
		return nil, err
	}
	return result.(*dynamodb.BatchWriteItemOutput), nil
}
