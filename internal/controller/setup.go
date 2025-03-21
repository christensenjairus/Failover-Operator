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
	if err := (&failover.FailoverReconciler{
		Client:      mgr.GetClient(),
		Scheme:      mgr.GetScheme(),
		ClusterName: config.ClusterName,
		Logger:      mgr.GetLogger().WithName("controllers").WithName("Failover"),
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("failed to setup Failover controller: %w", err)
	}

	// Set up FailoverGroup controller
	if err := (&failovergroup.FailoverGroupReconciler{
		Client:      mgr.GetClient(),
		Scheme:      mgr.GetScheme(),
		ClusterName: config.ClusterName,
		Log:         mgr.GetLogger().WithName("controllers").WithName("FailoverGroup"),
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("failed to setup FailoverGroup controller: %w", err)
	}

	// Setup DynamoDB if configured
	if err := setupDynamoDB(mgr.GetLogger(), config); err != nil {
		return fmt.Errorf("failed to setup DynamoDB: %w", err)
	}

	return nil
}

// setupDynamoDB initializes DynamoDB for the operator
func setupDynamoDB(logger logr.Logger, cfg *config.Config) error {
	ctx := context.Background()
	log := logger.WithName("dynamodb-setup")

	// Skip if no table name is specified
	if cfg.DynamoDBTableName == "" {
		log.Info("DynamoDB table name not specified, skipping setup")
		return nil
	}

	// Create AWS client factory and DynamoDB client
	factory := config.NewAWSClientFactory(cfg)
	dynamoClient, err := factory.CreateDynamoDBClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create DynamoDB client: %w", err)
	}

	// Setup DynamoDB table
	log.Info("Setting up DynamoDB table", "tableName", cfg.DynamoDBTableName)

	options := dynamodb.DefaultTableSetupOptions(cfg.DynamoDBTableName)
	if err := dynamodb.SetupDynamoDBTable(ctx, dynamoClient, options); err != nil {
		return fmt.Errorf("failed to setup DynamoDB table: %w", err)
	}

	log.Info("DynamoDB setup completed successfully")
	return nil
}
