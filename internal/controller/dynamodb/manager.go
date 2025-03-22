package dynamodb

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// DynamoDBClient defines the interface for interacting with DynamoDB
type DynamoDBClient interface {
	GetItem(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	UpdateItem(ctx context.Context, params *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	DeleteItem(ctx context.Context, params *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
	Query(ctx context.Context, params *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	Scan(ctx context.Context, params *dynamodb.ScanInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
	TransactWriteItems(ctx context.Context, params *dynamodb.TransactWriteItemsInput, optFns ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
	BatchGetItem(ctx context.Context, params *dynamodb.BatchGetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.BatchGetItemOutput, error)
	BatchWriteItem(ctx context.Context, params *dynamodb.BatchWriteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error)
}

// BaseManager provides core functionality for interacting with DynamoDB
type BaseManager struct {
	client      DynamoDBClient
	tableName   string
	clusterName string
	operatorID  string
}

// ManagerGroupState represents the state of a failover group
type ManagerGroupState struct {
	GroupID                   string     `json:"groupId"`
	Status                    string     `json:"status"`
	CurrentRole               string     `json:"currentRole"`
	FailoverCount             int        `json:"failoverCount"`
	LastFailover              *time.Time `json:"lastFailover,omitempty"`
	LastHeartbeat             *time.Time `json:"lastHeartbeat,omitempty"`
	FailoverReason            string     `json:"failoverReason,omitempty"`
	LastUpdate                int64      `json:"lastUpdate"`
	VolumeState               string     `json:"volumeState,omitempty"`               // Current state of volumes during failover
	LastVolumeStateUpdateTime string     `json:"lastVolumeStateUpdateTime,omitempty"` // When volume state was last updated
}

// DynamoDBService provides access to all DynamoDB functionality
// This is the main entry point for controllers interacting with DynamoDB
type DynamoDBService struct {
	*BaseManager
	ClusterName        string
	OperatorID         string
	TableName          string
	volumeStateManager *VolumeStateManager
	operationsManager  *OperationsManager

	// Cache for frequently accessed data
	cache        *ServiceCache
	cacheEnabled bool
	cacheTTL     time.Duration
	state        *StateManager
}

// ServiceCache provides in-memory caching for DynamoDB service
type ServiceCache struct {
	// GroupConfig cache
	groupConfigs map[string]*CacheEntry

	// ClusterStatus cache
	clusterStatuses map[string]*CacheEntry

	// GroupState cache
	groupStates map[string]*CacheEntry

	// Cache mutex
	mu sync.RWMutex
}

// CacheEntry represents a cached item with expiration
type CacheEntry struct {
	Value      interface{}
	Expiration time.Time
}

// NewDynamoDBService creates a new DynamoDB service with optional caching
func NewDynamoDBService(client DynamoDBClient, tableName, clusterName, operatorID string) *DynamoDBService {
	// Use default values if empty strings are provided
	if tableName == "" {
		tableName = "FailoverOperator"
	}

	if operatorID == "" {
		operatorID = "default-operator"
	}

	// Create a valid clusterName if one is not provided
	if clusterName == "" {
		hostname, err := os.Hostname()
		if err == nil && hostname != "" {
			clusterName = hostname
		} else {
			clusterName = "unknown-cluster"
		}
	}

	baseManager := &BaseManager{
		client:      client,
		tableName:   tableName,
		clusterName: clusterName,
		operatorID:  operatorID,
	}
	stateManager := NewStateManager(baseManager)
	operationsManager := NewOperationsManager(baseManager)
	volumeStateManager := NewVolumeStateManager(baseManager)

	// Create cache with default 30-second TTL
	return NewDynamoDBServiceWithCache(baseManager, stateManager, operationsManager, volumeStateManager, true, 30*time.Second)
}

// NewDynamoDBServiceWithCache creates a new DynamoDB service with configurable caching
func NewDynamoDBServiceWithCache(
	baseManager *BaseManager,
	stateManager *StateManager,
	operationsManager *OperationsManager,
	volumeStateManager *VolumeStateManager,
	enableCache bool,
	cacheTTL time.Duration,
) *DynamoDBService {
	cache := &ServiceCache{
		groupConfigs:    make(map[string]*CacheEntry),
		clusterStatuses: make(map[string]*CacheEntry),
		groupStates:     make(map[string]*CacheEntry),
	}

	service := &DynamoDBService{
		BaseManager:        baseManager,
		ClusterName:        baseManager.clusterName,
		OperatorID:         baseManager.operatorID,
		TableName:          baseManager.tableName,
		volumeStateManager: volumeStateManager,
		operationsManager:  operationsManager,
		cache:              cache,
		cacheEnabled:       enableCache,
		cacheTTL:           cacheTTL,
		state:              stateManager,
	}

	// Start background cache cleanup if caching is enabled
	if enableCache {
		go service.startCacheCleanup(context.Background())
	}

	return service
}

// startCacheCleanup periodically removes expired cache entries
func (s *DynamoDBService) startCacheCleanup(ctx context.Context) {
	// Run cleanup every quarter of the TTL duration or at least every 10 seconds
	cleanupInterval := s.cacheTTL / 4
	if cleanupInterval < 10*time.Second {
		cleanupInterval = 10 * time.Second
	}

	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Context canceled, exit the goroutine
			return
		case <-ticker.C:
			// Time to clean up expired entries
			s.cleanupExpiredEntries()
		}
	}
}

// cleanupExpiredEntries removes all expired entries from the cache
func (s *DynamoDBService) cleanupExpiredEntries() {
	now := time.Now()

	// Lock the cache for writing
	s.cache.mu.Lock()
	defer s.cache.mu.Unlock()

	// Clean up group configs
	for key, entry := range s.cache.groupConfigs {
		if entry.Expiration.Before(now) {
			delete(s.cache.groupConfigs, key)
		}
	}

	// Clean up cluster statuses
	for key, entry := range s.cache.clusterStatuses {
		if entry.Expiration.Before(now) {
			delete(s.cache.clusterStatuses, key)
		}
	}

	// Clean up group states
	for key, entry := range s.cache.groupStates {
		if entry.Expiration.Before(now) {
			delete(s.cache.groupStates, key)
		}
	}
}

// GroupConfigKey generates a cache key for group config
func groupConfigKey(namespace, name string) string {
	return fmt.Sprintf("config:%s:%s", namespace, name)
}

// ClusterStatusKey generates a cache key for cluster status
func clusterStatusKey(namespace, name, clusterName string) string {
	return fmt.Sprintf("status:%s:%s:%s", namespace, name, clusterName)
}

// GroupStateKey generates a cache key for group state
func groupStateKey(namespace, name string) string {
	return fmt.Sprintf("state:%s:%s", namespace, name)
}

// GetGroupConfig gets the configuration for a FailoverGroup with caching
func (s *DynamoDBService) GetGroupConfig(ctx context.Context, namespace, name string) (*GroupConfigRecord, error) {
	if !s.cacheEnabled {
		return s.BaseManager.GetGroupConfig(ctx, namespace, name)
	}

	// Check cache first
	cacheKey := groupConfigKey(namespace, name)
	s.cache.mu.RLock()
	entry, found := s.cache.groupConfigs[cacheKey]
	s.cache.mu.RUnlock()

	now := time.Now()
	if found && entry.Expiration.After(now) {
		// Cache hit
		if config, ok := entry.Value.(*GroupConfigRecord); ok {
			return config, nil
		}
	}

	// Cache miss or expired, get from DynamoDB
	config, err := s.BaseManager.GetGroupConfig(ctx, namespace, name)
	if err != nil {
		return nil, err
	}

	// Update cache
	s.cache.mu.Lock()
	s.cache.groupConfigs[cacheKey] = &CacheEntry{
		Value:      config,
		Expiration: now.Add(s.cacheTTL),
	}
	s.cache.mu.Unlock()

	return config, nil
}

// UpdateGroupConfig updates the configuration for a FailoverGroup and invalidates cache
func (s *DynamoDBService) UpdateGroupConfig(ctx context.Context, config *GroupConfigRecord) error {
	err := s.BaseManager.UpdateGroupConfig(ctx, config)
	if err != nil {
		return err
	}

	if s.cacheEnabled {
		// Invalidate cache
		cacheKey := groupConfigKey(config.GroupNamespace, config.GroupName)
		s.cache.mu.Lock()
		delete(s.cache.groupConfigs, cacheKey)
		s.cache.mu.Unlock()
	}

	return nil
}

// GetClusterStatus gets the status of a cluster with caching
func (s *DynamoDBService) GetClusterStatus(ctx context.Context, namespace, name, clusterName string) (*ClusterStatusRecord, error) {
	if !s.cacheEnabled {
		return s.BaseManager.GetClusterStatus(ctx, namespace, name, clusterName)
	}

	// Check cache first
	cacheKey := clusterStatusKey(namespace, name, clusterName)
	s.cache.mu.RLock()
	entry, found := s.cache.clusterStatuses[cacheKey]
	s.cache.mu.RUnlock()

	now := time.Now()
	if found && entry.Expiration.After(now) {
		// Cache hit
		if status, ok := entry.Value.(*ClusterStatusRecord); ok {
			return status, nil
		}
	}

	// Cache miss or expired, get from DynamoDB
	status, err := s.BaseManager.GetClusterStatus(ctx, namespace, name, clusterName)
	if err != nil {
		return nil, err
	}

	// Update cache only if we got a valid status
	if status != nil {
		s.cache.mu.Lock()
		s.cache.clusterStatuses[cacheKey] = &CacheEntry{
			Value:      status,
			Expiration: now.Add(s.cacheTTL),
		}
		s.cache.mu.Unlock()
	}

	return status, nil
}

// UpdateClusterStatus updates the status of a cluster and invalidates cache
func (s *DynamoDBService) UpdateClusterStatus(ctx context.Context, namespace, name, clusterName, health, state string, componentsJSON string) error {
	err := s.BaseManager.UpdateClusterStatus(ctx, namespace, name, clusterName, health, state, componentsJSON)
	if err != nil {
		return err
	}

	if s.cacheEnabled {
		// Invalidate cache
		cacheKey := clusterStatusKey(namespace, name, clusterName)
		s.cache.mu.Lock()
		delete(s.cache.clusterStatuses, cacheKey)
		s.cache.mu.Unlock()

		// Also invalidate group state cache since cluster status affects group state
		stateKey := groupStateKey(namespace, name)
		s.cache.mu.Lock()
		delete(s.cache.groupStates, stateKey)
		s.cache.mu.Unlock()
	}

	return nil
}

// GetGroupState gets the current state of a FailoverGroup with caching
func (s *DynamoDBService) GetGroupState(ctx context.Context, namespace, name string) (*ManagerGroupState, error) {
	if !s.cacheEnabled {
		return s.BaseManager.GetGroupState(ctx, namespace, name)
	}

	// Check cache first
	cacheKey := groupStateKey(namespace, name)
	s.cache.mu.RLock()
	entry, found := s.cache.groupStates[cacheKey]
	s.cache.mu.RUnlock()

	now := time.Now()
	if found && entry.Expiration.After(now) {
		// Cache hit
		if state, ok := entry.Value.(*ManagerGroupState); ok {
			return state, nil
		}
	}

	// Cache miss or expired, get from DynamoDB
	state, err := s.BaseManager.GetGroupState(ctx, namespace, name)
	if err != nil {
		return nil, err
	}

	// Update cache
	s.cache.mu.Lock()
	s.cache.groupStates[cacheKey] = &CacheEntry{
		Value:      state,
		Expiration: now.Add(s.cacheTTL),
	}
	s.cache.mu.Unlock()

	return state, nil
}

// ClearCache clears all cached data
func (s *DynamoDBService) ClearCache() {
	if !s.cacheEnabled {
		return
	}

	s.cache.mu.Lock()
	defer s.cache.mu.Unlock()

	s.cache.groupConfigs = make(map[string]*CacheEntry)
	s.cache.clusterStatuses = make(map[string]*CacheEntry)
	s.cache.groupStates = make(map[string]*CacheEntry)
}

// State returns the state manager
func (s *DynamoDBService) State() *StateManager {
	return s.state
}

// Operations returns the operations manager
func (s *DynamoDBService) Operations() *OperationsManager {
	return s.operationsManager
}

// VolumeState returns the volume state manager
func (s *DynamoDBService) VolumeState() *VolumeStateManager {
	return s.volumeStateManager
}

// getGroupPK creates a primary key for a FailoverGroup
func (m *BaseManager) getGroupPK(namespace, name string) string {
	return fmt.Sprintf("GROUP#%s#%s#%s", m.operatorID, namespace, name)
}

// getClusterSK creates a sort key for a cluster status record
func (m *BaseManager) getClusterSK(clusterName string) string {
	return fmt.Sprintf("CLUSTER#%s", clusterName)
}

// getHistorySK creates a sort key for a history record
func (m *BaseManager) getHistorySK(timestamp time.Time) string {
	return fmt.Sprintf("HISTORY#%s", timestamp.Format(time.RFC3339))
}

// getGSI1PK creates a GSI1 primary key for an operator
func (m *BaseManager) getOperatorGSI1PK() string {
	return fmt.Sprintf("OPERATOR#%s", m.operatorID)
}

// getGSI1SK creates a GSI1 sort key for a group
func (m *BaseManager) getGroupGSI1SK(namespace, name string) string {
	return fmt.Sprintf("GROUP#%s#%s", namespace, name)
}

// getClusterGSI1PK creates a GSI1 primary key for a cluster
func (m *BaseManager) getClusterGSI1PK(clusterName string) string {
	return fmt.Sprintf("CLUSTER#%s", clusterName)
}

// Key Workflow Scenarios

// 1. Normal Operation Workflow:
// - Each cluster periodically calls UpdateClusterStatus to report its health
// - Each cluster periodically calls GetCurrentGroupConfig to update its view of the global state
// - If a cluster's state changes, it calls UpdateClusterStatus to report the change

// 2. Planned Failover Workflow:
// - Cluster initiating failover calls AcquireLock to obtain exclusive access
// - Initiator verifies preconditions (replication status, component health, etc.)
// - If preconditions met, calls ExecuteFailover to change ownership
// - All clusters detect the change during their next GetCurrentGroupConfig
// - PRIMARY cluster transitions to STANDBY, STANDBY cluster transitions to PRIMARY
// - Lock is automatically released as part of ExecuteFailover transaction

// 3. Emergency Failover Workflow:
// - Similar to planned failover but skips some precondition checks
// - Uses forceFastMode to prioritize availability over perfect consistency
// - May be triggered automatically by DetectStaleHeartbeats or health monitoring

// 4. Automatic Failover Workflow:
// - Controller periodically calls DetectStaleHeartbeats
// - If PRIMARY cluster's heartbeat is stale, controller creates a Failover resource
// - Failover controller processes the resource using the emergency workflow
// - All clusters adapt to the new ownership during regular synchronization

// 5. Suspension Workflow:
// - Administrator calls UpdateSuspension to disable automatic failovers
// - Controllers check IsSuspended before creating automatic Failover resources
// - Manual Failover resources can still override suspension if force=true

// Operations methods

// ExecuteFailover executes a failover from the current primary to a new primary cluster
func (s *DynamoDBService) ExecuteFailover(ctx context.Context, namespace, name, failoverName, targetCluster, reason string, forceFastMode bool) error {
	logger := log.FromContext(ctx)

	if s.operationsManager == nil {
		logger.Error(nil, "operationsManager is nil, cannot execute failover")
		return fmt.Errorf("operationsManager is nil, cannot execute failover")
	}

	return s.operationsManager.ExecuteFailover(ctx, namespace, name, failoverName, targetCluster, reason, forceFastMode)
}

// ExecuteFailback executes a failback to the original primary cluster
func (s *DynamoDBService) ExecuteFailback(ctx context.Context, namespace, name, reason string) error {
	logger := log.FromContext(ctx)

	if s.operationsManager == nil {
		logger.Error(nil, "operationsManager is nil, cannot execute failback")
		return fmt.Errorf("operationsManager is nil, cannot execute failback")
	}

	return s.operationsManager.ExecuteFailback(ctx, namespace, name, reason)
}

// ValidateFailoverPreconditions validates that preconditions for a failover are met
func (s *DynamoDBService) ValidateFailoverPreconditions(ctx context.Context, namespace, name, targetCluster string, skipHealthCheck bool) error {
	logger := log.FromContext(ctx)

	if s.operationsManager == nil {
		logger.Error(nil, "operationsManager is nil, cannot validate failover preconditions")
		return fmt.Errorf("operationsManager is nil, cannot validate failover preconditions")
	}

	return s.operationsManager.ValidateFailoverPreconditions(ctx, namespace, name, targetCluster, skipHealthCheck)
}

// UpdateSuspension updates the suspension status of a FailoverGroup
func (s *DynamoDBService) UpdateSuspension(ctx context.Context, namespace, name string, suspended bool, reason string) error {
	logger := log.FromContext(ctx)

	if s.operationsManager == nil {
		logger.Error(nil, "operationsManager is nil, cannot update suspension")
		return fmt.Errorf("operationsManager is nil, cannot update suspension")
	}

	return s.operationsManager.UpdateSuspension(ctx, namespace, name, suspended, reason)
}

// AcquireLock attempts to acquire a lock for a FailoverGroup
func (s *DynamoDBService) AcquireLock(ctx context.Context, namespace, name, reason string) (string, error) {
	logger := log.FromContext(ctx)

	if s.operationsManager == nil {
		logger.Error(nil, "operationsManager is nil, cannot acquire lock")
		return "", fmt.Errorf("operationsManager is nil, cannot acquire lock")
	}

	return s.operationsManager.AcquireLock(ctx, namespace, name, reason)
}

// ReleaseLock releases a previously acquired lock for a FailoverGroup
func (s *DynamoDBService) ReleaseLock(ctx context.Context, namespace, name, leaseToken string) error {
	logger := log.FromContext(ctx)

	if s.operationsManager == nil {
		logger.Error(nil, "operationsManager is nil, cannot release lock")
		return fmt.Errorf("operationsManager is nil, cannot release lock")
	}

	return s.operationsManager.ReleaseLock(ctx, namespace, name, leaseToken)
}

// IsLocked checks if a FailoverGroup is currently locked
func (s *DynamoDBService) IsLocked(ctx context.Context, namespace, name string) (bool, string, error) {
	logger := log.FromContext(ctx)

	if s.operationsManager == nil {
		logger.Error(nil, "operationsManager is nil, cannot check lock status")
		return false, "", fmt.Errorf("operationsManager is nil, cannot check lock status")
	}

	return s.operationsManager.IsLocked(ctx, namespace, name)
}

// DetectAndReportProblems checks for problems and reports them
func (s *DynamoDBService) DetectAndReportProblems(ctx context.Context, namespace, name string) ([]string, error) {
	logger := log.FromContext(ctx)

	if s.operationsManager == nil {
		logger.Error(nil, "operationsManager is nil, cannot detect problems")
		return nil, fmt.Errorf("operationsManager is nil, cannot detect problems")
	}

	return s.operationsManager.DetectAndReportProblems(ctx, namespace, name)
}

// VolumeState methods

// GetVolumeState retrieves the current volume state for a failover group
func (s *DynamoDBService) GetVolumeState(ctx context.Context, namespace, groupName string) (string, error) {
	logger := log.FromContext(ctx)

	if s.volumeStateManager == nil {
		logger.Error(nil, "volumeStateManager is nil, cannot get volume state")
		return "", fmt.Errorf("volumeStateManager is nil, cannot get volume state")
	}

	return s.volumeStateManager.GetVolumeState(ctx, namespace, groupName)
}

// SetVolumeState updates the volume state for a failover group
func (s *DynamoDBService) SetVolumeState(ctx context.Context, namespace, groupName, state string) error {
	logger := log.FromContext(ctx)

	if s.volumeStateManager == nil {
		logger.Error(nil, "volumeStateManager is nil, cannot set volume state")
		return fmt.Errorf("volumeStateManager is nil, cannot set volume state")
	}

	return s.volumeStateManager.SetVolumeState(ctx, namespace, groupName, state)
}

// UpdateHeartbeat updates the heartbeat timestamp for a cluster
func (s *DynamoDBService) UpdateHeartbeat(ctx context.Context, namespace, groupName, clusterName string) error {
	logger := log.FromContext(ctx)

	if s.volumeStateManager == nil {
		logger.Error(nil, "volumeStateManager is nil, cannot update heartbeat")
		return fmt.Errorf("volumeStateManager is nil, cannot update heartbeat")
	}

	return s.volumeStateManager.UpdateHeartbeat(ctx, namespace, groupName, clusterName)
}

// RemoveVolumeState removes the volume state information
func (s *DynamoDBService) RemoveVolumeState(ctx context.Context, namespace, groupName string) error {
	logger := log.FromContext(ctx)

	if s.volumeStateManager == nil {
		logger.Error(nil, "volumeStateManager is nil, cannot remove volume state")
		return fmt.Errorf("volumeStateManager is nil, cannot remove volume state")
	}

	return s.volumeStateManager.RemoveVolumeState(ctx, namespace, groupName)
}

// StateManager methods that aren't in BaseManager

// SyncClusterState synchronizes the cluster state with DynamoDB
func (s *DynamoDBService) SyncClusterState(ctx context.Context, namespace, name string) error {
	// This is a stub implementation that would typically synchronize the local cluster state
	// with the state stored in DynamoDB
	logger := log.FromContext(ctx).WithValues(
		"namespace", namespace,
		"name", name,
	)
	logger.V(1).Info("Synchronizing cluster state with DynamoDB")
	return nil
}

// DetectStaleHeartbeats detects clusters with stale heartbeats
func (s *DynamoDBService) DetectStaleHeartbeats(ctx context.Context, namespace, name string) ([]string, error) {
	// This is a stub implementation that would typically check for stale heartbeats
	// and return a list of clusters that have not updated their heartbeat recently
	logger := log.FromContext(ctx).WithValues(
		"namespace", namespace,
		"name", name,
	)
	logger.V(1).Info("Detecting stale heartbeats")
	return []string{}, nil
}

// GetAllClusterStatuses gets the status of all clusters in the failover group
func (s *DynamoDBService) GetAllClusterStatuses(ctx context.Context, namespace, name string) (map[string]*ClusterStatusRecord, error) {
	// Delegate to the BaseManager's implementation to properly query for all cluster statuses
	return s.BaseManager.GetAllClusterStatuses(ctx, namespace, name)
}
