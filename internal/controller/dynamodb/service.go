// Package dynamodb provides DynamoDB integration for state management
package dynamodb

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

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

	// Configure connection pooling and timeouts
	options = append(options, configureHTTPClientSettings())

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

// configureHTTPClientSettings sets up optimal HTTP client settings for DynamoDB
func configureHTTPClientSettings() func(*awsconfig.LoadOptions) error {
	return func(opts *awsconfig.LoadOptions) error {
		// Get connection pool settings from environment or use defaults
		timeoutSec, _ := strconv.Atoi(getEnvWithDefault("AWS_HTTP_TIMEOUT_SEC", "30"))
		keepAliveSec, _ := strconv.Atoi(getEnvWithDefault("AWS_HTTP_KEEPALIVE_SEC", "30"))
		maxIdleConns, _ := strconv.Atoi(getEnvWithDefault("AWS_HTTP_MAX_IDLE_CONNS", "100"))
		maxIdleConnsPerHost, _ := strconv.Atoi(getEnvWithDefault("AWS_HTTP_MAX_IDLE_CONNS_PER_HOST", "100"))
		idleConnTimeoutSec, _ := strconv.Atoi(getEnvWithDefault("AWS_HTTP_IDLE_CONN_TIMEOUT_SEC", "90"))

		// Create a custom HTTP client with optimized settings
		transport := &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   time.Duration(timeoutSec) * time.Second,
				KeepAlive: time.Duration(keepAliveSec) * time.Second,
			}).DialContext,
			MaxIdleConns:          maxIdleConns,
			MaxIdleConnsPerHost:   maxIdleConnsPerHost,
			IdleConnTimeout:       time.Duration(idleConnTimeoutSec) * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ForceAttemptHTTP2:     true,
		}

		httpClient := &http.Client{
			Transport: transport,
			Timeout:   time.Duration(timeoutSec) * time.Second,
		}

		// Apply the custom HTTP client to the AWS configuration
		opts.HTTPClient = httpClient

		return nil
	}
}

// getEnvWithDefault gets an environment variable or returns a default value
func getEnvWithDefault(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
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
