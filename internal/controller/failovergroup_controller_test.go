/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
	"github.com/christensenjairus/Failover-Operator/internal/controller/deployments"
	"github.com/christensenjairus/Failover-Operator/internal/controller/dynamodb"
)

// Create a basic mock for the DynamoDB client
type mockDynamoDBClient struct {
	dynamodb.DynamoDBClient
}

func TestFailoverGroupController(t *testing.T) {
	// Register operator types with the runtime scheme
	scheme := runtime.NewScheme()
	_ = crdv1alpha1.AddToScheme(scheme)

	// Create a fake client
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	// Create a fake FailoverGroup
	failoverGroup := &crdv1alpha1.FailoverGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-group",
			Namespace: "default",
		},
		Spec: crdv1alpha1.FailoverGroupSpec{
			DefaultFailoverMode: "safe",
			Components: []crdv1alpha1.ComponentSpec{
				{
					Name: "test-component",
				},
			},
		},
	}

	// Add the FailoverGroup to the fake client
	ctx := context.Background()
	err := fakeClient.Create(ctx, failoverGroup)
	assert.NoError(t, err)

	// Create a FailoverGroupReconciler
	reconciler := &FailoverGroupReconciler{
		Client:             fakeClient,
		Scheme:             scheme,
		Log:                zap.New(zap.UseDevMode(true)),
		ClusterName:        "test-cluster",
		DeploymentsManager: deployments.NewManager(fakeClient),
		// TODO: Initialize other managers as needed
	}

	// Set up mock for DynamoDB manager
	mockClient := &TestDynamoDBClient{}
	reconciler.DynamoDBManager = dynamodb.NewDynamoDBService(mockClient, "test-table", "test-cluster", "test-operator")

	// Test reconciliation
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "default",
			Name:      "test-group",
		},
	}

	// Run the reconciler
	result, err := reconciler.Reconcile(ctx, req)
	assert.NoError(t, err)
	assert.True(t, result.Requeue || result.RequeueAfter > 0, "Expected requeue or requeue after to be set")

	// Verify the FailoverGroup was updated with a finalizer
	var updatedFailoverGroup crdv1alpha1.FailoverGroup
	err = fakeClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: "test-group"}, &updatedFailoverGroup)
	assert.NoError(t, err)
	assert.Contains(t, updatedFailoverGroup.Finalizers, "failovergroup.hahomelabs.com/finalizer", "Finalizer should be added")

	// Test basic state management
	// TODO: Expand test to cover more functionality once implemented
	if updatedFailoverGroup.Status.State == "" {
		// Reconcile again to initialize state
		_, err = reconciler.Reconcile(ctx, req)
		assert.NoError(t, err)

		// Check that state was initialized
		err = fakeClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: "test-group"}, &updatedFailoverGroup)
		assert.NoError(t, err)
		assert.Equal(t, string(crdv1alpha1.FailoverGroupStateStandby), updatedFailoverGroup.Status.State, "Status should be initialized to STANDBY")
	}

	// Test deletion handling
	// Mark the FailoverGroup for deletion
	now := metav1.Now()
	updatedFailoverGroup.DeletionTimestamp = &now
	err = fakeClient.Update(ctx, &updatedFailoverGroup)
	assert.NoError(t, err)

	// Reconcile again to handle deletion
	_, err = reconciler.Reconcile(ctx, req)
	assert.NoError(t, err)

	// Verify finalizer was removed
	err = fakeClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: "test-group"}, &updatedFailoverGroup)
	assert.NoError(t, err)
	assert.NotContains(t, updatedFailoverGroup.Finalizers, "failovergroup.hahomelabs.com/finalizer", "Finalizer should be removed")
}

func TestFailoverGroupSetupWithManager(t *testing.T) {
	// Create a new Manager
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: runtime.NewScheme(),
	})
	if err != nil {
		// Skip if running outside of a cluster
		t.Skip("Unable to create manager, skipping test")
	}

	// Create a FailoverGroupReconciler
	reconciler := &FailoverGroupReconciler{
		Client:      mgr.GetClient(),
		Scheme:      mgr.GetScheme(),
		ClusterName: "test-cluster",
	}

	// Test SetupWithManager
	err = reconciler.SetupWithManager(mgr)
	assert.NoError(t, err)

	// Verify that managers are initialized
	assert.NotNil(t, reconciler.DeploymentsManager)
	assert.NotNil(t, reconciler.StatefulSetsManager)
	assert.NotNil(t, reconciler.CronJobsManager)
	assert.NotNil(t, reconciler.KustomizationsManager)
	assert.NotNil(t, reconciler.HelmReleasesManager)
	assert.NotNil(t, reconciler.VirtualServicesManager)
	assert.NotNil(t, reconciler.VolumeReplicationsManager)
	assert.NotNil(t, reconciler.IngressesManager)

	// DynamoDBManager is not initialized in this test as it requires AWS config
	assert.Nil(t, reconciler.DynamoDBManager)

	// TODO: Test with initialized DynamoDBManager
}
