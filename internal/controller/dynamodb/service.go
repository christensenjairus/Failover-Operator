// Package dynamodb provides DynamoDB integration for state management
package dynamodb

import (
	"context"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// CreateAWSDynamoDBClient creates a real DynamoDB client using the AWS SDK
func CreateAWSDynamoDBClient(ctx context.Context) (DynamoDBClient, error) {
	logger := log.FromContext(ctx)

	// Get configuration from environment variables
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "us-west-2" // Default region if not specified
	}

	endpoint := os.Getenv("AWS_ENDPOINT")
	useLocalEndpoint := os.Getenv("AWS_USE_LOCAL_ENDPOINT") == "true"

	accessKey := os.Getenv("AWS_ACCESS_KEY_ID")
	secretKey := os.Getenv("AWS_SECRET_ACCESS_KEY")
	profile := os.Getenv("AWS_PROFILE")

	// Create AWS config options
	var options []func(*awsconfig.LoadOptions) error

	// Set region
	options = append(options, awsconfig.WithRegion(region))

	// Use credentials or profile
	if profile != "" {
		logger.Info("Using AWS profile from credentials file", "profile", profile)
		options = append(options, awsconfig.WithSharedConfigProfile(profile))
	} else if accessKey != "" && secretKey != "" {
		logger.Info("Using explicitly provided AWS credentials")
		credProvider := credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")
		options = append(options, awsconfig.WithCredentialsProvider(credProvider))
	} else {
		logger.Info("Using default AWS credential provider chain")
	}

	// Load AWS configuration
	cfg, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS configuration: %w", err)
	}

	// Create DynamoDB client
	var dynamoDBOptions []func(*dynamodb.Options)

	// Use custom endpoint for local testing if configured
	if useLocalEndpoint && endpoint != "" {
		logger.Info("Using local DynamoDB endpoint", "endpoint", endpoint)
		dynamoDBOptions = append(dynamoDBOptions, func(options *dynamodb.Options) {
			options.BaseEndpoint = aws.String(endpoint)
		})
	}

	// Create and return the client
	client := dynamodb.NewFromConfig(cfg, dynamoDBOptions...)

	// Test the connection to provide better error messages
	logger.Info("Testing DynamoDB connection...")
	_, err = client.ListTables(ctx, &dynamodb.ListTablesInput{
		Limit: aws.Int32(1),
	})
	if err != nil {
		return nil, fmt.Errorf("DynamoDB connection test failed: %w", err)
	}
	logger.Info("DynamoDB connection successful")

	// Create a retry-enabled client with default retry configuration
	retryClient := NewRetryingDynamoDBClient(client, nil)
	logger.Info("Created retry-enabled DynamoDB client",
		"maxRetries", retryClient.config.MaxRetries,
		"initialBackoffMs", retryClient.config.InitialBackoffMs,
		"maxBackoffMs", retryClient.config.MaxBackoffMs)

	return retryClient, nil
}

// GetDynamoDBService creates and returns a new DynamoDB service instance
func GetDynamoDBService(ctx context.Context) (*DynamoDBService, error) {
	logger := log.FromContext(ctx)

	tableName := os.Getenv("DYNAMODB_TABLE_NAME")
	if tableName == "" {
		tableName = "failover-operator" // Default table name
	}

	// Get cluster name from environment or hostname
	clusterName := os.Getenv("CLUSTER_NAME")
	if clusterName == "" {
		hostname, err := os.Hostname()
		if err == nil && hostname != "" {
			clusterName = hostname
		} else {
			clusterName = "unknown-cluster"
		}
	}

	// Generate a unique operator ID
	operatorID := os.Getenv("OPERATOR_ID")
	if operatorID == "" {
		// Generate a semi-unique ID based on hostname and timestamp
		hostname, _ := os.Hostname()
		operatorID = fmt.Sprintf("%s-%d", hostname, os.Getpid())
	}

	logger.Info("Creating DynamoDB service",
		"tableName", tableName,
		"clusterName", clusterName,
		"operatorID", operatorID)

	// Create the DynamoDB client
	client, err := CreateAWSDynamoDBClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create DynamoDB client: %w", err)
	}

	// Create the DynamoDB service
	service := NewDynamoDBService(client, tableName, clusterName, operatorID)

	return service, nil
}
