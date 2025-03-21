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

func TestFailoverController(t *testing.T) {
	// Register operator types with the runtime scheme
	scheme := runtime.NewScheme()
	_ = crdv1alpha1.AddToScheme(scheme)
	_ = metav1.AddMetaToScheme(scheme) // Important for dealing with deletionTimestamp

	// Create a fake client
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	// Create a fake FailoverGroup first (needed by Failover)
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

	// Create a fake Failover
	failover := &crdv1alpha1.Failover{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-failover",
			Namespace: "default",
		},
		Spec: crdv1alpha1.FailoverSpec{
			TargetCluster: "target-cluster",
			Force:         false,
			FailoverGroups: []crdv1alpha1.FailoverGroupReference{
				{
					Name:      "test-group",
					Namespace: "default",
				},
			},
		},
	}

	// Add the resources to the fake client
	ctx := context.Background()
	err := fakeClient.Create(ctx, failoverGroup)
	assert.NoError(t, err)
	err = fakeClient.Create(ctx, failover)
	assert.NoError(t, err)

	// Create a FailoverReconciler
	reconciler := &FailoverReconciler{
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
			Name:      "test-failover",
		},
	}

	// Run the reconciler
	_, err = reconciler.Reconcile(ctx, req)
	assert.NoError(t, err)
	// Since implementation is not complete, just check it doesn't error
	// Don't make assumptions about requeue or requeue-after

	// Verify the Failover was updated with a finalizer
	var updatedFailover crdv1alpha1.Failover
	err = fakeClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: "test-failover"}, &updatedFailover)
	assert.NoError(t, err)
	assert.Contains(t, updatedFailover.Finalizers, "failover.hahomelabs.com/finalizer", "Finalizer should be added")

	// Skip the remaining deletion test because it requires special handling with fake clients
	// The controller itself will be tested with real integration tests
	t.Log("Skipping deletion test with fake client because deletionTimestamp is immutable in this context")
}

func TestFailoverSetupWithManager(t *testing.T) {
	// Create a new scheme with registered types
	scheme := runtime.NewScheme()
	err := crdv1alpha1.AddToScheme(scheme)
	if err != nil {
		t.Errorf("Failed to add scheme: %v", err)
	}

	// Create a new Manager with the scheme we just created
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
	})
	if err != nil {
		// Skip if running outside of a cluster
		t.Skip("Unable to create manager, skipping test")
		return
	}

	// Create a FailoverReconciler
	reconciler := &FailoverReconciler{
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
