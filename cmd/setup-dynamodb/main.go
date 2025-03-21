package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	dynamodbcontroller "github.com/christensenjairus/Failover-Operator/internal/controller/dynamodb"
)

func main() {
	var (
		tableName        string
		awsRegion        string
		awsEndpoint      string
		useLocalEndpoint bool
		awsAccessKey     string
		awsSecretKey     string
		awsProfile       string
		payPerRequest    bool
		readCapacity     int64
		writeCapacity    int64
		verbose          bool
	)

	// Define command line flags
	flag.StringVar(&tableName, "table-name", "failover-operator", "DynamoDB table name to create")
	flag.StringVar(&awsRegion, "region", "us-west-2", "AWS region")
	flag.StringVar(&awsEndpoint, "endpoint", "", "Custom AWS endpoint (for local DynamoDB)")
	flag.BoolVar(&useLocalEndpoint, "local", false, "Use local DynamoDB endpoint")
	flag.StringVar(&awsAccessKey, "access-key", "", "AWS access key ID")
	flag.StringVar(&awsSecretKey, "secret-key", "", "AWS secret access key")
	flag.StringVar(&awsProfile, "profile", "", "AWS profile to use (from ~/.aws/credentials)")
	flag.BoolVar(&payPerRequest, "pay-per-request", true, "Use pay-per-request billing mode (false for provisioned)")
	flag.Int64Var(&readCapacity, "read-capacity", 5, "Provisioned read capacity (only used with provisioned billing)")
	flag.Int64Var(&writeCapacity, "write-capacity", 5, "Provisioned write capacity (only used with provisioned billing)")
	flag.BoolVar(&verbose, "verbose", false, "Enable verbose logging")

	// Parse flags and set up logging
	flag.Parse()

	opts := zap.Options{
		Development: true,
	}
	if verbose {
		opts.DestWriter = os.Stdout
		opts.Development = true // Set development mode for more verbose output
	}
	log.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	logger := log.Log.WithName("setup-dynamodb")

	// Use environment variables if not specified on command line
	if awsAccessKey == "" {
		awsAccessKey = os.Getenv("AWS_ACCESS_KEY_ID")
	}
	if awsSecretKey == "" {
		awsSecretKey = os.Getenv("AWS_SECRET_ACCESS_KEY")
	}
	if awsRegion == "us-west-2" && os.Getenv("AWS_REGION") != "" {
		awsRegion = os.Getenv("AWS_REGION")
	}
	if awsProfile == "" && os.Getenv("AWS_PROFILE") != "" {
		awsProfile = os.Getenv("AWS_PROFILE")
	}
	if awsEndpoint == "" && os.Getenv("AWS_ENDPOINT") != "" {
		awsEndpoint = os.Getenv("AWS_ENDPOINT")
		useLocalEndpoint = true
	}
	if useLocalEndpoint && awsEndpoint == "" {
		awsEndpoint = "http://localhost:8000"
	}

	// Log configuration
	logger.Info("Starting DynamoDB table setup",
		"tableName", tableName,
		"region", awsRegion,
		"endpoint", awsEndpoint,
		"useLocalEndpoint", useLocalEndpoint,
		"profile", awsProfile,
		"billingMode", func() string {
			if payPerRequest {
				return "PAY_PER_REQUEST"
			}
			return "PROVISIONED"
		}())

	// Create context
	ctx := context.Background()
	ctx = log.IntoContext(ctx, logger)

	// Create DynamoDB client
	client, err := createDynamoDBClient(ctx, logger, awsRegion, awsEndpoint, useLocalEndpoint, awsAccessKey, awsSecretKey, awsProfile)
	if err != nil {
		logger.Error(err, "Failed to create DynamoDB client")
		os.Exit(1)
	}

	// Set up table options
	options := &dynamodbcontroller.TableSetupOptions{
		TableName:      tableName,
		WaitForActive:  true,
		MaxWaitSeconds: 300,
	}

	if payPerRequest {
		options.BillingMode = types.BillingModePayPerRequest
	} else {
		options.BillingMode = types.BillingModeProvisioned
		options.ProvisionedThroughput = &types.ProvisionedThroughput{
			ReadCapacityUnits:  aws.Int64(readCapacity),
			WriteCapacityUnits: aws.Int64(writeCapacity),
		}
	}

	// Create the table
	logger.Info("Setting up DynamoDB table",
		"tableName", tableName,
		"region", awsRegion,
		"endpoint", awsEndpoint,
		"billingMode", options.BillingMode)

	startTime := time.Now()
	err = dynamodbcontroller.SetupDynamoDBTable(ctx, client, options)
	if err != nil {
		logger.Error(err, "Failed to set up DynamoDB table")
		os.Exit(1)
	}

	// Report success
	duration := time.Since(startTime)
	logger.Info("DynamoDB table setup completed successfully",
		"tableName", tableName,
		"duration", duration.String())
}

// createDynamoDBClient creates a DynamoDB client with the given configuration
func createDynamoDBClient(ctx context.Context, logger logr.Logger, region, endpoint string, useLocalEndpoint bool, accessKey, secretKey, profile string) (*dynamodb.Client, error) {
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

	return client, nil
}
