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
	"reflect"

	vmv1 "easystack.io/vm-operator/pkg/api/v1"

	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	cli "sigs.k8s.io/controller-runtime/pkg/client"

	klog "k8s.io/klog/v2"
)

// VirtualMachineReconciler reconciles a VirtualMachine object
type VirtualMachineReconciler struct {
	cli.Client
	ctx    context.Context
	server *Server
}

func NewVirtualMachine(mgr ctrl.Manager, mg *Server) *VirtualMachineReconciler {
	vmm := &VirtualMachineReconciler{
		Client: mgr.GetClient(),
		ctx:    context.Background(),
		server: mg,
	}
	err := vmm.probe(mgr)
	if err != nil {
		panic(err)
	}
	return vmm
}

func (r *VirtualMachineReconciler) probe(mgr ctrl.Manager) error {
	var (
		err error
	)
	err = mgr.Add(r.server)
	if err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&vmv1.VirtualMachine{}).
		Complete(r)
}

// +kubebuilder:rbac:groups=mixapp.easystack.io,resources=virtualmachines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mixapp.easystack.io,resources=virtualmachines/status,verbs=get;update;patch
func (r *VirtualMachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var (
		vm  vmv1.VirtualMachine
		err error
	)

	err = r.Get(ctx, req.NamespacedName, &vm)
	if err != nil {
		if apierrs.IsNotFound(err) {
			// Delete event
			klog.Infof("object %s had deleted", req.String())
			return ctrl.Result{}, nil
		}
		klog.Errorf("get object %s failed:%s", req.String(), err)
	}
	newvmobj := &vm

	if newvmobj.DeletionTimestamp != nil {
		klog.V(2).Infof("object %s is deleting", req.String())
		r.server.Process(newvmobj)
	} else {
		klog.V(2).Infof("START Reconcile:%s", req.String())
		switch newvmobj.Spec.AssemblyPhase {
		case vmv1.Recreate:
			fallthrough
		case vmv1.Start:
			fallthrough
		case vmv1.Stop:
			r.server.ServerRecocile(newvmobj)
			fallthrough
		case vmv1.Creating:
			fallthrough
		case vmv1.Updating:
			r.server.Process(newvmobj)
		case vmv1.Deleting:
			err = r.Delete(r.ctx, newvmobj)
			if err != nil {
				klog.Errorf("delete object %s failed:%v", req.String(), err)
				return ctrl.Result{}, nil
			}
		default:
			updateCondition(&newvmobj.Status, OpCheck, fmt.Errorf("not found assemblyPhase in spec"))
			return ctrl.Result{}, nil
		}
	}
	if &newvmobj.Status == nil {
		return ctrl.Result{}, err
	}

	err = r.doUpdateVmCrdStatus(req.NamespacedName, &newvmobj.Status)
	if err != nil {
		klog.Errorf("update cr failed:%v", err)
	}
	return ctrl.Result{}, err
}

func (r *VirtualMachineReconciler) doUpdateVmCrdStatus(nsname types.NamespacedName, stat *vmv1.VirtualMachineStatus) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		original := &vmv1.VirtualMachine{}
		var isup bool
		if err := r.Get(r.ctx, nsname, original); err != nil {
			klog.Errorf("get object %s failed:%v", nsname.String(), err)
			return err
		}
		if !reflect.DeepEqual(&original.Status, stat) {
			stat.DeepCopyInto(&original.Status)
			isup = true
		}
		if original.DeletionTimestamp != nil {
			original.Finalizers = nil
			klog.Infof("remove finalizers: %v", nsname.String())
			isup = true
		} else if original.Finalizers == nil {
			original.Finalizers = append(original.Finalizers, nsname.String())
			klog.Infof("add finalizers: %v", nsname.String())
			isup = true
		}
		if isup {
			return r.Update(r.ctx, original)
		}
		return nil
	})
}
