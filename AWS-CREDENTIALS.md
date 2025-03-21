# AWS Credentials Setup for Failover Operator

This document explains how to set up AWS credentials for the Failover Operator to access DynamoDB for state management.

## Prerequisites

- AWS account with permissions to access DynamoDB
- AWS access key and secret key
- Kubernetes cluster where the operator is deployed

## Configuration Options

The Failover Operator supports multiple ways to configure AWS credentials:

### 1. Environment Variables

The operator can read AWS credentials from environment variables set in the deployment manifest:

- `AWS_REGION` - AWS region where DynamoDB is deployed
- `AWS_ACCESS_KEY_ID` - AWS access key ID
- `AWS_SECRET_ACCESS_KEY` - AWS secret access key
- `AWS_SESSION_TOKEN` - (Optional) AWS session token for temporary credentials

Example in deployment manifest:

```yaml
containers:
  - name: manager
    env:
      - name: AWS_REGION
        value: "us-west-2"
      - name: AWS_ACCESS_KEY_ID
        valueFrom:
          secretKeyRef:
            name: aws-credentials
            key: access_key
      - name: AWS_SECRET_ACCESS_KEY
        valueFrom:
          secretKeyRef:
            name: aws-credentials
            key: secret_key
```

### 2. Kubernetes Secret

The operator can load AWS credentials from a Kubernetes Secret. To use this method:

1. Create a secret containing AWS credentials:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: aws-credentials
  namespace: system
type: Opaque
data:
  access_key: <base64-encoded-access-key>
  secret_key: <base64-encoded-secret-key>
  region: <base64-encoded-region>  # Optional
  session_token: <base64-encoded-session-token>  # Optional
```

2. Configure the operator to use the secret by setting command line arguments:

```yaml
containers:
  - name: manager
    args:
      - --aws-secret-name=aws-credentials
      - --aws-secret-namespace=system
```

### 3. AWS Instance Profiles / IRSA

For production deployments, it is recommended to use IAM roles:

- For EKS: Use [IAM Roles for Service Accounts (IRSA)](https://docs.aws.amazon.com/eks/latest/userguide/iam-roles-for-service-accounts.html)
- For EC2: Use [Instance Profiles](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_roles_use_switch-role-ec2_instance-profiles.html)

With these methods, you don't need to explicitly provide AWS credentials. The AWS SDK will automatically fetch credentials from the instance metadata service.

## Required IAM Permissions

The IAM role or user requires the following permissions for DynamoDB:

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
        "dynamodb:TransactWriteItems"
      ],
      "Resource": "arn:aws:dynamodb:<region>:<account-id>:table/failover-operator"
    }
  ]
}
```

## Local Development

For local development and testing, you can:

1. Set up a local DynamoDB instance:

```yaml
env:
  - name: AWS_ENDPOINT
    value: "http://localhost:8000"
  - name: AWS_USE_LOCAL_ENDPOINT
    value: "true"
  - name: AWS_REGION
    value: "us-west-2"
  - name: AWS_ACCESS_KEY_ID
    value: "dummy"
  - name: AWS_SECRET_ACCESS_KEY
    value: "dummy"
```

2. Use your AWS profile from `~/.aws/credentials`

## Troubleshooting

- Check the operator logs for AWS-related error messages
- Verify that the correct credentials are being used
- Ensure the IAM policy has the necessary permissions
- For credential loading issues, set the log level to debug for more detailed information 