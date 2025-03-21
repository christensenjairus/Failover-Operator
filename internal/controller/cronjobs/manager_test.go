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

// setupTestManager creates a test manager with a fake client and test CronJob
func setupTestManager() (*Manager, *batchv1.CronJob) {
	// Create a scheme
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = batchv1.AddToScheme(scheme)

	// Create a test CronJob
	suspend := false
	cronjob := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cronjob",
			Namespace: "default",
		},
		Spec: batchv1.CronJobSpec{
			Schedule: "*/5 * * * *",
			Suspend:  &suspend,
		},
	}

	// Create a fake client with the CronJob
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cronjob).
		Build()

	return NewManager(client), cronjob
}

// TestNewManager tests the creation of a new CronJob manager
func TestNewManager(t *testing.T) {
	// Create a fake client
	scheme := runtime.NewScheme()
	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	// Create the manager
	manager := NewManager(client)

	// Assert manager is not nil and client is set
	assert.NotNil(t, manager)
	assert.Equal(t, client, manager.client)
}

// TestScaleCronJob tests scaling (suspending/unsuspending) a CronJob
func TestScaleCronJob(t *testing.T) {
	// Setup
	manager, cronjob := setupTestManager()
	ctx := context.Background()

	// Verify initial state
	assert.False(t, *cronjob.Spec.Suspend)

	// Call the function to suspend the cronjob
	err := manager.ScaleCronJob(ctx, cronjob.Name, cronjob.Namespace, true)
	assert.NoError(t, err)

	// Get the updated CronJob
	updatedCronJob := &batchv1.CronJob{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: cronjob.Name, Namespace: cronjob.Namespace}, updatedCronJob)
	assert.NoError(t, err)

	// Assert the CronJob is suspended
	assert.True(t, *updatedCronJob.Spec.Suspend)

	// Call the function to unsuspend the cronjob
	err = manager.ScaleCronJob(ctx, cronjob.Name, cronjob.Namespace, false)
	assert.NoError(t, err)

	// Get the updated CronJob again
	err = manager.client.Get(ctx, types.NamespacedName{Name: cronjob.Name, Namespace: cronjob.Namespace}, updatedCronJob)
	assert.NoError(t, err)

	// Assert the CronJob is unsuspended
	assert.False(t, *updatedCronJob.Spec.Suspend)
}

// TestSuspend tests suspending a CronJob
func TestSuspend(t *testing.T) {
	// Setup
	manager, cronjob := setupTestManager()
	ctx := context.Background()

	// Verify initial state
	assert.False(t, *cronjob.Spec.Suspend)

	// Call the function to suspend the cronjob
	err := manager.Suspend(ctx, cronjob.Name, cronjob.Namespace)
	assert.NoError(t, err)

	// Get the updated CronJob
	updatedCronJob := &batchv1.CronJob{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: cronjob.Name, Namespace: cronjob.Namespace}, updatedCronJob)
	assert.NoError(t, err)

	// Assert the CronJob is suspended
	assert.True(t, *updatedCronJob.Spec.Suspend)
}

// TestResume tests resuming a CronJob
func TestResume(t *testing.T) {
	// Setup
	manager, cronjob := setupTestManager()
	ctx := context.Background()

	// First suspend the cronjob
	suspend := true
	cronjob.Spec.Suspend = &suspend
	err := manager.client.Update(ctx, cronjob)
	assert.NoError(t, err)

	// Verify it's suspended
	updatedCronJob := &batchv1.CronJob{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: cronjob.Name, Namespace: cronjob.Namespace}, updatedCronJob)
	assert.NoError(t, err)
	assert.True(t, *updatedCronJob.Spec.Suspend)

	// Call the function to resume the cronjob
	err = manager.Resume(ctx, cronjob.Name, cronjob.Namespace)
	assert.NoError(t, err)

	// Get the updated CronJob again
	err = manager.client.Get(ctx, types.NamespacedName{Name: cronjob.Name, Namespace: cronjob.Namespace}, updatedCronJob)
	assert.NoError(t, err)

	// Assert the CronJob is resumed
	assert.False(t, *updatedCronJob.Spec.Suspend)
}

// TestIsReady tests checking if a CronJob is ready (not suspended)
func TestIsReady(t *testing.T) {
	// Setup
	manager, cronjob := setupTestManager()
	ctx := context.Background()

	// Test with unsuspended CronJob (should be ready)
	ready, err := manager.IsReady(ctx, cronjob.Name, cronjob.Namespace)
	assert.NoError(t, err)
	assert.True(t, ready)

	// Update CronJob to be suspended
	suspend := true
	cronjob.Spec.Suspend = &suspend
	err = manager.client.Update(ctx, cronjob)
	assert.NoError(t, err)

	// Test with suspended CronJob (should not be ready)
	ready, err = manager.IsReady(ctx, cronjob.Name, cronjob.Namespace)
	assert.NoError(t, err)
	assert.False(t, ready)
}

// TestIsSuspended tests checking if a CronJob is suspended
func TestIsSuspended(t *testing.T) {
	// Setup
	manager, cronjob := setupTestManager()
	ctx := context.Background()

	// Test with unsuspended CronJob (should not be suspended)
	suspended, err := manager.IsSuspended(ctx, cronjob.Name, cronjob.Namespace)
	assert.NoError(t, err)
	assert.False(t, suspended)

	// Update CronJob to be suspended
	suspend := true
	cronjob.Spec.Suspend = &suspend
	err = manager.client.Update(ctx, cronjob)
	assert.NoError(t, err)

	// Test with suspended CronJob (should be suspended)
	suspended, err = manager.IsSuspended(ctx, cronjob.Name, cronjob.Namespace)
	assert.NoError(t, err)
	assert.True(t, suspended)
}

// TestAddAnnotation tests adding an annotation to a CronJob
func TestAddAnnotation(t *testing.T) {
	// Setup
	manager, cronjob := setupTestManager()
	ctx := context.Background()

	// Call the function to add an annotation
	err := manager.AddAnnotation(ctx, cronjob.Name, cronjob.Namespace, "test-key", "test-value")
	assert.NoError(t, err)

	// Get the updated CronJob
	updatedCronJob := &batchv1.CronJob{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: cronjob.Name, Namespace: cronjob.Namespace}, updatedCronJob)
	assert.NoError(t, err)

	// Assert the annotation was added
	assert.Equal(t, "test-value", updatedCronJob.Annotations["test-key"])
}

// TestRemoveAnnotation tests removing an annotation from a CronJob
func TestRemoveAnnotation(t *testing.T) {
	// Setup
	manager, cronjob := setupTestManager()
	ctx := context.Background()

	// Add an annotation first
	if cronjob.Annotations == nil {
		cronjob.Annotations = make(map[string]string)
	}
	cronjob.Annotations["test-key"] = "test-value"
	err := manager.client.Update(ctx, cronjob)
	assert.NoError(t, err)

	// Verify the annotation exists
	updatedCronJob := &batchv1.CronJob{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: cronjob.Name, Namespace: cronjob.Namespace}, updatedCronJob)
	assert.NoError(t, err)
	assert.Equal(t, "test-value", updatedCronJob.Annotations["test-key"])

	// Call the function to remove the annotation
	err = manager.RemoveAnnotation(ctx, cronjob.Name, cronjob.Namespace, "test-key")
	assert.NoError(t, err)

	// Get the updated CronJob again
	err = manager.client.Get(ctx, types.NamespacedName{Name: cronjob.Name, Namespace: cronjob.Namespace}, updatedCronJob)
	assert.NoError(t, err)

	// Assert the annotation was removed
	_, exists := updatedCronJob.Annotations["test-key"]
	assert.False(t, exists)
}

// TestGetAnnotation tests getting an annotation from a CronJob
func TestGetAnnotation(t *testing.T) {
	// Setup
	manager, cronjob := setupTestManager()
	ctx := context.Background()

	// Add an annotation
	if cronjob.Annotations == nil {
		cronjob.Annotations = make(map[string]string)
	}
	cronjob.Annotations["test-key"] = "test-value"
	err := manager.client.Update(ctx, cronjob)
	assert.NoError(t, err)

	// Call the function to get the annotation
	value, exists, err := manager.GetAnnotation(ctx, cronjob.Name, cronjob.Namespace, "test-key")
	assert.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, "test-value", value)

	// Test getting a non-existent annotation
	value, exists, err = manager.GetAnnotation(ctx, cronjob.Name, cronjob.Namespace, "non-existent")
	assert.NoError(t, err)
	assert.False(t, exists)
	assert.Equal(t, "", value)
}

// TestAddFluxAnnotation tests adding the Flux reconcile annotation
func TestAddFluxAnnotation(t *testing.T) {
	// Setup
	manager, cronjob := setupTestManager()
	ctx := context.Background()

	// Call the function to add flux annotation
	err := manager.AddFluxAnnotation(ctx, cronjob.Name, cronjob.Namespace)
	assert.NoError(t, err)

	// Get the updated CronJob
	updatedCronJob := &batchv1.CronJob{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: cronjob.Name, Namespace: cronjob.Namespace}, updatedCronJob)
	assert.NoError(t, err)

	// Assert flux annotation was added
	assert.Equal(t, DisabledValue, updatedCronJob.Annotations[FluxReconcileAnnotation])
}

// TestRemoveFluxAnnotation tests removing the Flux reconcile annotation
func TestRemoveFluxAnnotation(t *testing.T) {
	// Setup
	manager, cronjob := setupTestManager()
	ctx := context.Background()

	// Add flux annotation first
	if cronjob.Annotations == nil {
		cronjob.Annotations = make(map[string]string)
	}
	cronjob.Annotations[FluxReconcileAnnotation] = DisabledValue
	err := manager.client.Update(ctx, cronjob)
	assert.NoError(t, err)

	// Verify the annotation exists
	updatedCronJob := &batchv1.CronJob{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: cronjob.Name, Namespace: cronjob.Namespace}, updatedCronJob)
	assert.NoError(t, err)
	assert.Equal(t, DisabledValue, updatedCronJob.Annotations[FluxReconcileAnnotation])

	// Call the function to remove flux annotation
	err = manager.RemoveFluxAnnotation(ctx, cronjob.Name, cronjob.Namespace)
	assert.NoError(t, err)

	// Get the updated CronJob again
	err = manager.client.Get(ctx, types.NamespacedName{Name: cronjob.Name, Namespace: cronjob.Namespace}, updatedCronJob)
	assert.NoError(t, err)

	// Assert flux annotation was removed
	_, exists := updatedCronJob.Annotations[FluxReconcileAnnotation]
	assert.False(t, exists)
}
