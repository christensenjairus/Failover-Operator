/*
Copyright 2025.

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

package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"github.com/joho/godotenv"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
	"github.com/christensenjairus/Failover-Operator/internal/config"
	"github.com/christensenjairus/Failover-Operator/internal/controller"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(crdv1alpha1.AddToScheme(scheme))
	// Register VolumeReplication scheme
	utilruntime.Must(controller.RegisterSchemes(scheme))
	// +kubebuilder:scaffold:scheme
}

// loadEnvFromFile loads environment variables from .env file
func loadEnvFromFile() {
	// Try to find .env file in different locations
	locations := []string{
		".env",                                   // Current directory
		filepath.Join("..", ".env"),              // One directory up
		filepath.Join(os.Getenv("HOME"), ".env"), // Home directory
	}

	for _, location := range locations {
		if _, err := os.Stat(location); err == nil {
			fmt.Printf("Loading environment from file: %s\n", location)
			err := godotenv.Load(location)
			if err != nil {
				fmt.Printf("Error loading .env file from %s: %v\n", location, err)
			} else {
				fmt.Printf("Successfully loaded environment from %s\n", location)
				return
			}
		}
	}

	// Create a .env file with default values if none exists
	if _, err := os.Stat(".env"); os.IsNotExist(err) {
		fmt.Println("No .env file found. Creating one with default values.")
		defaultEnv := `# AWS Configuration
AWS_REGION=us-west-2
AWS_ACCESS_KEY_ID=
AWS_SECRET_ACCESS_KEY=
AWS_SESSION_TOKEN=
AWS_ENDPOINT=
AWS_USE_LOCAL_ENDPOINT=false

# DynamoDB Configuration
DYNAMODB_TABLE_NAME=failover-operator

# Operator Configuration
CLUSTER_NAME=test-cluster
OPERATOR_ID=failover-operator

# Timeouts and intervals
RECONCILE_INTERVAL=30s
DEFAULT_HEARTBEAT_INTERVAL=30s
`
		err := os.WriteFile(".env", []byte(defaultEnv), 0644)
		if err != nil {
			fmt.Printf("Error creating default .env file: %v\n", err)
		} else {
			fmt.Println("Created default .env file. Please update it with your configuration.")
		}
	}
}

func main() {
	// Load environment variables from .env file
	loadEnvFromFile()

	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var clusterName string
	var operatorID string
	var dynamoDBTableName string
	var awsRegion string
	var awsEndpoint string
	var useLocalEndpoint bool
	var tlsOpts []func(*tls.Config)
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.StringVar(&clusterName, "cluster-name", "", "The name of this cluster for multi-cluster operations.")
	flag.StringVar(&operatorID, "operator-id", "", "The unique ID for this operator instance.")
	flag.StringVar(&dynamoDBTableName, "dynamodb-table-name", "", "The name of the DynamoDB table to use.")
	flag.StringVar(&awsRegion, "aws-region", "", "The AWS region where DynamoDB is located.")
	flag.StringVar(&awsEndpoint, "aws-endpoint", "", "Custom endpoint for AWS services (for local testing).")
	flag.BoolVar(&useLocalEndpoint, "use-local-endpoint", false, "Whether to use a local AWS endpoint.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: tlsOpts,
	})

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		// TODO(user): TLSOpts is used to allow configuring the TLS config used for the server. If certificates are
		// not provided, self-signed certificates will be generated by default. This option is not recommended for
		// production environments as self-signed certificates do not offer the same level of trust and security
		// as certificates issued by a trusted Certificate Authority (CA). The primary risk is potentially allowing
		// unauthorized access to sensitive metrics data. Consider replacing with CertDir, CertName, and KeyName
		// to provide certificates, ensuring the server communicates using trusted and secure certificates.
		TLSOpts: tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "fecc9312.hahomelabs.com",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Load configuration
	operatorConfig, err := config.LoadConfig()
	if err != nil {
		setupLog.Error(err, "unable to load operator configuration")
		os.Exit(1)
	}

	// Override config with command line arguments if provided
	if clusterName != "" {
		operatorConfig.ClusterName = clusterName
	}
	if operatorID != "" {
		operatorConfig.OperatorID = operatorID
	}
	if dynamoDBTableName != "" {
		operatorConfig.DynamoDBTableName = dynamoDBTableName
	}
	if awsRegion != "" {
		operatorConfig.AWSRegion = awsRegion
	}
	if awsEndpoint != "" {
		operatorConfig.AWSEndpoint = awsEndpoint
	}
	if useLocalEndpoint {
		operatorConfig.AWSUseLocalEndpoint = useLocalEndpoint
	}

	setupLog.Info("starting with configuration",
		"clusterName", operatorConfig.ClusterName,
		"operatorID", operatorConfig.OperatorID,
		"dynamoDBTable", operatorConfig.DynamoDBTableName,
		"awsRegion", operatorConfig.AWSRegion,
		"awsEndpoint", operatorConfig.AWSEndpoint,
		"useLocalEndpoint", operatorConfig.AWSUseLocalEndpoint)

	// Setup all controllers
	if err = controller.SetupControllers(mgr, operatorConfig); err != nil {
		setupLog.Error(err, "unable to setup controllers")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
