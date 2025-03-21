# DynamoDB Integration for Failover Operator

The Failover Operator uses Amazon DynamoDB for cross-cluster state coordination. This document explains how to set up and configure DynamoDB for the operator.

## Overview

DynamoDB is used by the Failover Operator to:

1. Track the current state of FailoverGroups across multiple clusters
2. Provide distributed locking for safe failover operations
3. Maintain a history of failover events for auditing
4. Track cluster health via heartbeats
5. Coordinate volume replication status during failovers

## Setting Up DynamoDB

### Option 1: Automatic Setup During Operator Startup

The simplest method is to let the operator automatically create and configure the DynamoDB table at startup:

1. Configure the operator with AWS credentials that have permissions to create and manage DynamoDB tables
2. Set the `DYNAMODB_TABLE_NAME` configuration option in your operator config
3. The operator will automatically create the table if it doesn't exist

### Option 2: Manual Setup Using Command-Line Tool

For more control over the table creation, or to pre-create the table before deploying the operator:

1. Build the setup tool:
   ```bash
   go build -o setup-dynamodb cmd/setup-dynamodb/main.go
   ```

2. Run the setup tool:
   ```bash
   # Using default settings with AWS credentials from environment variables
   ./setup-dynamodb
   
   # Specifying a custom table name
   ./setup-dynamodb --table-name=my-failover-table
   
   # Using provisioned capacity instead of pay-per-request
   ./setup-dynamodb --pay-per-request=false --read-capacity=10 --write-capacity=10
   ```

   See [Setup Tool Documentation](cmd/setup-dynamodb/README.md) for more options.

### Option 3: Manual Setup in AWS Console

If you prefer to create the table manually in the AWS Console:

1. Log into the AWS Console and navigate to DynamoDB
2. Click "Create table"
3. Enter table name (e.g., `failover-operator`)
4. Set Primary key to `PK` (String)
5. Check "Add sort key" and set to `SK` (String)
6. Choose either "On-demand" or "Provisioned" capacity
7. Click "Create"

8. After creating the table, add a Global Secondary Index:
   - Index name: `GSI1`
   - Partition key: `GSI1PK` (String)
   - Sort key: `GSI1SK` (String)
   - Projected attributes: All

## DynamoDB Table Schema

The Failover Operator uses a single-table design with a flexible schema:

### Primary Key Structure

- **PK (Partition Key)**: `GROUP#{operatorID}#{namespace}#{name}`
- **SK (Sort Key)**: Varies by record type

### Global Secondary Index (GSI1)

- **GSI1PK**: Varies by query pattern
- **GSI1SK**: Varies by query pattern

### Record Types

The table stores four main types of records:

1. **Group Configuration Records**: Store ownership and settings for a FailoverGroup
   - SK = `CONFIG`
   - Contains ownership information, timeouts, suspension status

2. **Cluster Status Records**: Track the status of a cluster for a FailoverGroup
   - SK = `CLUSTER#{clusterName}`
   - Contains health status, heartbeat timestamps

3. **Lock Records**: Manage distributed locking during failover operations
   - SK = `LOCK`
   - Contains lock holder, expiry time, reason

4. **History Records**: Store a history of failover operations
   - SK = `HISTORY#{timestamp}`
   - Contains details about each failover operation

## AWS IAM Permissions

The Failover Operator requires certain IAM permissions to interact with DynamoDB:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "dynamodb:GetItem",
        "dynamodb:PutItem",
        "dynamodb:UpdateItem",
        "dynamodb:DeleteItem",
        "dynamodb:Query",
        "dynamodb:Scan",
        "dynamodb:TransactWriteItems",
        "dynamodb:CreateTable",
        "dynamodb:DescribeTable"
      ],
      "Resource": [
        "arn:aws:dynamodb:<region>:<account-id>:table/<table-name>",
        "arn:aws:dynamodb:<region>:<account-id>:table/<table-name>/index/*"
      ]
    }
  ]
}
```

For production deployments, you should restrict permissions as needed. The `CreateTable` and `DescribeTable` permissions are only needed if you want the operator to automatically create the table.

## Configuration Options

The following configuration options control DynamoDB behavior:

| Config Option | Description | Default Value |
|--------------|-------------|---------------|
| `AWS_REGION` | AWS region where DynamoDB is located | `us-west-2` |
| `AWS_ENDPOINT` | Custom endpoint for DynamoDB (for local testing) | `""` |
| `AWS_USE_LOCAL_ENDPOINT` | Use a local DynamoDB endpoint | `false` |
| `DYNAMODB_TABLE_NAME` | Name of the DynamoDB table | `failover-operator` |
| `OPERATOR_ID` | Unique identifier for this operator instance | `failover-operator` |

## Local Development and Testing

For local development and testing, you can use [DynamoDB Local](https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/DynamoDBLocal.html):

1. Start DynamoDB Local with Docker:
   ```bash
   docker run -d -p 8000:8000 amazon/dynamodb-local
   ```

2. Configure the operator to use the local endpoint:
   ```yaml
   AWS_ENDPOINT: "http://localhost:8000"
   AWS_USE_LOCAL_ENDPOINT: "true"
   AWS_REGION: "us-west-2"
   AWS_ACCESS_KEY_ID: "dummy"
   AWS_SECRET_ACCESS_KEY: "dummy"
   ```

## Key Operations

The DynamoDB integration supports several key operations:

1. **Global State Coordination**: Maintains a consistent view of FailoverGroup state across clusters
2. **Distributed Locking**: Prevents multiple clusters from executing conflicting operations
3. **Health Monitoring**: Tracks cluster health through regular heartbeats
4. **Automatic Failover Detection**: Identifies stale heartbeats that might indicate a cluster failure
5. **Volume State Tracking**: Maintains the state of volume replications during failover operations

## Troubleshooting

Common issues with DynamoDB integration:

1. **Table Not Found**: Ensure the table exists or the operator has permissions to create it
2. **Permission Denied**: Verify IAM permissions for the DynamoDB table
3. **Cross-Region Access**: If accessing DynamoDB across regions, check networking and IAM policies
4. **Throttling**: Watch for capacity errors if using provisioned capacity
5. **Connectivity Issues**: Ensure network connectivity between the Kubernetes cluster and AWS

Check the operator logs for detailed error messages related to DynamoDB operations. 