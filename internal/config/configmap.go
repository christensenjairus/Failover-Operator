package config

import (
	"context"
	"fmt"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// ConfigMapManager provides functionality to retrieve and watch ConfigMaps
type ConfigMapManager struct {
	client client.Client
}

// NewConfigMapManager creates a new ConfigMap manager
func NewConfigMapManager(client client.Client) *ConfigMapManager {
	return &ConfigMapManager{
		client: client,
	}
}

// GetOperatorConfig retrieves configuration from a ConfigMap
func (m *ConfigMapManager) GetOperatorConfig(ctx context.Context, namespace, name string) (*Config, error) {
	logger := log.FromContext(ctx).WithValues(
		"namespace", namespace,
		"configMapName", name,
	)
	logger.V(1).Info("Getting operator configuration from ConfigMap")

	// First load default configuration from environment variables
	config, err := LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load default configuration: %w", err)
	}

	// Retrieve the ConfigMap
	configMap := &corev1.ConfigMap{}
	err = m.client.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}, configMap)
	if err != nil {
		return nil, fmt.Errorf("failed to get ConfigMap: %w", err)
	}

	// Update config with values from ConfigMap if they exist
	// AWS Configuration
	if region, exists := configMap.Data["AWS_REGION"]; exists && region != "" {
		config.AWSRegion = region
	}

	if endpoint, exists := configMap.Data["AWS_ENDPOINT"]; exists {
		config.AWSEndpoint = endpoint
	}

	if useLocalStr, exists := configMap.Data["AWS_USE_LOCAL_ENDPOINT"]; exists {
		config.AWSUseLocalEndpoint = parseBool(useLocalStr, config.AWSUseLocalEndpoint)
	}

	// DynamoDB Configuration
	if tableName, exists := configMap.Data["DYNAMODB_TABLE_NAME"]; exists && tableName != "" {
		config.DynamoDBTableName = tableName
	}

	// Operator Configuration
	if clusterName, exists := configMap.Data["CLUSTER_NAME"]; exists && clusterName != "" {
		config.ClusterName = clusterName
	}

	if operatorID, exists := configMap.Data["OPERATOR_ID"]; exists && operatorID != "" {
		config.OperatorID = operatorID
	}

	// Timeouts and intervals
	if reconcileStr, exists := configMap.Data["RECONCILE_INTERVAL"]; exists {
		config.ReconcileInterval = parseDuration(reconcileStr, config.ReconcileInterval)
	}

	if heartbeatStr, exists := configMap.Data["DEFAULT_HEARTBEAT_INTERVAL"]; exists {
		config.DefaultHeartbeatInterval = parseDuration(heartbeatStr, config.DefaultHeartbeatInterval)
	}

	return config, nil
}

// Helper functions for parsing ConfigMap values

func parseBool(value string, defaultValue bool) bool {
	if value == "" {
		return defaultValue
	}
	parsedValue, err := strconv.ParseBool(value)
	if err != nil {
		return defaultValue
	}
	return parsedValue
}

func parseDuration(value string, defaultValue time.Duration) time.Duration {
	if value == "" {
		return defaultValue
	}
	parsedValue, err := time.ParseDuration(value)
	if err != nil {
		return defaultValue
	}
	return parsedValue
}
