package controller

import (
	"context"
	"fmt"

	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
	"github.com/christensenjairus/Failover-Operator/internal/config"
	"github.com/christensenjairus/Failover-Operator/internal/controller/dynamodb"
	"github.com/christensenjairus/Failover-Operator/internal/controller/failover"
	"github.com/christensenjairus/Failover-Operator/internal/controller/failovergroup"
	replicationv1alpha1 "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
)

// RegisterSchemes registers all required API schemas
func RegisterSchemes(scheme *runtime.Scheme) error {
	if err := crdv1alpha1.AddToScheme(scheme); err != nil {
		return err
	}
	if err := replicationv1alpha1.AddToScheme(scheme); err != nil {
		return err
	}
	return nil
}

// SetupControllers sets up all controllers with the manager
func SetupControllers(mgr ctrl.Manager, config *config.Config) error {
	// Set up Failover controller
	failoverReconciler := &failover.FailoverReconciler{
		Client:      mgr.GetClient(),
		Scheme:      mgr.GetScheme(),
		ClusterName: config.ClusterName,
		Logger:      mgr.GetLogger().WithName("controllers").WithName("Failover"),
	}

	// Set up FailoverGroup controller
	failoverGroupReconciler := &failovergroup.FailoverGroupReconciler{
		Client:      mgr.GetClient(),
		Scheme:      mgr.GetScheme(),
		Log:         mgr.GetLogger().WithName("controllers").WithName("FailoverGroup"),
		ClusterName: config.ClusterName,
	}

	// Setup DynamoDB if configured
	dynamoDBService, err := setupDynamoDB(mgr.GetLogger(), config)
	if err != nil {
		return fmt.Errorf("failed to setup DynamoDB: %w", err)
	}

	// If DynamoDB is configured, set the manager in the controllers
	if dynamoDBService != nil {
		// Create the FailoverGroup manager and set the DynamoDB service
		failoverGroupManager := failovergroup.NewManager(mgr.GetClient(), config.ClusterName, failoverGroupReconciler.Log)
		failoverGroupManager.SetDynamoDBManager(dynamoDBService)
		failoverGroupReconciler.Manager = failoverGroupManager

		// Create the Failover manager and set the DynamoDB service
		failoverManager := failover.NewManager(mgr.GetClient(), config.ClusterName, failoverReconciler.Logger)
		failoverManager.SetDynamoDBManager(dynamoDBService)
	}

	// Register the controllers with the manager
	if err := failoverReconciler.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("failed to setup Failover controller: %w", err)
	}

	if err := failoverGroupReconciler.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("failed to setup FailoverGroup controller: %w", err)
	}

	return nil
}

// setupDynamoDB initializes DynamoDB for the operator and returns a DynamoDB service instance
func setupDynamoDB(logger logr.Logger, cfg *config.Config) (*dynamodb.DynamoDBService, error) {
	ctx := context.Background()
	log := logger.WithName("dynamodb-setup")

	// Skip if no table name is specified
	if cfg.DynamoDBTableName == "" {
		log.Info("DynamoDB table name not specified, skipping setup")
		return nil, nil
	}

	// Create AWS client factory and DynamoDB client
	factory := config.NewAWSClientFactory(cfg)
	dynamoClient, err := factory.CreateDynamoDBClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create DynamoDB client: %w", err)
	}

	// Skip table creation - assume the table already exists
	log.Info("Skipping DynamoDB table creation - assuming table already exists", "tableName", cfg.DynamoDBTableName)

	// Create and return a DynamoDB service instance
	dynamoDBService := dynamodb.NewDynamoDBService(dynamoClient, cfg.DynamoDBTableName, cfg.ClusterName, cfg.OperatorID)
	log.Info("Created DynamoDB service",
		"tableName", cfg.DynamoDBTableName,
		"clusterName", cfg.ClusterName,
		"operatorID", cfg.OperatorID)

	return dynamoDBService, nil
}
