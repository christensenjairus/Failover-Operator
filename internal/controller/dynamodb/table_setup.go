package dynamodb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// TableSetupOptions contains options for setting up the DynamoDB table
type TableSetupOptions struct {
	// The name of the DynamoDB table to create
	TableName string

	// BillingMode is the billing mode for the table (PROVISIONED or PAY_PER_REQUEST)
	BillingMode types.BillingMode

	// ProvisionedThroughput is used when BillingMode is PROVISIONED
	ProvisionedThroughput *types.ProvisionedThroughput

	// Whether to wait for the table to be created
	WaitForActive bool

	// Maximum wait time in seconds for table creation (default: 300)
	MaxWaitSeconds int
}

// DefaultTableSetupOptions returns default options for table setup
func DefaultTableSetupOptions(tableName string) *TableSetupOptions {
	return &TableSetupOptions{
		TableName:      tableName,
		BillingMode:    types.BillingModePayPerRequest,
		WaitForActive:  true,
		MaxWaitSeconds: 300,
	}
}

// SetupDynamoDBTable creates the DynamoDB table needed for the Failover Operator
func SetupDynamoDBTable(ctx context.Context, client *dynamodb.Client, options *TableSetupOptions) error {
	logger := log.FromContext(ctx).WithValues(
		"tableName", options.TableName,
	)

	logger.Info("Setting up DynamoDB table")

	// First, check if the table already exists
	exists, err := tableExists(ctx, client, options.TableName)
	if err != nil {
		return fmt.Errorf("failed to check if table exists: %w", err)
	}

	if exists {
		logger.Info("Table already exists, skipping creation")
		return nil
	}

	// Define the table schema
	createTableInput := &dynamodb.CreateTableInput{
		TableName:   aws.String(options.TableName),
		BillingMode: options.BillingMode,

		// Primary key schema
		KeySchema: []types.KeySchemaElement{
			{
				AttributeName: aws.String("PK"),
				KeyType:       types.KeyTypeHash,
			},
			{
				AttributeName: aws.String("SK"),
				KeyType:       types.KeyTypeRange,
			},
		},

		// Attribute definitions for all key attributes
		AttributeDefinitions: []types.AttributeDefinition{
			{
				AttributeName: aws.String("PK"),
				AttributeType: types.ScalarAttributeTypeS,
			},
			{
				AttributeName: aws.String("SK"),
				AttributeType: types.ScalarAttributeTypeS,
			},
			{
				AttributeName: aws.String("GSI1PK"),
				AttributeType: types.ScalarAttributeTypeS,
			},
			{
				AttributeName: aws.String("GSI1SK"),
				AttributeType: types.ScalarAttributeTypeS,
			},
		},

		// Global Secondary Indexes for efficient queries
		GlobalSecondaryIndexes: []types.GlobalSecondaryIndex{
			{
				IndexName: aws.String("GSI1"),
				KeySchema: []types.KeySchemaElement{
					{
						AttributeName: aws.String("GSI1PK"),
						KeyType:       types.KeyTypeHash,
					},
					{
						AttributeName: aws.String("GSI1SK"),
						KeyType:       types.KeyTypeRange,
					},
				},
				Projection: &types.Projection{
					ProjectionType: types.ProjectionTypeAll,
				},
			},
		},
	}

	// Add provisioned throughput if using PROVISIONED billing mode
	if options.BillingMode == types.BillingModeProvisioned {
		if options.ProvisionedThroughput == nil {
			// Default provisioned throughput if not specified
			options.ProvisionedThroughput = &types.ProvisionedThroughput{
				ReadCapacityUnits:  aws.Int64(5),
				WriteCapacityUnits: aws.Int64(5),
			}
		}
		createTableInput.ProvisionedThroughput = options.ProvisionedThroughput

		// Add provisioned throughput for GSI
		for i := range createTableInput.GlobalSecondaryIndexes {
			createTableInput.GlobalSecondaryIndexes[i].ProvisionedThroughput = options.ProvisionedThroughput
		}
	}

	// Create the table
	logger.Info("Creating DynamoDB table")
	_, err = client.CreateTable(ctx, createTableInput)
	if err != nil {
		return fmt.Errorf("failed to create table: %w", err)
	}

	logger.Info("DynamoDB table creation initiated")

	// Wait for table to become active if requested
	if options.WaitForActive {
		logger.Info("Waiting for table to become active", "maxWaitSeconds", options.MaxWaitSeconds)
		err = waitForTableActive(ctx, client, options.TableName, options.MaxWaitSeconds)
		if err != nil {
			return fmt.Errorf("error waiting for table to become active: %w", err)
		}
		logger.Info("DynamoDB table is now active")
	}

	return nil
}

// tableExists checks if a DynamoDB table exists
func tableExists(ctx context.Context, client *dynamodb.Client, tableName string) (bool, error) {
	input := &dynamodb.DescribeTableInput{
		TableName: aws.String(tableName),
	}

	_, err := client.DescribeTable(ctx, input)
	if err != nil {
		// Check if the error is because the table doesn't exist
		var notFoundErr *types.ResourceNotFoundException
		if errors.As(err, &notFoundErr) {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

// waitForTableActive waits for a DynamoDB table to reach the ACTIVE state
func waitForTableActive(ctx context.Context, client *dynamodb.Client, tableName string, maxWaitSeconds int) error {
	logger := log.FromContext(ctx)

	// Calculate deadline
	deadline := time.Now().Add(time.Duration(maxWaitSeconds) * time.Second)

	input := &dynamodb.DescribeTableInput{
		TableName: aws.String(tableName),
	}

	// Poll until active or timeout
	for time.Now().Before(deadline) {
		result, err := client.DescribeTable(ctx, input)
		if err != nil {
			return err
		}

		if result.Table.TableStatus == types.TableStatusActive {
			return nil
		}

		logger.V(1).Info(
			"Waiting for table to become active",
			"currentStatus", result.Table.TableStatus,
			"tableName", tableName,
		)

		// Wait before polling again
		time.Sleep(5 * time.Second)
	}

	return fmt.Errorf("timed out waiting for table %s to become active", tableName)
}
