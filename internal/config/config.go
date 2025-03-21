package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all configuration values for the application
type Config struct {
	// AWS Configuration
	AWSRegion           string
	AWSAccessKey        string
	AWSSecretKey        string
	AWSSessionToken     string
	AWSEndpoint         string // For local DynamoDB testing
	AWSUseLocalEndpoint bool

	// DynamoDB Configuration
	DynamoDBTableName string

	// Operator Configuration
	ClusterName string
	OperatorID  string

	// Timeouts and intervals
	ReconcileInterval        time.Duration
	DefaultHeartbeatInterval time.Duration
}

// LoadConfig loads configuration from environment variables
func LoadConfig() (*Config, error) {
	// Get environment variables with logging
	awsRegion := getEnv("AWS_REGION", "us-west-2")
	awsAccessKey := getEnv("AWS_ACCESS_KEY_ID", "")
	awsSecretKey := getEnv("AWS_SECRET_ACCESS_KEY", "")
	awsSessionToken := getEnv("AWS_SESSION_TOKEN", "")
	awsEndpoint := getEnv("AWS_ENDPOINT", "")
	awsUseLocalEndpoint := getEnvBool("AWS_USE_LOCAL_ENDPOINT", false)

	// Only use local endpoint if explicitly requested AND endpoint is specified
	if awsUseLocalEndpoint && awsEndpoint == "" {
		fmt.Println("WARNING: AWS_USE_LOCAL_ENDPOINT is true but AWS_ENDPOINT is not set. Disabling local endpoint.")
		awsUseLocalEndpoint = false
	}

	// If real AWS credentials are provided, prefer those over local endpoint
	// unless local endpoint was explicitly requested
	hasAwsCredentials := awsAccessKey != "" && awsSecretKey != ""
	if hasAwsCredentials && awsUseLocalEndpoint {
		fmt.Println("NOTE: Both AWS credentials and local endpoint configuration detected.")
		fmt.Println("Using configuration specified by AWS_USE_LOCAL_ENDPOINT:", awsUseLocalEndpoint)
	}

	dynamoDBTableName := getEnv("DYNAMODB_TABLE_NAME", "failover-operator")
	clusterName := getEnv("CLUSTER_NAME", "default-cluster")
	operatorID := getEnv("OPERATOR_ID", "failover-operator")

	// Log the loaded environment variables
	fmt.Printf("Loaded environment variables:\n")
	fmt.Printf("AWS_REGION: %s\n", awsRegion)
	fmt.Printf("AWS_ENDPOINT: %s\n", awsEndpoint)
	fmt.Printf("AWS_USE_LOCAL_ENDPOINT: %v\n", awsUseLocalEndpoint)
	fmt.Printf("DYNAMODB_TABLE_NAME: %s\n", dynamoDBTableName)
	fmt.Printf("CLUSTER_NAME: %s\n", clusterName)
	fmt.Printf("OPERATOR_ID: %s\n", operatorID)

	config := &Config{
		// AWS Configuration
		AWSRegion:           awsRegion,
		AWSAccessKey:        awsAccessKey,
		AWSSecretKey:        awsSecretKey,
		AWSSessionToken:     awsSessionToken,
		AWSEndpoint:         awsEndpoint,
		AWSUseLocalEndpoint: awsUseLocalEndpoint,

		// DynamoDB Configuration
		DynamoDBTableName: dynamoDBTableName,

		// Operator Configuration
		ClusterName: clusterName,
		OperatorID:  operatorID,

		// Timeouts and intervals (with defaults)
		ReconcileInterval:        getEnvDuration("RECONCILE_INTERVAL", 30*time.Second),
		DefaultHeartbeatInterval: getEnvDuration("DEFAULT_HEARTBEAT_INTERVAL", 30*time.Second),
	}

	// Validate required values
	if config.ClusterName == "" {
		return nil, fmt.Errorf("CLUSTER_NAME must be set")
	}

	return config, nil
}

// Helper functions for parsing environment variables

func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

func getEnvBool(key string, defaultValue bool) bool {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}
	value, err := strconv.ParseBool(valueStr)
	if err != nil {
		return defaultValue
	}
	return value
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}
	value, err := time.ParseDuration(valueStr)
	if err != nil {
		return defaultValue
	}
	return value
}
