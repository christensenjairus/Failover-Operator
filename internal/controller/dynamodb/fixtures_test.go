package dynamodb

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
)

// Test constants
const (
	TestOperatorID  = "test-operator"
	TestClusterName = "test-cluster"
	TestTableName   = "test-table"
	TestNamespace   = "test-namespace"
	TestGroupName   = "test-group"
)

// Time constants for testing
var (
	TestTime       = time.Date(2024, 3, 15, 12, 0, 0, 0, time.UTC)
	TestTimePlus1m = TestTime.Add(1 * time.Minute)
	TestTimePlus5m = TestTime.Add(5 * time.Minute)
)

// DynamoDB Record Fixtures

// CreateTestGroupConfigRecord creates a test GroupConfigRecord
func CreateTestGroupConfigRecord() *GroupConfigRecord {
	return &GroupConfigRecord{
		PK:                "GROUP#test-operator#test-namespace#test-group",
		SK:                "CONFIG",
		GSI1PK:            "OPERATOR#test-operator",
		GSI1SK:            "GROUP#test-namespace#test-group",
		OperatorID:        TestOperatorID,
		GroupNamespace:    TestNamespace,
		GroupName:         TestGroupName,
		OwnerCluster:      TestClusterName,
		PreviousOwner:     "previous-cluster",
		Version:           1,
		LastUpdated:       TestTime,
		HeartbeatInterval: "30s",
		Timeouts: TimeoutSettings{
			TransitoryState:  "5m",
			UnhealthyPrimary: "2m",
			Heartbeat:        "1m",
		},
		Suspended: false,
		LastFailover: &FailoverReference{
			Name:      "test-failover",
			Namespace: TestNamespace,
			Timestamp: TestTime.Add(-24 * time.Hour),
		},
	}
}

// CreateTestClusterStatusRecord creates a test ClusterStatusRecord
func CreateTestClusterStatusRecord(clusterName, health, state string) *ClusterStatusRecord {
	componentStatuses := map[string]ComponentStatus{
		"web-app": {
			Health:  health,
			Message: "Web app is " + health,
		},
		"database": {
			Health:  health,
			Message: "Database is " + health,
		},
	}

	componentsJSON, _ := json.Marshal(componentStatuses)

	return &ClusterStatusRecord{
		PK:             "GROUP#test-operator#test-namespace#test-group",
		SK:             "CLUSTER#" + clusterName,
		GSI1PK:         "CLUSTER#" + clusterName,
		GSI1SK:         "GROUP#test-namespace#test-group",
		OperatorID:     TestOperatorID,
		GroupNamespace: TestNamespace,
		GroupName:      TestGroupName,
		ClusterName:    clusterName,
		Health:         health,
		State:          state,
		LastHeartbeat:  TestTime,
		Components:     string(componentsJSON),
	}
}

// CreateTestLockRecord creates a test LockRecord
func CreateTestLockRecord(lockedBy, reason string) *LockRecord {
	return &LockRecord{
		PK:             "GROUP#test-operator#test-namespace#test-group",
		SK:             "LOCK",
		OperatorID:     TestOperatorID,
		GroupNamespace: TestNamespace,
		GroupName:      TestGroupName,
		LockedBy:       lockedBy,
		LockReason:     reason,
		AcquiredAt:     TestTime,
		ExpiresAt:      TestTime.Add(10 * time.Minute),
		LeaseToken:     "test-lease-token",
	}
}

// CreateTestHistoryRecord creates a test HistoryRecord
func CreateTestHistoryRecord(sourceCluster, targetCluster, reason string) *HistoryRecord {
	return &HistoryRecord{
		PK:             "GROUP#test-operator#test-namespace#test-group",
		SK:             "HISTORY#" + TestTime.Format(time.RFC3339),
		OperatorID:     TestOperatorID,
		GroupNamespace: TestNamespace,
		GroupName:      TestGroupName,
		FailoverName:   "test-failover",
		SourceCluster:  sourceCluster,
		TargetCluster:  targetCluster,
		StartTime:      TestTime,
		EndTime:        TestTimePlus5m,
		Status:         "SUCCESS",
		Reason:         reason,
		Downtime:       60,
		Duration:       300,
	}
}

// DynamoDB Attribute Value Helpers

// ConvertToAttributeValue converts a record to DynamoDB attribute value map
func ConvertToAttributeValue(record interface{}) map[string]types.AttributeValue {
	// This is a simplified implementation
	// In a real implementation, you would use aws-sdk-go-v2 marshaling

	// Simple string-based implementation for testing
	jsonData, _ := json.Marshal(record)
	var jsonMap map[string]interface{}
	_ = json.Unmarshal(jsonData, &jsonMap)

	result := make(map[string]types.AttributeValue)
	for k, v := range jsonMap {
		switch val := v.(type) {
		case string:
			result[k] = &types.AttributeValueMemberS{Value: val}
		case int:
			result[k] = &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", val)}
		case bool:
			result[k] = &types.AttributeValueMemberBOOL{Value: val}
		}
	}

	return result
}

// Kubernetes Resource Fixtures

// CreateTestFailoverGroup creates a test FailoverGroup resource
func CreateTestFailoverGroup() *crdv1alpha1.FailoverGroup {
	return &crdv1alpha1.FailoverGroup{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "crd.hahomelabs.com/v1alpha1",
			Kind:       "FailoverGroup",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      TestGroupName,
			Namespace: TestNamespace,
		},
		Spec: crdv1alpha1.FailoverGroupSpec{
			OperatorID:          TestOperatorID,
			DefaultFailoverMode: "safe",
			Suspended:           false,
			HeartbeatInterval:   "30s",
			Timeouts: crdv1alpha1.TimeoutSettings{
				TransitoryState:  "5m",
				UnhealthyPrimary: "2m",
				Heartbeat:        "1m",
			},
			Components: []crdv1alpha1.ComponentSpec{
				{
					Name:         "web-app",
					FailoverMode: "fast",
					Workloads: []crdv1alpha1.ResourceRef{
						{
							Kind: "Deployment",
							Name: "web-app-deployment",
						},
					},
					VirtualServices: []string{"web-app-vs"},
					Ingresses:       []string{"web-app-ingress"},
				},
				{
					Name:         "database",
					FailoverMode: "safe",
					Workloads: []crdv1alpha1.ResourceRef{
						{
							Kind: "StatefulSet",
							Name: "database-statefulset",
						},
					},
					VolumeReplications: []string{"database-volume-replication"},
				},
			},
			ParentFluxResources: []crdv1alpha1.ResourceRef{
				{
					Kind: "Kustomization",
					Name: "app-stack",
				},
			},
		},
		Status: crdv1alpha1.FailoverGroupStatus{
			State:  string(crdv1alpha1.FailoverGroupStatePrimary),
			Health: "OK",
			Components: []crdv1alpha1.ComponentStatus{
				{
					Name:    "web-app",
					Health:  "OK",
					Message: "Web app is healthy",
				},
				{
					Name:    "database",
					Health:  "OK",
					Message: "Database is healthy",
				},
			},
			LastFailoverTime: TestTime.Format(time.RFC3339),
			GlobalState: crdv1alpha1.GlobalStateInfo{
				ActiveCluster: TestClusterName,
				ThisCluster:   TestClusterName,
				DBSyncStatus:  "Synced",
				LastSyncTime:  TestTime.Format(time.RFC3339),
				Clusters: []crdv1alpha1.ClusterInfo{
					{
						Name:          TestClusterName,
						Role:          "PRIMARY",
						Health:        "OK",
						LastHeartbeat: TestTime.Format(time.RFC3339),
					},
					{
						Name:          "standby-cluster",
						Role:          "STANDBY",
						Health:        "OK",
						LastHeartbeat: TestTime.Format(time.RFC3339),
					},
				},
			},
		},
	}
}

// CreateTestFailover creates a test Failover resource
func CreateTestFailover() *crdv1alpha1.Failover {
	return &crdv1alpha1.Failover{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "crd.hahomelabs.com/v1alpha1",
			Kind:       "Failover",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-failover",
			Namespace: TestNamespace,
		},
		Spec: crdv1alpha1.FailoverSpec{
			TargetCluster: "standby-cluster",
			ForceFastMode: false,
			Force:         false,
			Reason:        "Planned failover for testing",
			FailoverGroups: []crdv1alpha1.FailoverGroupReference{
				{
					Name:      TestGroupName,
					Namespace: TestNamespace,
				},
			},
		},
		Status: crdv1alpha1.FailoverStatus{
			Status: "IN_PROGRESS",
			FailoverGroups: []crdv1alpha1.FailoverGroupReference{
				{
					Name:      TestGroupName,
					Namespace: TestNamespace,
					Status:    "IN_PROGRESS",
					StartTime: TestTime.Format(time.RFC3339),
					Message:   "Failover in progress",
				},
			},
		},
	}
}

// CreateTestDeployment creates a test Deployment resource
func CreateTestDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-app-deployment",
			Namespace: TestNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: aws.Int32(3),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "web-app",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "web-app",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "web-app",
							Image: "nginx:latest",
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 80,
								},
							},
						},
					},
				},
			},
		},
		Status: appsv1.DeploymentStatus{
			AvailableReplicas: 3,
			ReadyReplicas:     3,
			Replicas:          3,
			UpdatedReplicas:   3,
		},
	}
}

// CreateTestStatefulSet creates a test StatefulSet resource
func CreateTestStatefulSet() *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "StatefulSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "database-statefulset",
			Namespace: TestNamespace,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: aws.Int32(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "database",
				},
			},
			ServiceName: "database",
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "database",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "database",
							Image: "postgres:latest",
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 5432,
								},
							},
						},
					},
				},
			},
		},
		Status: appsv1.StatefulSetStatus{
			AvailableReplicas: 1,
			ReadyReplicas:     1,
			Replicas:          1,
			UpdatedReplicas:   1,
		},
	}
}

// CreateTestCronJob creates a test CronJob resource
func CreateTestCronJob() *batchv1.CronJob {
	return &batchv1.CronJob{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "batch/v1",
			Kind:       "CronJob",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backup-cronjob",
			Namespace: TestNamespace,
		},
		Spec: batchv1.CronJobSpec{
			Schedule: "0 0 * * *",
			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "backup",
									Image: "backup:latest",
									Command: []string{
										"/bin/backup.sh",
									},
								},
							},
							RestartPolicy: corev1.RestartPolicyOnFailure,
						},
					},
				},
			},
			Suspend: aws.Bool(false),
		},
	}
}

// CreateTestIngress creates a test Ingress resource
func CreateTestIngress() *networkingv1.Ingress {
	pathType := networkingv1.PathTypePrefix

	return &networkingv1.Ingress{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "networking.k8s.io/v1",
			Kind:       "Ingress",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-app-ingress",
			Namespace: TestNamespace,
		},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{
				{
					Host: "webapp.example.com",
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/",
									PathType: &pathType,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: "web-app",
											Port: networkingv1.ServiceBackendPort{
												Number: 80,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// CreateTestService creates a test Service resource
func CreateTestService() *corev1.Service {
	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-app",
			Namespace: TestNamespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"app": "web-app",
			},
			Ports: []corev1.ServicePort{
				{
					Port:       80,
					TargetPort: intstr.FromInt(80),
				},
			},
			Type: corev1.ServiceTypeClusterIP,
		},
	}
}

// Test DynamoDB Client Setup

// CreateTestDynamoDBClient creates a test DynamoDB client with customized behavior
func CreateTestDynamoDBClient() *EnhancedTestDynamoDBClient {
	return &EnhancedTestDynamoDBClient{
		GetGroupConfigFn: func() *GroupConfigRecord {
			return CreateTestGroupConfigRecord()
		},
		GetClusterStatusFn: func() *ClusterStatusRecord {
			return CreateTestClusterStatusRecord(TestClusterName, HealthOK, StatePrimary)
		},
		StaleClustersReturnFn: func() []string {
			return []string{"stale-cluster"}
		},
		ProblemsReturnFn: func() []string {
			return []string{"Problem detected: unhealthy cluster"}
		},
	}
}
