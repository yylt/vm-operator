/*
Copyright 2020 easystack.

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
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	mixappv1 "easystack.io/vm-operator/pkg/api/v1"
	"easystack.io/vm-operator/pkg/controllers"
	osservice "easystack.io/vm-operator/pkg/openstack"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)

	_ = mixappv1.AddToScheme(scheme)
	// +kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var configDir string
	var pollingPeriod int

	flag.BoolVar(&enableLeaderElection, "enable-leader-election", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&metricsAddr, "metrics-addr", "127.0.0.1:9446", "The address the metric endpoint binds to.")
	flag.StringVar(&configDir, "config-dir", "/etc/vm-operator", "Operator config dir")
	flag.IntVar(&pollingPeriod, "polling-period", 5, "Polling period of vm status.")
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	oss, err := osservice.NewOSService(configDir, ctrl.Log.WithName("VM"))
	if err != nil {
		setupLog.Error(err, "unable to init openstack service")
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:             scheme,
		MetricsBindAddress: metricsAddr,
		Port:               9446,
		LeaderElection:     enableLeaderElection,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	vm := controllers.NewVirtualMachine(mgr.GetClient(), mgr.GetAPIReader(), ctrl.Log.WithName("VM"), oss, pollingPeriod)
	// init vm cache from crd info
	err = vm.InitVmCacheFromCRD()
	if err != nil {
		setupLog.Error(err, "failed to init vm cache")
		os.Exit(1)
	}

	// periodically polling
	go vm.PollingVmInfo()

	if err = vm.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "VirtualMachine")
		os.Exit(1)
	}

	// +kubebuilder:scaffold:builder

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
