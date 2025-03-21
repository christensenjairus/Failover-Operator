package config

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// SecretsManager provides functionality to retrieve secrets
type SecretsManager struct {
	client client.Client
}

// NewSecretsManager creates a new secrets manager
func NewSecretsManager(client client.Client) *SecretsManager {
	return &SecretsManager{
		client: client,
	}
}

// AWSCredentials contains AWS credentials loaded from a secret
type AWSCredentials struct {
	AccessKey    string
	SecretKey    string
	SessionToken string
	Region       string
}

// GetAWSCredentials retrieves AWS credentials from a Kubernetes secret
func (m *SecretsManager) GetAWSCredentials(ctx context.Context, namespace, secretName string) (*AWSCredentials, error) {
	logger := log.FromContext(ctx).WithValues(
		"namespace", namespace,
		"secretName", secretName,
	)
	logger.V(1).Info("Getting AWS credentials from secret")

	// Retrieve the secret
	secret := &corev1.Secret{}
	err := m.client.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      secretName,
	}, secret)
	if err != nil {
		return nil, fmt.Errorf("failed to get AWS credentials secret: %w", err)
	}

	// Extract AWS credentials
	credentials := &AWSCredentials{}

	if accessKey, ok := secret.Data["access_key"]; ok {
		credentials.AccessKey = string(accessKey)
	} else {
		return nil, fmt.Errorf("AWS access key not found in secret")
	}

	if secretKey, ok := secret.Data["secret_key"]; ok {
		credentials.SecretKey = string(secretKey)
	} else {
		return nil, fmt.Errorf("AWS secret key not found in secret")
	}

	// These fields are optional
	if sessionToken, ok := secret.Data["session_token"]; ok {
		credentials.SessionToken = string(sessionToken)
	}

	if region, ok := secret.Data["region"]; ok {
		credentials.Region = string(region)
	}

	return credentials, nil
}

// UpdateConfigWithSecret updates the config with credentials from a secret
func (m *SecretsManager) UpdateConfigWithSecret(ctx context.Context, cfg *Config, namespace, secretName string) error {
	// Only attempt to load from secret if secret name is provided
	if secretName == "" {
		return nil
	}

	credentials, err := m.GetAWSCredentials(ctx, namespace, secretName)
	if err != nil {
		return err
	}

	// Update config with secret values, but don't override if config already has values
	if cfg.AWSAccessKey == "" {
		cfg.AWSAccessKey = credentials.AccessKey
	}

	if cfg.AWSSecretKey == "" {
		cfg.AWSSecretKey = credentials.SecretKey
	}

	if cfg.AWSSessionToken == "" && credentials.SessionToken != "" {
		cfg.AWSSessionToken = credentials.SessionToken
	}

	if cfg.AWSRegion == "" && credentials.Region != "" {
		cfg.AWSRegion = credentials.Region
	}

	return nil
}
