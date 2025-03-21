#!/bin/bash
# setup-local-dynamodb.sh - Sets up a local DynamoDB instance for development/testing

set -e

CONTAINER_NAME="failover-operator-dynamodb"
PORT=8000
TABLE_NAME="failover-operator"
DATA_DIR="$HOME/.failover-operator/dynamodb-data"

# Parse command line arguments
while [[ $# -gt 0 ]]; do
  case $1 in
    --port)
      PORT="$2"
      shift 2
      ;;
    --container-name)
      CONTAINER_NAME="$2"
      shift 2
      ;;
    --table-name)
      TABLE_NAME="$2"
      shift 2
      ;;
    --data-dir)
      DATA_DIR="$2"
      shift 2
      ;;
    --help)
      echo "Usage: $0 [--port PORT] [--container-name NAME] [--table-name TABLE] [--data-dir DIR]"
      echo ""
      echo "Options:"
      echo "  --port PORT            Port to expose DynamoDB on (default: 8000)"
      echo "  --container-name NAME  Name for the Docker container (default: failover-operator-dynamodb)"
      echo "  --table-name TABLE     Name of the table to create (default: failover-operator)"
      echo "  --data-dir DIR         Directory to store DynamoDB data (default: ~/.failover-operator/dynamodb-data)"
      echo "  --help                 Display this help message"
      exit 0
      ;;
    *)
      echo "Unknown option: $1"
      echo "Use --help for usage information"
      exit 1
      ;;
  esac
done

# Ensure data directory exists
mkdir -p "$DATA_DIR"
echo "Using data directory: $DATA_DIR"

# Check if Docker is installed
if ! command -v docker &> /dev/null; then
    echo "Error: Docker is not installed. Please install Docker first."
    exit 1
fi

# Check if container is already running
if docker ps -a | grep -q "$CONTAINER_NAME"; then
    echo "Container $CONTAINER_NAME already exists."
    
    if docker ps | grep -q "$CONTAINER_NAME"; then
        echo "Container is running. Stopping and removing it..."
        docker stop "$CONTAINER_NAME"
    else
        echo "Container exists but is not running. Removing it..."
    fi
    
    docker rm "$CONTAINER_NAME"
    echo "Removed existing container."
fi

# Start DynamoDB local container
echo "Starting DynamoDB local container on port $PORT..."
docker run -d \
    --name "$CONTAINER_NAME" \
    -p "$PORT:8000" \
    -v "$DATA_DIR:/data" \
    amazon/dynamodb-local \
    -jar DynamoDBLocal.jar -sharedDb -dbPath /data

echo "Waiting for DynamoDB to start..."
sleep 3

# Check if container is running
if ! docker ps | grep -q "$CONTAINER_NAME"; then
    echo "Error: Container failed to start. Check docker logs for details."
    echo "Run: docker logs $CONTAINER_NAME"
    exit 1
fi

echo "DynamoDB local is running at http://localhost:$PORT"

# Create the table using the setup-dynamodb tool
echo "Creating table $TABLE_NAME..."

# Set environment variables for the client
export AWS_REGION=us-west-2
export AWS_ACCESS_KEY_ID=local
export AWS_SECRET_ACCESS_KEY=local
export AWS_ENDPOINT="http://localhost:$PORT"
export AWS_USE_LOCAL_ENDPOINT=true
export DYNAMODB_TABLE_NAME="$TABLE_NAME"

# Check if setup-dynamodb tool exists
if [ -f "./setup-dynamodb" ]; then
    ./setup-dynamodb --table-name="$TABLE_NAME" --local
elif [ -f "./bin/setup-dynamodb" ]; then
    ./bin/setup-dynamodb --table-name="$TABLE_NAME" --local
else
    echo "Building setup-dynamodb tool..."
    # Ensure the bin directory exists
    mkdir -p ./bin
    go build -o ./bin/setup-dynamodb cmd/setup-dynamodb/main.go
    ./bin/setup-dynamodb --table-name="$TABLE_NAME" --local
fi

echo ""
echo "DynamoDB local setup complete!"
echo ""
echo "To use with the operator, set these environment variables:"
echo ""
echo "export AWS_REGION=us-west-2"
echo "export AWS_ACCESS_KEY_ID=local"
echo "export AWS_SECRET_ACCESS_KEY=local"
echo "export AWS_ENDPOINT=http://localhost:$PORT"
echo "export AWS_USE_LOCAL_ENDPOINT=true"
echo "export DYNAMODB_TABLE_NAME=$TABLE_NAME"
echo ""
echo "To stop the container, run:"
echo "docker stop $CONTAINER_NAME"
echo ""
echo "To start it again, run:"
echo "docker start $CONTAINER_NAME"
echo "" 