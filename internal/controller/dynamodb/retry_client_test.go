package dynamodb

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stretchr/testify/assert"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func init() {
	// Set up logging for tests
	logf.SetLogger(zap.New(zap.UseDevMode(true)))
}

// MockDynamoDBClientWithRetries mocks a DynamoDB client that fails a certain number of times before succeeding
type MockDynamoDBClientWithRetries struct {
	GetItemFailCount     int
	PutItemFailCount     int
	UpdateItemFailCount  int
	DeleteItemFailCount  int
	QueryFailCount       int
	ScanFailCount        int
	TransactFailCount    int
	GetItemCallCount     int
	PutItemCallCount     int
	UpdateItemCallCount  int
	DeleteItemCallCount  int
	QueryCallCount       int
	ScanCallCount        int
	TransactCallCount    int
	FailWithRetryable    bool
	FailWithNonRetryable bool
}

// Create a retryable error
func (m *MockDynamoDBClientWithRetries) retryableError() error {
	if m.FailWithNonRetryable {
		return errors.New("non-retryable error")
	}
	return &types.ProvisionedThroughputExceededException{
		Message: aws.String("Rate limit exceeded"),
	}
}

// Implement the DynamoDBClient interface
func (m *MockDynamoDBClientWithRetries) GetItem(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	m.GetItemCallCount++
	if m.GetItemCallCount <= m.GetItemFailCount {
		return nil, m.retryableError()
	}
	return &dynamodb.GetItemOutput{}, nil
}

func (m *MockDynamoDBClientWithRetries) PutItem(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	m.PutItemCallCount++
	if m.PutItemCallCount <= m.PutItemFailCount {
		return nil, m.retryableError()
	}
	return &dynamodb.PutItemOutput{}, nil
}

func (m *MockDynamoDBClientWithRetries) UpdateItem(ctx context.Context, params *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	m.UpdateItemCallCount++
	if m.UpdateItemCallCount <= m.UpdateItemFailCount {
		return nil, m.retryableError()
	}
	return &dynamodb.UpdateItemOutput{}, nil
}

func (m *MockDynamoDBClientWithRetries) DeleteItem(ctx context.Context, params *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	m.DeleteItemCallCount++
	if m.DeleteItemCallCount <= m.DeleteItemFailCount {
		return nil, m.retryableError()
	}
	return &dynamodb.DeleteItemOutput{}, nil
}

func (m *MockDynamoDBClientWithRetries) Query(ctx context.Context, params *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	m.QueryCallCount++
	if m.QueryCallCount <= m.QueryFailCount {
		return nil, m.retryableError()
	}
	return &dynamodb.QueryOutput{}, nil
}

func (m *MockDynamoDBClientWithRetries) Scan(ctx context.Context, params *dynamodb.ScanInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	m.ScanCallCount++
	if m.ScanCallCount <= m.ScanFailCount {
		return nil, m.retryableError()
	}
	return &dynamodb.ScanOutput{}, nil
}

func (m *MockDynamoDBClientWithRetries) TransactWriteItems(ctx context.Context, params *dynamodb.TransactWriteItemsInput, optFns ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	m.TransactCallCount++
	if m.TransactCallCount <= m.TransactFailCount {
		return nil, m.retryableError()
	}
	return &dynamodb.TransactWriteItemsOutput{}, nil
}

// Test that the retry client successfully retries operations
func TestRetryClient_SuccessfulRetry(t *testing.T) {
	// Create a mock client that fails twice and then succeeds
	mockClient := &MockDynamoDBClientWithRetries{
		GetItemFailCount:  2,
		FailWithRetryable: true,
	}

	// Create a retry client with default config but faster backoff for testing
	config := DefaultRetryConfig()
	config.InitialBackoffMs = 10 // Fast backoff for testing
	config.MaxBackoffMs = 50
	retryClient := NewRetryingDynamoDBClient(mockClient, config)

	// Test GetItem
	ctx := context.Background()
	result, err := retryClient.GetItem(ctx, &dynamodb.GetItemInput{})

	// Should succeed after 3 attempts (2 failures + 1 success)
	assert.NoError(t, err, "GetItem should succeed after retries")
	assert.NotNil(t, result, "Result should not be nil")
	assert.Equal(t, 3, mockClient.GetItemCallCount, "Should have called GetItem 3 times")
}

// Test that the retry client gives up after max retries
func TestRetryClient_MaxRetriesExceeded(t *testing.T) {
	// Create a mock client that always fails
	mockClient := &MockDynamoDBClientWithRetries{
		GetItemFailCount:  10, // More failures than max retries
		FailWithRetryable: true,
	}

	// Create a retry client with default config but faster backoff for testing
	config := DefaultRetryConfig()
	config.MaxRetries = 3
	config.InitialBackoffMs = 10 // Fast backoff for testing
	config.MaxBackoffMs = 50
	retryClient := NewRetryingDynamoDBClient(mockClient, config)

	// Test GetItem
	ctx := context.Background()
	result, err := retryClient.GetItem(ctx, &dynamodb.GetItemInput{})

	// Should fail after max retries (4 attempts total: 1 initial + 3 retries)
	assert.Error(t, err, "GetItem should fail after max retries")
	assert.Nil(t, result, "Result should be nil")
	assert.Equal(t, 4, mockClient.GetItemCallCount, "Should have called GetItem 4 times")
}

// Test that non-retryable errors aren't retried
func TestRetryClient_NonRetryableError(t *testing.T) {
	// Create a mock client that fails with a non-retryable error
	mockClient := &MockDynamoDBClientWithRetries{
		GetItemFailCount:     10,
		FailWithNonRetryable: true,
	}

	// Create a retry client
	retryClient := NewRetryingDynamoDBClient(mockClient, nil)

	// Test GetItem
	ctx := context.Background()
	result, err := retryClient.GetItem(ctx, &dynamodb.GetItemInput{})

	// Should fail immediately and not retry
	assert.Error(t, err, "GetItem should fail without retrying")
	assert.Nil(t, result, "Result should be nil")
	assert.Equal(t, 1, mockClient.GetItemCallCount, "Should have called GetItem exactly once")
}

// Test that context cancellation stops retrying
func TestRetryClient_ContextCancellation(t *testing.T) {
	// Create a mock client that always fails
	mockClient := &MockDynamoDBClientWithRetries{
		GetItemFailCount:  10,
		FailWithRetryable: true,
	}

	// Create a retry client with slow backoff to ensure cancellation during backoff
	config := DefaultRetryConfig()
	config.InitialBackoffMs = 500 // Slow backoff to ensure we can cancel
	retryClient := NewRetryingDynamoDBClient(mockClient, config)

	// Create a context that will be cancelled
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Test GetItem
	result, err := retryClient.GetItem(ctx, &dynamodb.GetItemInput{})

	// Should fail due to context cancellation
	assert.Error(t, err, "GetItem should fail due to context cancellation")
	assert.Nil(t, result, "Result should be nil")
	assert.Contains(t, err.Error(), "cancelled", "Error should mention cancellation")
	assert.LessOrEqual(t, mockClient.GetItemCallCount, 2, "Should not have called GetItem more than twice")
}

// Test backoff calculation
func TestRetryClient_CalculateBackoff(t *testing.T) {
	config := DefaultRetryConfig()
	config.InitialBackoffMs = 100
	config.MaxBackoffMs = 1000
	config.BackoffMultiplier = 2.0

	retryClient := NewRetryingDynamoDBClient(nil, config)

	// First attempt
	backoff1 := retryClient.calculateBackoff(1)
	assert.True(t, backoff1 >= 80*time.Millisecond && backoff1 <= 240*time.Millisecond,
		"First backoff should be around 100ms with jitter, got %s", backoff1)

	// Second attempt
	backoff2 := retryClient.calculateBackoff(2)
	assert.True(t, backoff2 >= 160*time.Millisecond && backoff2 <= 480*time.Millisecond,
		"Second backoff should be around 200ms with jitter, got %s", backoff2)

	// Many attempts (should cap at max)
	backoff10 := retryClient.calculateBackoff(10)
	assert.True(t, backoff10 >= 800*time.Millisecond && backoff10 <= 1000*time.Millisecond,
		"High attempt backoff should be capped at max, got %s", backoff10)
}

// Test various operations with retries
func TestRetryClient_AllOperations(t *testing.T) {
	// Create a mock client that fails once for each operation
	mockClient := &MockDynamoDBClientWithRetries{
		GetItemFailCount:    1,
		PutItemFailCount:    1,
		UpdateItemFailCount: 1,
		DeleteItemFailCount: 1,
		QueryFailCount:      1,
		ScanFailCount:       1,
		TransactFailCount:   1,
		FailWithRetryable:   true,
	}

	// Create a retry client with fast backoff for testing
	config := DefaultRetryConfig()
	config.InitialBackoffMs = 5
	config.MaxBackoffMs = 10
	retryClient := NewRetryingDynamoDBClient(mockClient, config)

	// Test context
	ctx := context.Background()

	// Test all operations
	_, err := retryClient.GetItem(ctx, &dynamodb.GetItemInput{})
	assert.NoError(t, err, "GetItem should succeed after retry")
	assert.Equal(t, 2, mockClient.GetItemCallCount)

	_, err = retryClient.PutItem(ctx, &dynamodb.PutItemInput{})
	assert.NoError(t, err, "PutItem should succeed after retry")
	assert.Equal(t, 2, mockClient.PutItemCallCount)

	_, err = retryClient.UpdateItem(ctx, &dynamodb.UpdateItemInput{})
	assert.NoError(t, err, "UpdateItem should succeed after retry")
	assert.Equal(t, 2, mockClient.UpdateItemCallCount)

	_, err = retryClient.DeleteItem(ctx, &dynamodb.DeleteItemInput{})
	assert.NoError(t, err, "DeleteItem should succeed after retry")
	assert.Equal(t, 2, mockClient.DeleteItemCallCount)

	_, err = retryClient.Query(ctx, &dynamodb.QueryInput{})
	assert.NoError(t, err, "Query should succeed after retry")
	assert.Equal(t, 2, mockClient.QueryCallCount)

	_, err = retryClient.Scan(ctx, &dynamodb.ScanInput{})
	assert.NoError(t, err, "Scan should succeed after retry")
	assert.Equal(t, 2, mockClient.ScanCallCount)

	_, err = retryClient.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{})
	assert.NoError(t, err, "TransactWriteItems should succeed after retry")
	assert.Equal(t, 2, mockClient.TransactCallCount)
}
