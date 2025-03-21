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

package failover

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
	"github.com/christensenjairus/Failover-Operator/internal/controller/dynamodb"
)

func TestFailoverController(t *testing.T) {
	// Register the scheme
	scheme := runtime.NewScheme()
	err := clientgoscheme.AddToScheme(scheme)
	require.NoError(t, err)
	err = crdv1alpha1.AddToScheme(scheme)
	require.NoError(t, err)

	// Create test resources
	failoverGroup := &crdv1alpha1.FailoverGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-group",
			Namespace: "default",
		},
		Spec: crdv1alpha1.FailoverGroupSpec{
			FailoverMode: "safe",
			// Add additional spec fields as needed
		},
		Status: crdv1alpha1.FailoverGroupStatus{
			// Add status fields as needed
		},
	}

	failover := &crdv1alpha1.Failover{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-failover",
			Namespace: "default",
		},
		Spec: crdv1alpha1.FailoverSpec{
			TargetCluster: "target-cluster",
			FailoverGroups: []crdv1alpha1.FailoverGroupReference{
				{
					Name: "test-group",
				},
			},
		},
	}

	// Create a fake client
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	// Add the resources to the fake client
	ctx := context.Background()
	err = fakeClient.Create(ctx, failoverGroup)
	assert.NoError(t, err)
	err = fakeClient.Create(ctx, failover)
	assert.NoError(t, err)

	// Create a logger
	logger := zap.New(zap.UseDevMode(true))

	// Create a FailoverReconciler
	reconciler := &FailoverReconciler{
		Client:      fakeClient,
		Logger:      logger,
		Scheme:      scheme,
		ClusterName: "test-cluster",
	}

	// Create and set the FailoverManager
	failoverManager := NewManager(fakeClient, "test-cluster", logger)
	dynamodbService := &dynamodb.DynamoDBService{}
	failoverManager.SetDynamoDBManager(dynamodbService)
	reconciler.FailoverManager = failoverManager

	// Test the reconcile function
	_, err = reconciler.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-failover",
			Namespace: "default",
		},
	})
	assert.NoError(t, err)

	// Verify the reconciler processed the failover
	updatedFailover := &crdv1alpha1.Failover{}
	err = fakeClient.Get(ctx, client.ObjectKey{
		Name:      "test-failover",
		Namespace: "default",
	}, updatedFailover)
	assert.NoError(t, err)

	// Check if the finalizer was added
	assert.True(t, containsFinalizer(updatedFailover, finalizerName))
}

func TestFailoverSetupWithManager(t *testing.T) {
	// Register the scheme
	scheme := runtime.NewScheme()
	err := clientgoscheme.AddToScheme(scheme)
	require.NoError(t, err)
	err = crdv1alpha1.AddToScheme(scheme)
	require.NoError(t, err)

	// Create a manager
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
	})
	require.NoError(t, err)

	// Create a logger
	logger := zap.New(zap.UseDevMode(true))

	// Create a reconciler
	reconciler := &FailoverReconciler{
		Client:      mgr.GetClient(),
		Logger:      logger,
		Scheme:      mgr.GetScheme(),
		ClusterName: "test-cluster",
	}

	// Test SetupWithManager
	err = reconciler.SetupWithManager(mgr)
	assert.NoError(t, err)
}

// Helper function to check if a finalizer is present
func containsFinalizer(obj metav1.Object, finalizer string) bool {
	for _, fin := range obj.GetFinalizers() {
		if fin == finalizer {
			return true
		}
	}
	return false
}
