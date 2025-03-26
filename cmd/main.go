/*
Copyright 2025 The Kubernetes Authors.

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
	"context"
	"crypto/tls"
	"flag"
	"os"
	"strings"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	cache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	// Import API types
	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"

	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	kubeconfigprovider "sigs.k8s.io/multicluster-runtime/providers/kubeconfig"

	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	// +kubebuilder:scaffold:imports
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	scheme = runtime.NewScheme()
)

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var masterURL string
	var tlsOpts []func(*tls.Config)

	var namespace string
	var kubeconfigLabel string
	var connectionTimeout time.Duration
	var cacheSyncTimeout time.Duration
	var providerReadyTimeout time.Duration

	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.StringVar(&masterURL, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig.")

	flag.StringVar(&namespace, "namespace", "failover-operator-system", "Namespace where kubeconfig secrets are stored")
	flag.StringVar(&kubeconfigLabel, "kubeconfig-label", "sigs.k8s.io/multicluster-runtime-kubeconfig",
		"Label used to identify secrets containing kubeconfig data")
	flag.DurationVar(&connectionTimeout, "connection-timeout", 15*time.Second,
		"Timeout for connecting to a cluster")
	flag.DurationVar(&cacheSyncTimeout, "cache-sync-timeout", 60*time.Second,
		"Timeout for waiting for the cache to sync")
	flag.DurationVar(&providerReadyTimeout, "provider-ready-timeout", 120*time.Second,
		"Timeout for waiting for the provider to be ready")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrllog.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	entryLog := ctrllog.Log.WithName("entrypoint")

	// Create config with explicitly provided kubeconfig/master
	// Note: controller-runtime already handles the --kubeconfig flag
	var config *rest.Config
	var err error

	// Get kubeconfig path from the flag that controller-runtime registers
	kubeconfigPath := os.Getenv("KUBECONFIG")
	for i := 1; i < len(os.Args); i++ {
		if strings.HasPrefix(os.Args[i], "--kubeconfig=") {
			kubeconfigPath = strings.TrimPrefix(os.Args[i], "--kubeconfig=")
			break
		}
		if os.Args[i] == "--kubeconfig" && i+1 < len(os.Args) {
			kubeconfigPath = os.Args[i+1]
			break
		}
	}

	if masterURL != "" {
		entryLog.Info("Using explicitly provided master URL", "master", masterURL)
		// Use explicit master URL with kubeconfig if provided
		if kubeconfigPath != "" {
			entryLog.Info("Using kubeconfig file with explicit master", "kubeconfig", kubeconfigPath)
			config, err = clientcmd.BuildConfigFromFlags(masterURL, kubeconfigPath)
		} else {
			// Just use the master URL
			config = &rest.Config{
				Host: masterURL,
			}
		}
	} else {
		// Use controller-runtime's standard config handling
		entryLog.Info("Using controller-runtime config handling")
		if kubeconfigPath != "" {
			entryLog.Info("Using kubeconfig file", "path", kubeconfigPath)
		}
		config, err = ctrl.GetConfig()
	}

	if err != nil {
		entryLog.Error(err, "unable to get kubernetes configuration")
		os.Exit(1)
	}

	entryLog.Info("Master K8s API:", "host", config.Host)

	entryLog.Info("Starting application", "namespace", namespace, "kubeconfigLabel", kubeconfigLabel)

	// Create the kubeconfig provider with options
	providerOpts := kubeconfigprovider.Options{
		Namespace:         namespace,
		KubeconfigLabel:   kubeconfigLabel,
		ConnectionTimeout: connectionTimeout,
		CacheSyncTimeout:  cacheSyncTimeout,
		KubeconfigPath:    kubeconfigPath,
	}

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		entryLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: tlsOpts,
	})

	// Metrics endpoint options
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// Create standard manager options
	mgmtOpts := manager.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "cb9167b4.hahomelabs.com",
	}

	// Create the provider first, then the manager with the provider
	entryLog.Info("Creating provider")
	provider := kubeconfigprovider.New(providerOpts)

	// Create the multicluster manager with the provider
	entryLog.Info("Creating manager")
	mcMgr, err := mcmanager.New(ctrl.GetConfigOrDie(), provider, mgmtOpts)
	if err != nil {
		entryLog.Error(err, "unable to create multicluster manager")
		os.Exit(1)
	}

	// Get the root context for the whole application
	ctx := ctrl.SetupSignalHandler()

	// Create a standard controller-runtime manager for controllers
	entryLog.Info("Creating controller-runtime manager")
	ctrlMgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Metrics: metricsserver.Options{
			BindAddress: ":8081", // Use a different port than the multicluster manager
		},
		// Increase cache sync timeout to ensure controllers have time to sync
		Cache: cache.Options{
			SyncPeriod: &cacheSyncTimeout,
		},
	})
	if err != nil {
		entryLog.Error(err, "Unable to create manager")
		os.Exit(1)
	}

	// Start provider in a goroutine
	entryLog.Info("Starting provider")
	go func() {
		err := provider.Run(ctx, mcMgr)
		if err != nil && ctx.Err() == nil {
			entryLog.Error(err, "Provider exited with error")
		}
	}()

	// Wait for the provider to be ready with a short timeout
	entryLog.Info("Waiting for provider to be ready")
	readyCtx, cancel := context.WithTimeout(ctx, providerReadyTimeout)
	defer cancel()

	// Wait for the provider to be ready before starting the manager
	select {
	case <-provider.IsReady():
		entryLog.Info("Provider is ready")
	case <-readyCtx.Done():
		entryLog.Error(readyCtx.Err(), "Timeout waiting for provider to be ready, continuing anyway")
	}

	// Register our custom resource types with the scheme
	entryLog.Info("Registering API types with the scheme")
	if err := crdv1alpha1.AddToScheme(ctrlMgr.GetScheme()); err != nil {
		entryLog.Error(err, "unable to register API types with scheme")
		os.Exit(1)
	}

	// Add our controllers
	entryLog.Info("Adding controllers")

	// // Create and add the failover controller
	// entryLog.Info("Setting up failover controller")
	// failoverManager := &failovers.Manager{
	// 	Client:              ctrlMgr.GetClient(),
	// 	Scheme:              ctrlMgr.GetScheme(),
	// 	MCReconciler:        provider,
	// 	DisableMultiCluster: false,
	// }
	// if err := failoverManager.SetupWithManager(ctrlMgr); err != nil {
	// 	entryLog.Error(err, "unable to set up failover controller")
	// 	os.Exit(1)
	// }

	// // Create and add the failovergroup controller
	// entryLog.Info("Setting up failovergroup controller")
	// failoverGroupManager := &failovergroups.Manager{
	// 	Client:              ctrlMgr.GetClient(),
	// 	Scheme:              ctrlMgr.GetScheme(),
	// 	MCReconciler:        provider,
	// 	DisableMultiCluster: false,
	// }
	// if err := failoverGroupManager.SetupWithManager(ctrlMgr); err != nil {
	// 	entryLog.Error(err, "unable to set up failovergroup controller")
	// 	os.Exit(1)
	// }

	// Setup healthz/readyz checks
	if err := ctrlMgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		entryLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := ctrlMgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		entryLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	// Start the manager
	entryLog.Info("Starting manager")
	if err := mcMgr.Start(ctx); err != nil {
		entryLog.Error(err, "Error running manager")
		os.Exit(1)
	}
}
