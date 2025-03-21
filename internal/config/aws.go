package config

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// AWSClientFactory creates AWS service clients
type AWSClientFactory struct {
	Config *Config
}

// NewAWSClientFactory creates a new AWS client factory
func NewAWSClientFactory(cfg *Config) *AWSClientFactory {
	return &AWSClientFactory{
		Config: cfg,
	}
}

// CreateDynamoDBClient creates a new DynamoDB client
func (f *AWSClientFactory) CreateDynamoDBClient(ctx context.Context) (*dynamodb.Client, error) {
	logger := log.FromContext(ctx)
	logger.V(1).Info("Creating DynamoDB client",
		"region", f.Config.AWSRegion,
		"useLocalEndpoint", f.Config.AWSUseLocalEndpoint,
		"endpoint", f.Config.AWSEndpoint,
	)

	// Create AWS config options
	var options []func(*config.LoadOptions) error

	// If credentials provided explicitly, use them
	if f.Config.AWSAccessKey != "" && f.Config.AWSSecretKey != "" {
		logger.V(1).Info("Using provided AWS credentials")

		credProvider := credentials.NewStaticCredentialsProvider(
			f.Config.AWSAccessKey,
			f.Config.AWSSecretKey,
			f.Config.AWSSessionToken,
		)
		options = append(options, config.WithCredentialsProvider(credProvider))
	} else {
		logger.V(1).Info("Using default AWS credential provider chain")
	}

	// Set AWS region
	options = append(options, config.WithRegion(f.Config.AWSRegion))

	// Load AWS configuration
	awsCfg, err := config.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS configuration: %w", err)
	}

	// Create DynamoDB client
	var dynamoDBOptions []func(*dynamodb.Options)

	// Use local endpoint for testing if configured
	if f.Config.AWSUseLocalEndpoint && f.Config.AWSEndpoint != "" {
		logger.V(1).Info("Using local DynamoDB endpoint", "endpoint", f.Config.AWSEndpoint)

		dynamoDBOptions = append(dynamoDBOptions, func(o *dynamodb.Options) {
			o.BaseEndpoint = aws.String(f.Config.AWSEndpoint)
		})
	}

	return dynamodb.NewFromConfig(awsCfg, dynamoDBOptions...), nil
}
