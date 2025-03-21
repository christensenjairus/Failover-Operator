/*
Copyright 2023 Jairus Christensen.

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
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/christensenjairus/Failover-Operator/internal/controller/dynamodb"
)

// AutoFailoverChecker monitors for failover requests and auto-creates
// Failover resources when needed
type AutoFailoverChecker struct {
	client      client.Client
	logger      logr.Logger
	clusterName string
	dynamoDB    *dynamodb.DynamoDBService
}

// NewAutoFailoverChecker creates a new automatic failover checker
func NewAutoFailoverChecker(
	client client.Client,
	logger logr.Logger,
	clusterName string,
	dynamoDB *dynamodb.DynamoDBService,
) *AutoFailoverChecker {
	return &AutoFailoverChecker{
		client:      client,
		logger:      logger.WithName("auto-failover-checker"),
		clusterName: clusterName,
		dynamoDB:    dynamoDB,
	}
}

// StartChecker starts the automatic failover checker loop
func (c *AutoFailoverChecker) StartChecker(ctx context.Context, intervalSeconds int) {
	c.logger.Info("Starting automatic failover checker",
		"checkIntervalSeconds", intervalSeconds,
		"clusterName", c.clusterName)

	go func() {
		ticker := time.NewTicker(time.Duration(intervalSeconds) * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				c.logger.Info("Stopping automatic failover checker")
				return
			case <-ticker.C:
				c.checkForFailoverRequests(ctx)
			}
		}
	}()
}

// checkForFailoverRequests checks DynamoDB for any pending failover requests
// and creates Failover resources for them if needed
func (c *AutoFailoverChecker) checkForFailoverRequests(ctx context.Context) {
	c.logger.V(1).Info("Checking for pending failover requests")

	// This would typically query DynamoDB for pending failover requests
	// and create Failover resources if needed.
	// Implementation depends on the DynamoDB schema and failover request format.

	// Example pseudo-implementation:
	// 1. Query DynamoDB for rows where TargetCluster = this.clusterName AND Status = "PENDING"
	// 2. For each request, check if we have already created a Failover for it
	// 3. If not, create a new Failover resource with the appropriate spec

	// For now, we'll leave this as a placeholder that simply logs
	// The actual implementation would depend on specific project requirements

	c.logger.V(1).Info("Auto-failover check completed")
}
