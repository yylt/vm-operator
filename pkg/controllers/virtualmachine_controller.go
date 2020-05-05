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

package controllers

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	vmv1 "easystack.io/vm-operator/pkg/api/v1"
	"easystack.io/vm-operator/pkg/openstack"
	vmtpl "easystack.io/vm-operator/pkg/templates"
	"easystack.io/vm-operator/pkg/utils"
	"github.com/go-logr/logr"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	cli "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/gophercloud/gophercloud/openstack/orchestration/v1/stacks"
	"reflect"
)

const (
	loggerCtxKey = "logger"
	tplOutBase   = "/tmp/vm-templates"
)

// VirtualMachineReconciler reconciles a VirtualMachine object
type VirtualMachineReconciler struct {
	client        cli.Client
	cliReader     cli.Reader
	log           logr.Logger
	scheme        *runtime.Scheme
	osService     *openstack.OSService
	vmCache       *vmCache
	PollingPeriod int
}

type vmCache struct {
	mu sync.Mutex
	// using stackname as key
	vmMap map[string]vmv1.VirtualMachine
}

func NewVirtualMachine(c cli.Client, r cli.Reader, logger logr.Logger, oss *openstack.OSService, period int) *VirtualMachineReconciler {
	return &VirtualMachineReconciler{
		client:        c,
		cliReader:     r,
		log:           logger,
		osService:     oss,
		vmCache:       &vmCache{vmMap: make(map[string]vmv1.VirtualMachine)},
		PollingPeriod: period,
	}
}

// +kubebuilder:rbac:groups=mixapp.easystack.io,resources=virtualmachines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mixapp.easystack.io,resources=virtualmachines/status,verbs=get;update;patch

func (r *VirtualMachineReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	rootCtx := context.Background()
	logger := r.log.WithValues("Reconcile", req.NamespacedName)
	ctx := context.WithValue(rootCtx, loggerCtxKey, logger)

	var vm vmv1.VirtualMachine
	err := r.cliReader.Get(ctx, req.NamespacedName, &vm)
	if err != nil {
		if apierrs.IsNotFound(err) {
			// Delete event
			logger.Info("Delete Event", "vm crd has been deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// vm is in the process of being deleted, so no need to do anything.
	if vm.DeletionTimestamp != nil {
		logger.Info("Delete Event", "vm crd is in the process of being deleted")
		return ctrl.Result{}, nil
	}

	cached, ok := r.vmCache.get(vm.Name)
	if !ok {
		// Add event
		logger.Info("Add Event", "CRD Spec", vm.Spec)
		createOpts, err := r.buildStackCreateOpts(ctx, req.Name, &vm)
		if err != nil {
			return ctrl.Result{}, nil
		}
		r.osService.StackCreate(ctx, createOpts)
		r.vmCache.set(vm.Name, vm)
	} else {
		// TODO: Update event
		fmt.Printf("update event: %v\n", cached)
	}

	return ctrl.Result{}, nil
}

func (r *VirtualMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&vmv1.VirtualMachine{}).
		Complete(r)
}

func (r *VirtualMachineReconciler) InitVmCacheFromCRD() error {
	rootCtx := context.Background()
	logger := r.log.WithName("InitCRD")
	ctx := context.WithValue(rootCtx, loggerCtxKey, logger)

	var vmList vmv1.VirtualMachineList

	err := r.cliReader.List(ctx, &vmList)
	if err != nil {
		logger.Error(err, "Failed to list vm crd")
		return err
	}

	r.vmCache.mu.Lock()
	defer r.vmCache.mu.Unlock()
	for i := range vmList.Items {
		projectID := vmList.Items[i].Spec.Project.ProjectID
		name := vmList.Items[i].Spec.Server.NamePrefix
		key := strings.Join([]string{projectID, name}, "-")
		r.vmCache.vmMap[key] = vmList.Items[i]
	}

	return nil
}

func (r *VirtualMachineReconciler) PollingVmInfo() error {
	rootCtx := context.Background()
	logger := r.log.WithName("Polling")
	ctx := context.WithValue(rootCtx, loggerCtxKey, logger)

	var vmList vmv1.VirtualMachineList

	for {
		time.Sleep(time.Duration(r.PollingPeriod) * time.Second)
		fmt.Println("start polling vm latest info")

		// Get all VM Stacks
		stackList, err := r.osService.StackListAll(ctx)
		if err != nil {
			logger.Error(err, "failed to list stacks")
			continue
		}

		// transfer stack list to map
		stackMap := make(map[string]*stacks.ListedStack)
		for _, stack := range stackList {
			stackMap[stack.Name] = &stack
		}

		// Get all vm CRD
		err = r.cliReader.List(ctx, &vmList)
		if err != nil {
			logger.Error(err, "Failed to list vm crd")
			continue
		}

		for i := range vmList.Items {
			vmStatus := vmList.Items[i].Status.VmStatus
			if strings.HasSuffix(vmStatus, "COMPLETE") || strings.HasSuffix(vmStatus, "FAILED") {
				continue
			}
			err := r.checkAndUpdate(ctx, &vmList.Items[i], stackMap)
			if err != nil {
				logger.Error(err, "Failed to update vm CRD")
			}
		}

	}
}

func (r *VirtualMachineReconciler) checkAndUpdate(ctx context.Context, vm *vmv1.VirtualMachine, stackMap map[string]*stacks.ListedStack) error {
	key := vm.Name
	stackStatus := stackMap[key].Status
	// 1. if vm status not changed, ingore this time
	if vm.Status.VmStatus == stackStatus {
		fmt.Printf("vm[%s] of project[%s] didn't change status, nothing to update", vm.Name, vm.Spec.Project.ProjectID)
		return nil
	}

	// 2. remove vm crd if phase is deleting and stack not found
	if vm.Spec.AssemblyPhase == vmv1.Deleting {
		if _, ok := stackMap[key]; !ok {
			err := r.client.Delete(ctx, vm)
			if err != nil {
				fmt.Printf("Failed to delete crd %s\n", vm.Name)
				return err
			}
			r.vmCache.del(key)
			return nil
		}
	}

	// 3. update vm status
	if vm.Spec.AssemblyPhase == vmv1.Creating || vm.Spec.AssemblyPhase == vmv1.Updating {
		vm.Status.VmStatus = stackStatus
		return r.doUpdateVmCrdStatus(ctx, vm)
	}

	return nil
}

func (r *VirtualMachineReconciler) doUpdateVmCrdStatus(ctx context.Context, vm *vmv1.VirtualMachine) error {
	logger := utils.GetLoggerOrDie(ctx)

	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := r.client.Update(ctx, vm); err != nil {
			logger.Error(err, "Failed to update VM CRD")
			return err
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to update VM %s: %v", vm.Name, err)
	}
	return nil
}

func spec2HeatParams(spec interface{}, params *map[string]interface{}) error {
	t := reflect.TypeOf(spec)
	v := reflect.ValueOf(spec)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
		v = v.Elem()
	}

	if t.Kind() == reflect.Struct {
		for i := 0; i < t.NumField(); i++ {
			switch t.Field(i).Name {
			case "Replicas":
				(*params)["replicas"] = v.Field(i).Interface()
			case "NamePrefix":
				(*params)["replicas"] = v.Field(i).Interface()
			case "Image":
				(*params)["image"] = v.Field(i).Interface()
			case "Flavor":
				(*params)["flavor"] = v.Field(i).Interface()
			case "AvailableZone":
				(*params)["availability_zone"] = v.Field(i).Interface()
			case "KeyName":
				(*params)["key_name"] = v.Field(i).Interface()
			case "AdminPass":
				(*params)["admin_pass"] = v.Field(i).Interface()
			case "BootVolumeType":
				(*params)["boot_volume_type"] = v.Field(i).Interface()
			case "BootVolumeSize":
				(*params)["boot_volume_size"] = v.Field(i).Interface()
			case "SecurityGroup":
				(*params)["security_group"] = v.Field(i).Interface()
			case "ExternalNetwork":
				(*params)["external_network"] = v.Field(i).Interface()
			case "ExistingNetwork":
				(*params)["existing_network"] = v.Field(i).Interface()
			case "ExistingSubnet":
				(*params)["existing_subnet"] = v.Field(i).Interface()
			case "PrivateNetworkCidr":
				(*params)["private_network_cidr"] = v.Field(i).Interface()
			case "PrivateNetworkName":
				(*params)["private_network_name"] = v.Field(i).Interface()
			case "NeutronAz":
				(*params)["neutron_az"] = v.Field(i).Interface()
			case "FloatingIp":
				(*params)["floating_ip"] = v.Field(i).Interface()
			case "FloatingIpBandwidth":
				(*params)["floating_ip_bandwidth"] = v.Field(i).Interface()
			default:
				fmt.Println("Unknown spec field")
			}
		}
	}
	return nil
}

func (r *VirtualMachineReconciler) buildStackCreateOpts(ctx context.Context, name string, vm *vmv1.VirtualMachine) (*stacks.CreateOpts, error) {
	params := make(map[string]interface{})
	spec2HeatParams(&vm.Spec.Server, &params)
	spec2HeatParams(&vm.Spec.Network, &params)

	tplDir := strings.Join([]string{tplOutBase, vm.Name}, "/")
	tpl := vmtpl.New(r.osService.ConfigDir, tplDir, params)
	err := tpl.RenderToFile()
	if err != nil {
		fmt.Println("render heat template file failed")
		return nil, err
	}

	template := &stacks.Template{}
	tplUrl := strings.Join([]string{tplDir, "vm_group.yaml.tpl"}, "/")
	fmt.Printf("tplUrl:%s\n", tplUrl)
	template.TE = stacks.TE{
		URL: tplUrl,
	}

	return &stacks.CreateOpts{
		Name:         name,
		TemplateOpts: template,
		Parameters:   params,
		Tags:         []string{openstack.StackTag},
	}, nil
}

func (v *vmCache) get(key string) (vmv1.VirtualMachine, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	vm, ok := v.vmMap[key]
	return vm, ok
}

func (v *vmCache) set(key string, vm vmv1.VirtualMachine) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.vmMap[key] = vm
}

func (v *vmCache) del(key string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.vmMap, key)
}
