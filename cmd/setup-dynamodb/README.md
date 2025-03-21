# DynamoDB Setup Tool

This command-line tool creates and configures the DynamoDB table required for the Failover Operator.

## Usage

```bash
setup-dynamodb [flags]
```

### Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--table-name` | DynamoDB table name to create | `failover-operator` |
| `--region` | AWS region | `us-west-2` |
| `--endpoint` | Custom AWS endpoint (for local DynamoDB) | `""` |
| `--local` | Use local DynamoDB endpoint | `false` |
| `--access-key` | AWS access key ID | `""` |
| `--secret-key` | AWS secret access key | `""` |
| `--profile` | AWS profile to use (from ~/.aws/credentials) | `""` |
| `--pay-per-request` | Use pay-per-request billing mode | `true` |
| `--read-capacity` | Provisioned read capacity (only used with provisioned billing) | `5` |
| `--write-capacity` | Provisioned write capacity (only used with provisioned billing) | `5` |
| `--verbose` | Enable verbose logging | `false` |

## Environment Variables

The tool also respects the following environment variables:

- `AWS_REGION` - AWS region (if not specified via `--region`)
- `AWS_ACCESS_KEY_ID` - AWS access key ID (if not specified via `--access-key`)
- `AWS_SECRET_ACCESS_KEY` - AWS secret access key (if not specified via `--secret-key`)
- `AWS_PROFILE` - AWS profile to use (if not specified via `--profile`)
- `AWS_ENDPOINT` - Custom AWS endpoint (if not specified via `--endpoint`)

## Examples

### Creating a table in AWS DynamoDB

```bash
# Using default settings with AWS credentials from environment variables
setup-dynamodb

# Using a specific AWS profile
setup-dynamodb --profile=my-aws-profile

# Specifying AWS credentials directly
setup-dynamodb --access-key=YOUR_ACCESS_KEY --secret-key=YOUR_SECRET_KEY

# Using provisioned capacity instead of pay-per-request
setup-dynamodb --pay-per-request=false --read-capacity=10 --write-capacity=10

# Enabling verbose logging for troubleshooting
setup-dynamodb --profile=my-aws-profile --verbose
```

### Using Local DynamoDB for Development

```bash
# Start local DynamoDB (requires Docker)
docker run -d -p 8000:8000 amazon/dynamodb-local

# Create table in local DynamoDB
setup-dynamodb --local

# With custom endpoint
setup-dynamodb --endpoint=http://host.docker.internal:8000
```

## Table Schema

The tool creates a DynamoDB table with the following schema:

- **Primary Key**: `PK` (hash) and `SK` (range)
- **Global Secondary Index**: `GSI1` with `GSI1PK` (hash) and `GSI1SK` (range)

This schema supports the following data patterns:

1. **Group Configuration**: Stores ownership and settings for FailoverGroups
2. **Cluster Status**: Tracks the status of clusters in a FailoverGroup
3. **Locks**: Manages distributed locking during failover operations
4. **History**: Records failover operations history

## Building the Tool

```bash
go build -o setup-dynamodb cmd/setup-dynamodb/main.go
```

## Troubleshooting

If you encounter connection issues:

1. **AWS Credentials**: Ensure your credentials are valid and have the necessary permissions
2. **AWS Profile**: If using a profile, verify it exists in your `~/.aws/credentials` file
3. **Region**: Make sure you're using the correct region for your AWS account
4. **Endpoint**: For local DynamoDB, check that the Docker container is running and the port is accessible
5. **Network**: Ensure your network allows connections to AWS services (check proxies, firewalls)

Run with the `--verbose` flag for more detailed logging that can help diagnose issues. 