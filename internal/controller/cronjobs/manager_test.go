package cronjobs

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func setupTestManager() (*Manager, runtime.Object) {
	// Create a scheme
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = batchv1.AddToScheme(scheme)

	// Create a CronJob fixture
	cronjob := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cronjob",
			Namespace: "test-namespace",
		},
		Spec: batchv1.CronJobSpec{
			Schedule: "*/5 * * * *",
		},
	}

	// Create a fake client with the CronJob
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(cronjob).
		Build()

	return NewManager(client), cronjob
}

func TestNewManager(t *testing.T) {
	// Create a fake client
	client := fake.NewClientBuilder().Build()

	// Create the manager
	manager := NewManager(client)

	// Assert manager is not nil and client is set
	assert.NotNil(t, manager)
	assert.Equal(t, client, manager.client)
}

func TestScaleCronJob(t *testing.T) {
	// Setup
	manager, cronjob := setupTestManager()
	ctx := context.Background()

	// Call the function to suspend the cronjob
	err := manager.ScaleCronJob(ctx, "test-cronjob", "test-namespace", true)

	// Assert no error
	assert.NoError(t, err)

	// Get the updated cronjob
	updatedCronJob := &batchv1.CronJob{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: "test-cronjob", Namespace: "test-namespace"}, updatedCronJob)
	assert.NoError(t, err)

	// Assert the cronjob is now suspended
	assert.NotNil(t, updatedCronJob.Spec.Suspend)
	assert.True(t, *updatedCronJob.Spec.Suspend)
}

func TestSuspend(t *testing.T) {
	// Setup
	manager, _ := setupTestManager()
	ctx := context.Background()

	// Call the function
	err := manager.Suspend(ctx, "test-cronjob", "test-namespace")

	// Assert no error
	assert.NoError(t, err)

	// Get the updated cronjob
	updatedCronJob := &batchv1.CronJob{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: "test-cronjob", Namespace: "test-namespace"}, updatedCronJob)
	assert.NoError(t, err)

	// Assert the cronjob is suspended
	assert.NotNil(t, updatedCronJob.Spec.Suspend)
	assert.True(t, *updatedCronJob.Spec.Suspend)
}

func TestResume(t *testing.T) {
	// Setup
	manager, cronjob := setupTestManager()
	ctx := context.Background()

	// Pre-suspend the cronjob
	suspended := true
	cronjob.(*batchv1.CronJob).Spec.Suspend = &suspended
	err := manager.client.Update(ctx, cronjob.(*batchv1.CronJob))
	assert.NoError(t, err)

	// Call the function to resume
	err = manager.Resume(ctx, "test-cronjob", "test-namespace")

	// Assert no error
	assert.NoError(t, err)

	// Get the updated cronjob
	updatedCronJob := &batchv1.CronJob{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: "test-cronjob", Namespace: "test-namespace"}, updatedCronJob)
	assert.NoError(t, err)

	// Assert the cronjob is not suspended
	assert.NotNil(t, updatedCronJob.Spec.Suspend)
	assert.False(t, *updatedCronJob.Spec.Suspend)
}

func TestIsReady(t *testing.T) {
	// Setup
	manager, cronjob := setupTestManager()
	ctx := context.Background()

	// Test with unsuspended cronjob
	suspended := false
	cronjob.(*batchv1.CronJob).Spec.Suspend = &suspended
	err := manager.client.Update(ctx, cronjob.(*batchv1.CronJob))
	assert.NoError(t, err)

	// Call the function
	ready, err := manager.IsReady(ctx, "test-cronjob", "test-namespace")

	// Assert
	assert.NoError(t, err)
	assert.True(t, ready)

	// Test with suspended cronjob
	suspended = true
	cronjob.(*batchv1.CronJob).Spec.Suspend = &suspended
	err = manager.client.Update(ctx, cronjob.(*batchv1.CronJob))
	assert.NoError(t, err)

	// Call the function
	ready, err = manager.IsReady(ctx, "test-cronjob", "test-namespace")

	// Assert
	assert.NoError(t, err)
	assert.False(t, ready)
}

func TestIsSuspended(t *testing.T) {
	// Setup
	manager, cronjob := setupTestManager()
	ctx := context.Background()

	// Test with unsuspended cronjob
	suspended := false
	cronjob.(*batchv1.CronJob).Spec.Suspend = &suspended
	err := manager.client.Update(ctx, cronjob.(*batchv1.CronJob))
	assert.NoError(t, err)

	// Call the function
	isSuspended, err := manager.IsSuspended(ctx, "test-cronjob", "test-namespace")

	// Assert
	assert.NoError(t, err)
	assert.False(t, isSuspended)

	// Test with suspended cronjob
	suspended = true
	cronjob.(*batchv1.CronJob).Spec.Suspend = &suspended
	err = manager.client.Update(ctx, cronjob.(*batchv1.CronJob))
	assert.NoError(t, err)

	// Call the function
	isSuspended, err = manager.IsSuspended(ctx, "test-cronjob", "test-namespace")

	// Assert
	assert.NoError(t, err)
	assert.True(t, isSuspended)
}

func TestAddAnnotation(t *testing.T) {
	// Setup
	manager, _ := setupTestManager()
	ctx := context.Background()

	// Call the function
	err := manager.AddAnnotation(ctx, "test-cronjob", "test-namespace", "test-key", "test-value")

	// Assert
	assert.NoError(t, err)

	// Get the updated cronjob
	updatedCronJob := &batchv1.CronJob{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: "test-cronjob", Namespace: "test-namespace"}, updatedCronJob)
	assert.NoError(t, err)

	// Assert annotation was added
	assert.Equal(t, "test-value", updatedCronJob.Annotations["test-key"])
}

func TestRemoveAnnotation(t *testing.T) {
	// Setup
	manager, cronjob := setupTestManager()
	ctx := context.Background()

	// Add an annotation first
	if cronjob.(*batchv1.CronJob).Annotations == nil {
		cronjob.(*batchv1.CronJob).Annotations = make(map[string]string)
	}
	cronjob.(*batchv1.CronJob).Annotations["test-key"] = "test-value"
	err := manager.client.Update(ctx, cronjob.(*batchv1.CronJob))
	assert.NoError(t, err)

	// Call the function to remove annotation
	err = manager.RemoveAnnotation(ctx, "test-cronjob", "test-namespace", "test-key")

	// Assert
	assert.NoError(t, err)

	// Get the updated cronjob
	updatedCronJob := &batchv1.CronJob{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: "test-cronjob", Namespace: "test-namespace"}, updatedCronJob)
	assert.NoError(t, err)

	// Assert annotation was removed
	_, exists := updatedCronJob.Annotations["test-key"]
	assert.False(t, exists)
}

func TestGetAnnotation(t *testing.T) {
	// Setup
	manager, cronjob := setupTestManager()
	ctx := context.Background()

	// Add an annotation
	if cronjob.(*batchv1.CronJob).Annotations == nil {
		cronjob.(*batchv1.CronJob).Annotations = make(map[string]string)
	}
	cronjob.(*batchv1.CronJob).Annotations["test-key"] = "test-value"
	err := manager.client.Update(ctx, cronjob.(*batchv1.CronJob))
	assert.NoError(t, err)

	// Call the function
	value, exists, err := manager.GetAnnotation(ctx, "test-cronjob", "test-namespace", "test-key")

	// Assert
	assert.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, "test-value", value)

	// Test for non-existent annotation
	value, exists, err = manager.GetAnnotation(ctx, "test-cronjob", "test-namespace", "non-existent-key")

	// Assert
	assert.NoError(t, err)
	assert.False(t, exists)
	assert.Equal(t, "", value)
}

func TestAddFluxAnnotation(t *testing.T) {
	// Setup
	manager, _ := setupTestManager()
	ctx := context.Background()

	// Call the function
	err := manager.AddFluxAnnotation(ctx, "test-cronjob", "test-namespace")

	// Assert
	assert.NoError(t, err)

	// Get the updated cronjob
	updatedCronJob := &batchv1.CronJob{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: "test-cronjob", Namespace: "test-namespace"}, updatedCronJob)
	assert.NoError(t, err)

	// Assert flux annotation was added
	assert.Equal(t, DisabledValue, updatedCronJob.Annotations[FluxReconcileAnnotation])
}

func TestRemoveFluxAnnotation(t *testing.T) {
	// Setup
	manager, cronjob := setupTestManager()
	ctx := context.Background()

	// Add the flux annotation first
	if cronjob.(*batchv1.CronJob).Annotations == nil {
		cronjob.(*batchv1.CronJob).Annotations = make(map[string]string)
	}
	cronjob.(*batchv1.CronJob).Annotations[FluxReconcileAnnotation] = DisabledValue
	err := manager.client.Update(ctx, cronjob.(*batchv1.CronJob))
	assert.NoError(t, err)

	// Call the function
	err = manager.RemoveFluxAnnotation(ctx, "test-cronjob", "test-namespace")

	// Assert
	assert.NoError(t, err)

	// Get the updated cronjob
	updatedCronJob := &batchv1.CronJob{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: "test-cronjob", Namespace: "test-namespace"}, updatedCronJob)
	assert.NoError(t, err)

	// Assert flux annotation was removed
	_, exists := updatedCronJob.Annotations[FluxReconcileAnnotation]
	assert.False(t, exists)
}

// Note: WaitForSuspended and WaitForResumed are more complex to test due to polling behavior
// For proper testing, we would need to mock the wait.PollImmediate function or use a test clock
// These tests would need to be more sophisticated than the ones here
