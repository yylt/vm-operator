package controllers

import (
	"context"
	"fmt"
	"time"

	vmv1 "easystack.io/vm-operator/pkg/api/v1"
	"easystack.io/vm-operator/pkg/manage"
	"easystack.io/vm-operator/pkg/template"
	klog "k8s.io/klog/v2"
)

const (
	OpCheck = "check"
)

type Server struct {
	nova           *Nova
	lb             *LoadBalance
	fip            *Floatip
	k8smgr         *manage.K8sMgr
	opmgr          *manage.OpenMgr
	k8sync, opsync time.Duration
	enablelead     bool
}

func NewServer(engine *template.Template, tmpdir string, k8smgr *manage.K8sMgr, enableleader bool, k8sync, opsync time.Duration) *Server {
	opmgr := manage.NewOpMgr()
	heat := NewHeat(engine, tmpdir, opmgr)
	nova := NewNova(heat, opmgr)
	lb := NewLoadBalance(heat, opmgr, k8smgr, nova)
	fip := NewFloatip(heat, opmgr, k8smgr, lb)
	return &Server{
		k8smgr:     k8smgr,
		opmgr:      opmgr,
		nova:       nova,
		k8sync:     k8sync,
		opsync:     opsync,
		lb:         lb,
		fip:        fip,
		enablelead: enableleader,
	}
}

func (m *Server) ServerRecocile(vm *vmv1.VirtualMachine) {
	//todo
}

func (m *Server) Process(vm *vmv1.VirtualMachine) {
	var (
		err error
	)
	if vm.Spec.Auth == nil {
		updateCondition(&vm.Status, OpCheck, fmt.Errorf("not found auth info"))
		return
	}
	err = m.nova.Process(vm)
	if err != nil {
		updateCondition(&vm.Status, manage.Vm.String(), err)
	}
	err = m.lb.Process(vm)
	if err != nil {
		updateCondition(&vm.Status, manage.Lb.String(), err)
	}
	err = m.fip.Process(vm)
	if err != nil {
		updateCondition(&vm.Status, manage.Fip.String(), err)
	}
}

func (m *Server) NeedLeaderElection() bool {
	return m.enablelead
}

func (m *Server) Start(ctx context.Context) error {
	m.opmgr.Run(m.opsync)
	m.k8smgr.Run(m.k8sync, m.lb.GetIpByLink)
	go func() {
		<-ctx.Done()
		err := ctx.Err()
		if err != nil {
			klog.Errorf("context done and message:%v", err)
		}
		m.opmgr.Stop()
		m.k8smgr.Stop()
	}()
	return nil
}

func updateCondition(stat *vmv1.VirtualMachineStatus, op string, err error) {
	if err == nil {
		return
	}
	resstr := op
	for _, cond := range stat.Conditions {
		if cond.Type == resstr {
			if cond.Reason == err.Error() {
				return
			}
		}
	}
	stat.Conditions = append(stat.Conditions, &vmv1.Condition{
		LastUpdateTime: time.Now().Format(time.RFC3339),
		Reason:         err.Error(),
		Type:           resstr,
	})
}
