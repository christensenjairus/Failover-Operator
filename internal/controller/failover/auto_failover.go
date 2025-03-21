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
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
)

// CheckAutomaticFailoverTriggers looks for failover groups that need automatic failover
// due to health issues or missing heartbeats
func (m *Manager) CheckAutomaticFailoverTriggers(ctx context.Context) error {
	// Get all FailoverGroups
	failoverGroups := &crdv1alpha1.FailoverGroupList{}
	if err := m.Client.List(ctx, failoverGroups); err != nil {
		return fmt.Errorf("failed to list failover groups: %w", err)
	}

	// Check each group for issues that would trigger automatic failover
	for _, group := range failoverGroups.Items {
		// Skip suspended groups
		if group.Spec.Suspended {
			continue
		}

		// Check if the current primary has issues
		if needsFailover, targetCluster, reason := m.checkGroupNeedsFailover(ctx, &group); needsFailover {
			// Create a failover resource to handle the failover
			if err := m.createAutomaticFailover(ctx, &group, targetCluster, reason); err != nil {
				m.Log.Error(err, "Failed to create automatic failover",
					"group", group.Name, "namespace", group.Namespace)
			}
		}
	}

	return nil
}

// checkGroupNeedsFailover determines if a failover group needs automatic failover
func (m *Manager) checkGroupNeedsFailover(ctx context.Context, group *crdv1alpha1.FailoverGroup) (bool, string, string) {
	// 1. Get the primary cluster
	primaryCluster, err := m.findPrimaryCluster(group)
	if err != nil {
		m.Log.Error(err, "Could not find primary cluster",
			"group", group.Name, "namespace", group.Namespace)
		return false, "", ""
	}

	// 2. Get a candidate standby cluster that can become primary
	targetCluster, err := m.findHealthyStandbyCluster(group)
	if err != nil {
		m.Log.Error(err, "Could not find healthy standby cluster",
			"group", group.Name, "namespace", group.Namespace)
		return false, "", ""
	}

	// 3. Check primary cluster health
	if primaryCluster.Health == "ERROR" {
		// Check if it's been unhealthy long enough to trigger a failover
		unhealthyTime, err := time.ParseDuration(group.Spec.Timeouts.UnhealthyPrimary)
		if err != nil {
			// Default to 5 minutes if invalid
			unhealthyTime = 5 * time.Minute
		}

		// Check if there's a status condition indicating when the health became ERROR
		if hasBeenUnhealthyLongEnough(group, unhealthyTime) {
			return true, targetCluster.Name, "Primary cluster health is in ERROR state"
		}
	}

	// 4. Check heartbeat timeout
	if m.hasHeartbeatTimeout(group, primaryCluster) {
		return true, targetCluster.Name, "Primary cluster heartbeat timeout"
	}

	return false, "", ""
}

// findPrimaryCluster finds the cluster marked as PRIMARY in the group
func (m *Manager) findPrimaryCluster(group *crdv1alpha1.FailoverGroup) (crdv1alpha1.ClusterInfo, error) {
	for _, cluster := range group.Status.GlobalState.Clusters {
		if cluster.Role == "PRIMARY" {
			return cluster, nil
		}
	}
	return crdv1alpha1.ClusterInfo{}, fmt.Errorf("no primary cluster found")
}

// findHealthyStandbyCluster finds a STANDBY cluster that can take over as PRIMARY
func (m *Manager) findHealthyStandbyCluster(group *crdv1alpha1.FailoverGroup) (crdv1alpha1.ClusterInfo, error) {
	for _, cluster := range group.Status.GlobalState.Clusters {
		if cluster.Role == "STANDBY" && (cluster.Health == "OK" || cluster.Health == "DEGRADED") {
			return cluster, nil
		}
	}
	return crdv1alpha1.ClusterInfo{}, fmt.Errorf("no healthy standby cluster found")
}

// hasBeenUnhealthyLongEnough checks if the primary has been unhealthy long enough
func hasBeenUnhealthyLongEnough(group *crdv1alpha1.FailoverGroup, timeout time.Duration) bool {
	// This would check conditions or status to determine how long the cluster
	// has been in an unhealthy state

	// For a real implementation, we'd check a timestamp condition
	// For now, return false as a placeholder
	return false
}

// hasHeartbeatTimeout checks if the primary cluster's heartbeat has timed out
func (m *Manager) hasHeartbeatTimeout(group *crdv1alpha1.FailoverGroup, primary crdv1alpha1.ClusterInfo) bool {
	if primary.LastHeartbeat == "" {
		return false
	}

	heartbeatTime, err := time.Parse(time.RFC3339, primary.LastHeartbeat)
	if err != nil {
		return false
	}

	heartbeatTimeout, err := time.ParseDuration(group.Spec.Timeouts.Heartbeat)
	if err != nil {
		// Default to 5 minutes if invalid
		heartbeatTimeout = 5 * time.Minute
	}

	return time.Since(heartbeatTime) > heartbeatTimeout
}

// createAutomaticFailover creates a Failover resource for automatic failover
func (m *Manager) createAutomaticFailover(ctx context.Context, group *crdv1alpha1.FailoverGroup, targetCluster, reason string) error {
	// Create a name for the automatic failover
	name := fmt.Sprintf("auto-failover-%s-%s", group.Name, time.Now().Format("20060102-150405"))

	// Create the Failover resource
	failover := &crdv1alpha1.Failover{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: group.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "failover-operator",
				"app.kubernetes.io/component":  "auto-failover",
			},
		},
		Spec: crdv1alpha1.FailoverSpec{
			TargetCluster: targetCluster,
			// For automatic failovers, we might want to use fast mode
			ForceFastMode: true,
			Reason:        fmt.Sprintf("Automatic failover: %s", reason),
			FailoverGroups: []crdv1alpha1.FailoverGroupReference{
				{
					Name:      group.Name,
					Namespace: group.Namespace,
				},
			},
		},
	}

	// Create the resource
	if err := m.Client.Create(ctx, failover); err != nil {
		return fmt.Errorf("failed to create automatic failover resource: %w", err)
	}

	m.Log.Info("Created automatic failover resource",
		"failover", name,
		"group", group.Name,
		"namespace", group.Namespace,
		"reason", reason)

	return nil
}

// GetFailoverGroup retrieves a FailoverGroup by name and namespace
func (m *Manager) GetFailoverGroup(ctx context.Context, name, namespace string) (*crdv1alpha1.FailoverGroup, error) {
	group := &crdv1alpha1.FailoverGroup{}
	if err := m.Client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, group); err != nil {
		return nil, err
	}
	return group, nil
}

// handleAutomaticFailoverError handles errors during automatic failover
// This documents how error handling works across the failover stages
/*
Error Handling (Any Stage)
--------------------------
When an error occurs during any stage of automatic failover:

1. The error is logged with detailed context and stack trace
2. The failover status is updated to FAILED with the error message
3. Cleanup actions are taken based on how far the failover progressed:
   - Stage 1 (Initialization): Release lock, update status
   - Stage 2 (Source Preparation): Restore network resources if possible
   - Stage 3 (Volume Demotion): No automatic rollback, requires manual intervention
   - Stage 4 (Volume Promotion): No automatic rollback, requires manual intervention
   - Stage 5 (Target Activation): Scale down target workloads if possible
   - Stage 6 (Completion): Release resources, ensure locks are freed

4. The error is recorded in the failover history
5. Appropriate alerts are triggered based on error severity
6. The controller will not retry automatically to avoid cascade failures
*/
