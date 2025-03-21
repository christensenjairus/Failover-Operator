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
	config := &Config{
		// AWS Configuration
		AWSRegion:           getEnv("AWS_REGION", "us-west-2"),
		AWSAccessKey:        getEnv("AWS_ACCESS_KEY_ID", ""),
		AWSSecretKey:        getEnv("AWS_SECRET_ACCESS_KEY", ""),
		AWSSessionToken:     getEnv("AWS_SESSION_TOKEN", ""),
		AWSEndpoint:         getEnv("AWS_ENDPOINT", ""),
		AWSUseLocalEndpoint: getEnvBool("AWS_USE_LOCAL_ENDPOINT", false),

		// DynamoDB Configuration
		DynamoDBTableName: getEnv("DYNAMODB_TABLE_NAME", "failover-operator"),

		// Operator Configuration
		ClusterName: getEnv("CLUSTER_NAME", "default-cluster"),
		OperatorID:  getEnv("OPERATOR_ID", "failover-operator"),

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
