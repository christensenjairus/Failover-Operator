# DynamoDB Structure Reorganization

This document outlines the changes made to reorganize the DynamoDB structure according to the proposed update document.

## Key Changes Implemented

### 1. Record Type Consolidation

- Consolidated `OWNERSHIP` and `CONFIG` record types into a single `GROUP_CONFIG` record type
- Updated record type constants to use more descriptive names (`GROUP_CONFIG`, `CLUSTER_STATUS`, etc.)
- Flattened nested structures for more efficient queries

### 2. Manager Organization

- Replaced multiple specialized managers with two main managers:
  - `StateManager`: Handles all state-related operations (group config, cluster status, history)
  - `OperationsManager`: Handles complex operations (failover, locking, transactions)
  
- Added a top-level `DynamoDBService` that exposes both managers through a clean API

### 3. Record Structure 

- Added Global Secondary Index (GSI) support to improve query efficiency:
  - GSI1PK/SK for querying by operator ID
  - GSI1PK/SK for querying by cluster name
  
- Simplified component status storage by using JSON strings instead of nested maps

### 4. Transaction Support

- Added explicit transaction support in the DynamoDB client interface
- Implemented complex operations in `OperationsManager` using transactions for atomicity

## Structure Overview

```
DynamoDBService
  ├── State (StateManager)
  │     ├── GetGroupState
  │     ├── GetGroupConfig
  │     ├── UpdateGroupConfig
  │     ├── GetClusterStatus
  │     ├── UpdateClusterStatus
  │     ├── GetAllClusterStatuses
  │     ├── GetFailoverHistory
  │     └── DetectStaleHeartbeats
  │
  └── Operations (OperationsManager)
        ├── ExecuteFailover
        ├── ExecuteFailback
        ├── ValidateFailoverPreconditions
        ├── UpdateSuspension
        ├── AcquireLock
        ├── ReleaseLock
        ├── IsLocked
        └── DetectAndReportProblems
```

## Record Types and Key Structure

### GroupConfigRecord

- **PK**: "GROUP#{operatorID}#{namespace}#{name}"
- **SK**: "CONFIG"
- **GSI1PK**: "OPERATOR#{operatorID}"
- **GSI1SK**: "GROUP#{namespace}#{name}"

### ClusterStatusRecord

- **PK**: "GROUP#{operatorID}#{namespace}#{name}"
- **SK**: "CLUSTER#{clusterName}"
- **GSI1PK**: "CLUSTER#{clusterName}"
- **GSI1SK**: "GROUP#{namespace}#{name}"

### LockRecord

- **PK**: "GROUP#{operatorID}#{namespace}#{name}"
- **SK**: "LOCK"

### HistoryRecord

- **PK**: "GROUP#{operatorID}#{namespace}#{name}"
- **SK**: "HISTORY#{timestamp}"

## Usage Example

```go
// Initialize the DynamoDB service
dbService := dynamodb.NewDynamoDBService(client, tableName, clusterName, operatorID)

// Get the current state of a FailoverGroup
state, err := dbService.State.GetGroupState(ctx, namespace, name)

// Execute a failover operation
err = dbService.Operations.ExecuteFailover(ctx, namespace, name, failoverName, targetCluster, reason, false)

// Update cluster status
err = dbService.State.UpdateClusterStatus(ctx, namespace, name, health, state, components)
```

## Implementation Notes

The current implementation includes placeholder methods that don't perform actual DynamoDB operations. These need to be fully implemented with real DynamoDB calls in a production environment.

The reorganized structure provides a more maintainable and efficient approach to managing failover operations through DynamoDB, better aligning with the design patterns outlined in the update document. 