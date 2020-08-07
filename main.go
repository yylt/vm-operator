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
	"math/rand"
	"os"
	"time"

	mixappv1 "easystack.io/vm-operator/pkg/api/v1"
	"easystack.io/vm-operator/pkg/controllers"

	uberzap "go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	scheme                  = runtime.NewScheme()
	setupLog                = ctrl.Log.WithName("setup")
	metricsAddr, healthAddr string
	enableLeaderElection    bool
	nettpl, vmtpl, tmpdir   string
	podperiod, syncperiod   int64
	level, identify         string
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)

	_ = mixappv1.AddToScheme(scheme)
	// +kubebuilder:scaffold:scheme

}

func setlevel(st string) uberzap.AtomicLevel {
	var lvl zapcore.Level
	switch st {
	case "debug":
		lvl = zapcore.DebugLevel
	case "info":
		lvl = zapcore.InfoLevel
	case "warn":
		lvl = zapcore.WarnLevel
	case "error":
		lvl = zapcore.ErrorLevel
	default:
		lvl = zapcore.InfoLevel
	}
	return uberzap.NewAtomicLevelAt(lvl)
}

func main() {
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", true,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&level, "level", "info", "log level, debug, info, warn, error")
	flag.StringVar(&metricsAddr, "metrics-addr", "127.0.0.1:9448", "The address the metric endpoint binds to.")
	flag.StringVar(&healthAddr, "health-addr", "", "The address the health endpoint binds to.")
	flag.StringVar(&nettpl, "net-tpl", "/opt/network.tpl", "net tpl file path")
	flag.StringVar(&vmtpl, "vm-tpl", "/opt/vm.tpl", "vm tpl file path")
	flag.StringVar(&tmpdir, "tmp-dir", "/tmp", "tmp dir ,should can write ")
	flag.StringVar(&identify, "identify-addr", "http://keystone-api.openstack.svc.cluster.local/v3", "identify address.")

	flag.Int64Var(&syncperiod, "sync-period", 120, "sync time duration second")
	flag.Int64Var(&podperiod, "pod-period", 60, "sync time duration second")
	flag.Parse()

	rand.Seed(time.Now().UnixNano())

	lvl := setlevel(level)
	ctrl.SetLogger(zap.New(zap.UseDevMode(true), zap.Level(&lvl)))

	sync := time.Duration(int64(time.Second) * syncperiod)
	opt := ctrl.Options{
		SyncPeriod:             &sync,
		HealthProbeBindAddress: healthAddr,
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Port:                   9446,
		LeaderElection:         enableLeaderElection,
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), opt)
	if err != nil {
		setupLog.Error(err, "unable to create manager")
		os.Exit(1)
	}

	client, err := dynamic.NewForConfig(ctrl.GetConfigOrDie())
	if err != nil {
		setupLog.Error(err, "unable to create dynamic client")
		os.Exit(1)
	}

	synck8s := controllers.NewPodIp(time.Duration(int64(time.Second)*podperiod), ctrl.Log, client)
	oss := controllers.NewOSService(nettpl, vmtpl, tmpdir, identify, synck8s, ctrl.Log)

	vm := controllers.NewVirtualMachine(mgr.GetClient(), mgr.GetAPIReader(), ctrl.Log, oss)
	if err = vm.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to setup manager")
		os.Exit(1)
	}

	// +kubebuilder:scaffold:builder
	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
