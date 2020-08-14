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
	vmv1 "easystack.io/vm-operator/pkg/api/v1"
	"github.com/go-logr/logr"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	cli "sigs.k8s.io/controller-runtime/pkg/client"
)

// VirtualMachineReconciler reconciles a VirtualMachine object
type VirtualMachineReconciler struct {
	cli.Client
	logger    logr.Logger
	scheme    *runtime.Scheme
	ctx       context.Context
	osService *OSService
}

func NewVirtualMachine(c cli.Client, r cli.Reader, logger logr.Logger, oss *OSService) *VirtualMachineReconciler {
	return &VirtualMachineReconciler{
		Client:    c,
		logger:    logger,
		ctx:       context.Background(),
		osService: oss,
	}
}

func (r *VirtualMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&vmv1.VirtualMachine{}).
		Complete(r)
}

// +kubebuilder:rbac:groups=mixapp.easystack.io,resources=virtualmachines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mixapp.easystack.io,resources=virtualmachines/status,verbs=get;update;patch

func (r *VirtualMachineReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	var (
		vm   vmv1.VirtualMachine
		err  error
		stat *vmv1.VirtualMachineStatus
	)

	err = r.Get(r.ctx, req.NamespacedName, &vm)
	if err != nil {
		if apierrs.IsNotFound(err) {
			// Delete event
			r.logger.Info("object had deleted", "object", req.String())
			return ctrl.Result{}, nil
		}
		r.logger.Error(err, "get object failed", "object", req.String())
	}
	if vm.DeletionTimestamp != nil {
		r.logger.Info("object is deleting", "object", req.String())
		stat = r.osService.Delete(&vm.Spec, vm.Status.DeepCopy())
	} else {
		r.logger.Info("START Reconcile", "object", req.String())
		switch vm.Spec.AssemblyPhase {
		case vmv1.Creating:
			fallthrough
		case vmv1.Updating:
			stat, err = r.osService.Reconcile(&vm)
		case vmv1.Deleting:
			stat = r.osService.Delete(&vm.Spec, vm.Status.DeepCopy())
		default:
			r.logger.Info("Not found assemblyPhase", "object", req.String())
			return ctrl.Result{}, nil
		}
	}
	if stat == nil {
		return ctrl.Result{}, err
	}
	//stat.DeepCopyInto(&vm.Status)
	err = r.doUpdateVmCrdStatus(req.NamespacedName, stat)
	return ctrl.Result{}, err
}

func (r *VirtualMachineReconciler) doUpdateVmCrdStatus(nsname types.NamespacedName, stat *vmv1.VirtualMachineStatus) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		original := &vmv1.VirtualMachine{}
		if err := r.Get(r.ctx, nsname, original); err != nil {
			r.logger.Error(err, "get object failed", "object", nsname.String())
			return err
		}
		original.Status = *stat
		if original.DeletionTimestamp != nil {
			original.Finalizers = nil
			r.logger.Info("delete finalizers", "object", nsname.String())
			return r.Update(r.ctx, original)
		} else {
			if original.Finalizers == nil {
				original.Finalizers = append(original.Finalizers, nsname.String())
				r.logger.Info("set finalizers", "object", nsname.String())
			}
			return r.Update(r.ctx, original)
		}
	})
}
