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
	_ "net/http/pprof"
	"os"
	"time"

	mixappv1 "easystack.io/vm-operator/pkg/api/v1"
	"easystack.io/vm-operator/pkg/controllers"
	"easystack.io/vm-operator/pkg/manage"
	"easystack.io/vm-operator/pkg/template"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	klog "k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
)

var (
	scheme = runtime.NewScheme()

	enableLeaderElection          bool
	nettpl, vmtpl, fiptpl, tmpdir string
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)

	_ = mixappv1.AddToScheme(scheme)

	rand.Seed(time.Now().UnixNano())
}

func main() {
	var (
		leaderid = "vm.controller"
	)

	flag.BoolVar(&enableLeaderElection, "enable-leader-election", false,
		"Enabling this will ensure only one active controller manager.")

	flag.StringVar(&nettpl, "net-tpl", "/opt/network.tpl", "net tpl file path")
	flag.StringVar(&vmtpl, "vm-tpl", "/opt/vm.tpl", "vm tpl file path")
	flag.StringVar(&fiptpl, "fip-tpl", "/opt/fip.tpl", "floatip tpl file path")
	flag.StringVar(&tmpdir, "tmp-dir", "/tmp", "must have write permission on this dir")

	optime := flag.Duration("openstack-sync-period", time.Second*30, "sync time which openstack fetch resource")
	k8time := flag.Duration("k8s-sync-period", time.Second*30, "sync time which k8s sync external service")
	syncdu := flag.Duration("sync-period", time.Second*35, "controller manager sync resource time duration")

	klog.InitFlags(nil)

	flag.Parse()

	config := ctrl.GetConfigOrDie()

	opt := ctrl.Options{
		SyncPeriod:         syncdu,
		Scheme:             scheme,
		MetricsBindAddress: "0",
		LeaderElection:     enableLeaderElection,
		LeaderElectionID:   leaderid,
	}

	mgr, err := ctrl.NewManager(config, opt)
	if err != nil {
		klog.Errorf("create runtime manager failed:%v", err)
		os.Exit(1)
	}

	client, err := dynamic.NewForConfig(ctrl.GetConfigOrDie())
	if err != nil {
		klog.Errorf("create dynamic client failed:%v ", err)
		os.Exit(1)
	}
	k8smgr := manage.NewK8sMgr(client)

	tempengine := template.NewTemplate()
	tempengine.AddTempFileMust(template.Fip, fiptpl)
	tempengine.AddTempFileMust(template.Lb, nettpl)
	tempengine.AddTempFileMust(template.Vm, vmtpl)

	server := controllers.NewServer(tempengine, tmpdir, k8smgr, enableLeaderElection, *k8time, *optime)

	controllers.NewVirtualMachine(mgr, server)

	klog.Infof("manager start")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		klog.Errorf("start manager failed:%v", err)
		os.Exit(1)
	}
}
