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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
)

func TestCheckAutomaticFailoverTriggers(t *testing.T) {
	// Setup a test scheme
	scheme := runtime.NewScheme()
	err := crdv1alpha1.AddToScheme(scheme)
	require.NoError(t, err)

	// Create test cases
	testCases := []struct {
		name           string
		group          *crdv1alpha1.FailoverGroup
		expectFailover bool
	}{
		{
			name: "suspended_group",
			group: createTestFailoverGroup("test-suspended", "default", true, "Ok", "10s", "10s", "10s",
				[]crdv1alpha1.ClusterInfo{
					{
						Name:          "cluster1",
						Role:          "PRIMARY",
						Health:        "ERROR",
						LastHeartbeat: time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
					},
					{
						Name:   "cluster2",
						Role:   "STANDBY",
						Health: "OK",
					},
				}),
			expectFailover: false, // Suspended, so no failover expected
		},
		{
			name: "healthy_primary",
			group: createTestFailoverGroup("test-healthy", "default", false, "Ok", "10s", "10s", "10s",
				[]crdv1alpha1.ClusterInfo{
					{
						Name:          "cluster1",
						Role:          "PRIMARY",
						Health:        "OK",
						LastHeartbeat: time.Now().Format(time.RFC3339),
					},
					{
						Name:   "cluster2",
						Role:   "STANDBY",
						Health: "OK",
					},
				}),
			expectFailover: false, // Primary is healthy
		},
		{
			name: "no_standby_cluster",
			group: createTestFailoverGroup("test-no-standby", "default", false, "Ok", "10s", "10s", "10s",
				[]crdv1alpha1.ClusterInfo{
					{
						Name:          "cluster1",
						Role:          "PRIMARY",
						Health:        "ERROR",
						LastHeartbeat: time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
					},
				}),
			expectFailover: false, // No healthy standby available
		},
		{
			name: "primary_health_error",
			group: createTestFailoverGroup("test-health-error", "default", false, "ERROR", "10s", "10s", "10s",
				[]crdv1alpha1.ClusterInfo{
					{
						Name:          "cluster1",
						Role:          "PRIMARY",
						Health:        "ERROR",
						LastHeartbeat: time.Now().Format(time.RFC3339),
					},
					{
						Name:   "cluster2",
						Role:   "STANDBY",
						Health: "OK",
					},
				}),
			expectFailover: false, // Return value will be false because hasBeenUnhealthyLongEnough is a placeholder
		},
		{
			name: "heartbeat_timeout",
			group: createTestFailoverGroup("test-heartbeat", "default", false, "Ok", "10s", "10s", "1s",
				[]crdv1alpha1.ClusterInfo{
					{
						Name:          "cluster1",
						Role:          "PRIMARY",
						Health:        "OK",
						LastHeartbeat: time.Now().Add(-10 * time.Second).Format(time.RFC3339),
					},
					{
						Name:   "cluster2",
						Role:   "STANDBY",
						Health: "OK",
					},
				}),
			expectFailover: true, // Heartbeat timeout should trigger failover
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a fake client with the test group
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tc.group).
				Build()

			// Create the failover manager
			manager := NewManager(fakeClient, "test-cluster", zap.New(zap.UseDevMode(true)))

			// Run the automatic failover check
			ctx := context.Background()
			err := manager.CheckAutomaticFailoverTriggers(ctx)
			require.NoError(t, err)

			// Check if a failover was created
			failoverList := &crdv1alpha1.FailoverList{}
			err = fakeClient.List(ctx, failoverList)
			require.NoError(t, err)

			if tc.expectFailover {
				assert.NotEmpty(t, failoverList.Items, "Expected a failover to be created")
				if len(failoverList.Items) > 0 {
					failover := failoverList.Items[0]
					assert.Equal(t, tc.group.Name, failover.Spec.FailoverGroups[0].Name)
					assert.Equal(t, tc.group.Namespace, failover.Spec.FailoverGroups[0].Namespace)
					assert.True(t, failover.Spec.ForceFastMode, "Automatic failover should use fast mode")
				}
			} else {
				assert.Empty(t, failoverList.Items, "Did not expect a failover to be created")
			}
		})
	}
}

func TestFindPrimaryCluster(t *testing.T) {
	// Create a failover group with a primary and standby
	group := createTestFailoverGroup("test-group", "default", false, "Ok", "10s", "10s", "10s",
		[]crdv1alpha1.ClusterInfo{
			{
				Name:   "cluster1",
				Role:   "PRIMARY",
				Health: "OK",
			},
			{
				Name:   "cluster2",
				Role:   "STANDBY",
				Health: "OK",
			},
		})

	// Create the manager
	fakeClient := fake.NewClientBuilder().Build()
	manager := NewManager(fakeClient, "test-cluster", zap.New(zap.UseDevMode(true)))

	// Find the primary cluster
	primary, err := manager.findPrimaryCluster(group)
	assert.NoError(t, err)
	assert.Equal(t, "cluster1", primary.Name)
	assert.Equal(t, "PRIMARY", primary.Role)
}

func TestFindHealthyStandbyCluster(t *testing.T) {
	// Create a failover group with multiple standbys of varying health
	group := createTestFailoverGroup("test-group", "default", false, "Ok", "10s", "10s", "10s",
		[]crdv1alpha1.ClusterInfo{
			{
				Name:   "cluster1",
				Role:   "PRIMARY",
				Health: "OK",
			},
			{
				Name:   "cluster2",
				Role:   "STANDBY",
				Health: "ERROR", // Unhealthy
			},
			{
				Name:   "cluster3",
				Role:   "STANDBY",
				Health: "OK", // This should be chosen
			},
			{
				Name:   "cluster4",
				Role:   "STANDBY",
				Health: "DEGRADED", // This is acceptable but should be chosen after OK
			},
		})

	// Create the manager
	fakeClient := fake.NewClientBuilder().Build()
	manager := NewManager(fakeClient, "test-cluster", zap.New(zap.UseDevMode(true)))

	// Find a healthy standby cluster
	standby, err := manager.findHealthyStandbyCluster(group)
	assert.NoError(t, err)
	assert.Equal(t, "cluster3", standby.Name, "Should pick the OK standby over DEGRADED")
	assert.Equal(t, "STANDBY", standby.Role)
	assert.Equal(t, "OK", standby.Health)

	// Modify the group to remove the OK standby
	group.Status.GlobalState.Clusters = []crdv1alpha1.ClusterInfo{
		{
			Name:   "cluster1",
			Role:   "PRIMARY",
			Health: "OK",
		},
		{
			Name:   "cluster2",
			Role:   "STANDBY",
			Health: "ERROR", // Unhealthy
		},
		{
			Name:   "cluster4",
			Role:   "STANDBY",
			Health: "DEGRADED", // Now this should be chosen
		},
	}

	// Try again - should pick the DEGRADED standby now
	standby, err = manager.findHealthyStandbyCluster(group)
	assert.NoError(t, err)
	assert.Equal(t, "cluster4", standby.Name)
	assert.Equal(t, "DEGRADED", standby.Health)

	// Modify the group to have no healthy standbys
	group.Status.GlobalState.Clusters = []crdv1alpha1.ClusterInfo{
		{
			Name:   "cluster1",
			Role:   "PRIMARY",
			Health: "OK",
		},
		{
			Name:   "cluster2",
			Role:   "STANDBY",
			Health: "ERROR",
		},
	}

	// Should return an error now
	_, err = manager.findHealthyStandbyCluster(group)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no healthy standby cluster found")
}

func TestHasHeartbeatTimeout(t *testing.T) {
	// Create a failover group with a heartbeat timeout
	group := createTestFailoverGroup("test-group", "default", false, "Ok", "10s", "5s", "10s", nil)

	// Create the manager
	fakeClient := fake.NewClientBuilder().Build()
	manager := NewManager(fakeClient, "test-cluster", zap.New(zap.UseDevMode(true)))

	// Test cases
	testCases := []struct {
		name           string
		primaryCluster crdv1alpha1.ClusterInfo
		expectTimeout  bool
	}{
		{
			name: "recent_heartbeat",
			primaryCluster: crdv1alpha1.ClusterInfo{
				Name:          "cluster1",
				Role:          "PRIMARY",
				Health:        "OK",
				LastHeartbeat: time.Now().Format(time.RFC3339),
			},
			expectTimeout: false,
		},
		{
			name: "old_heartbeat",
			primaryCluster: crdv1alpha1.ClusterInfo{
				Name:          "cluster1",
				Role:          "PRIMARY",
				Health:        "OK",
				LastHeartbeat: time.Now().Add(-20 * time.Second).Format(time.RFC3339),
			},
			expectTimeout: true,
		},
		{
			name: "no_heartbeat",
			primaryCluster: crdv1alpha1.ClusterInfo{
				Name:   "cluster1",
				Role:   "PRIMARY",
				Health: "OK",
				// No LastHeartbeat
			},
			expectTimeout: false,
		},
		{
			name: "invalid_heartbeat",
			primaryCluster: crdv1alpha1.ClusterInfo{
				Name:          "cluster1",
				Role:          "PRIMARY",
				Health:        "OK",
				LastHeartbeat: "invalid-date",
			},
			expectTimeout: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hasTimeout := manager.hasHeartbeatTimeout(group, tc.primaryCluster)
			assert.Equal(t, tc.expectTimeout, hasTimeout)
		})
	}
}

func TestCreateAutomaticFailover(t *testing.T) {
	// Setup a test scheme
	scheme := runtime.NewScheme()
	err := crdv1alpha1.AddToScheme(scheme)
	require.NoError(t, err)

	// Create a failover group
	group := createTestFailoverGroup("test-group", "default", false, "Ok", "10s", "10s", "10s", nil)

	// Create a fake client
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(group).
		Build()

	// Create the manager
	manager := NewManager(fakeClient, "test-cluster", zap.New(zap.UseDevMode(true)))

	// Create an automatic failover
	ctx := context.Background()
	err = manager.createAutomaticFailover(ctx, group, "target-cluster", "Test reason")
	require.NoError(t, err)

	// Check that a failover was created
	failoverList := &crdv1alpha1.FailoverList{}
	err = fakeClient.List(ctx, failoverList)
	require.NoError(t, err)
	require.Len(t, failoverList.Items, 1)

	failover := failoverList.Items[0]
	assert.Equal(t, group.Name, failover.Spec.FailoverGroups[0].Name)
	assert.Equal(t, group.Namespace, failover.Spec.FailoverGroups[0].Namespace)
	assert.Equal(t, "target-cluster", failover.Spec.TargetCluster)
	assert.True(t, failover.Spec.ForceFastMode)
	assert.Contains(t, failover.Spec.Reason, "Test reason")
}

func TestGetFailoverGroup(t *testing.T) {
	// Setup a test scheme
	scheme := runtime.NewScheme()
	err := crdv1alpha1.AddToScheme(scheme)
	require.NoError(t, err)

	// Create a failover group
	group := createTestFailoverGroup("test-group", "default", false, "Ok", "10s", "10s", "10s", nil)

	// Create a fake client
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(group).
		Build()

	// Create the manager
	manager := NewManager(fakeClient, "test-cluster", zap.New(zap.UseDevMode(true)))

	// Get the failover group
	ctx := context.Background()
	fetchedGroup, err := manager.GetFailoverGroup(ctx, "test-group", "default")
	require.NoError(t, err)
	assert.Equal(t, group.Name, fetchedGroup.Name)
	assert.Equal(t, group.Namespace, fetchedGroup.Namespace)

	// Try to get a non-existent group
	_, err = manager.GetFailoverGroup(ctx, "non-existent", "default")
	assert.Error(t, err)
}

// Helper function to create a test FailoverGroup
func createTestFailoverGroup(name, namespace string, suspended bool, health, transitory, unhealthy, heartbeat string, clusters []crdv1alpha1.ClusterInfo) *crdv1alpha1.FailoverGroup {
	return &crdv1alpha1.FailoverGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: crdv1alpha1.FailoverGroupSpec{
			FailoverMode: "safe",
			Suspended:    suspended,
			Timeouts: crdv1alpha1.TimeoutSettings{
				TransitoryState:  transitory,
				UnhealthyPrimary: unhealthy,
				Heartbeat:        heartbeat,
			},
		},
		Status: crdv1alpha1.FailoverGroupStatus{
			State:  "PRIMARY",
			Health: health,
			GlobalState: crdv1alpha1.GlobalStateInfo{
				ActiveCluster: "cluster1", // Default to cluster1 as active
				ThisCluster:   "test-cluster",
				Clusters:      clusters,
			},
		},
	}
}
