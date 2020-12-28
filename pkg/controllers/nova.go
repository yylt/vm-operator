package controllers

import (
	"bytes"
	"fmt"
	"sync"

	vmv1 "easystack.io/vm-operator/pkg/api/v1"
	"easystack.io/vm-operator/pkg/manage"
	"easystack.io/vm-operator/pkg/util"

	"github.com/gophercloud/gophercloud/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/pagination"
	klog "k8s.io/klog/v2"
)

const (
	ServerRunStat  = "ACTIVE"
	ServerStopStat = "SHUTOFF"
)

type VmResult struct {
	Stat      string                       `json:"status"`
	Name      string                       `json:"name"`
	Id        string                       `json:"id"`
	Ip4addres map[string]string            `json:"-"`
	Addresses map[string][]servers.Address `json:"addresses,omitempty"`
}

func (s *VmResult) DeepCopy() *VmResult {
	tmp := &VmResult{
		Ip4addres: make(map[string]string),
	}
	tmp.Id = s.Id
	tmp.Name = s.Name
	tmp.Stat = s.Stat
	for k, v := range s.Ip4addres {
		tmp.Ip4addres[k] = v
	}

	return tmp
}

func (s *VmResult) DeepCopyInto(netname string, vm *vmv1.ServerStat) {
	vm.Id = s.Id
	vm.ResName = s.Name
	vm.ResStat = s.Stat
	for name, addr := range s.Ip4addres {
		if netname == name {
			vm.Ip = addr
			break
		}
	}
}

func (s *VmResult) DeepCopyFrom(ls *VmResult) {
	//TODO make sure which id, that will be used by floating ip
	//s.Id=ls.ID
	s.Id = ls.Id
	s.Name = ls.Name
	s.Stat = ls.Stat
	for name, addrs := range ls.Addresses {
		for _, val := range addrs {
			if val.Version == 4 {
				s.Ip4addres[name] = val.Address
			}
		}
	}
}

type Nova struct {
	mgr  *manage.OpenMgr
	heat *Heat

	mu sync.RWMutex
	// key: the name which cut suffix [-x]
	// value: vmname : VmResults
	vms map[string]map[string]*VmResult
}

func (p *Nova) GetAllIps(vm *vmv1.VirtualMachine) []string {
	if vm == nil || len(vm.Status.Members) == 0 {
		return nil
	}
	var ips []string
	for _, v := range vm.Status.Members {
		if v.Ip == "" {
			continue
		}
		ips = append(ips, v.Ip)
	}
	return ips
}

func NewNova(heat *Heat, mgr *manage.OpenMgr) *Nova {
	vm := &Nova{
		mgr:  mgr,
		heat: heat,
		vms:  make(map[string]map[string]*VmResult),
	}
	mgr.Regist(manage.Vm, vm.addVmStore)
	return vm
}

func (p *Nova) addVmStore(page pagination.Page) {

	var svs []*VmResult
	err := servers.ExtractServersInto(page, &svs)
	if err != nil {
		klog.Errorf("servers extract page failed:%v", err)
		return
	}
	prefix := prefixName()
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, sv := range svs {
		svbyte := []byte(sv.Name)
		if !bytes.HasPrefix(svbyte, prefix) {
			continue
		}
		// NOTE: cut last two byte, thus always "-[0-9]"
		// Require replicse can not bigger than 9
		v, ok := p.vms[string(svbyte[:len(svbyte)-2])]
		if ok {
			klog.V(3).Infof("update nova stat:%v", sv)
			result, ok := v[sv.Name]
			if ok {
				result.DeepCopyFrom(sv)
			} else {
				v[sv.Name] = sv.DeepCopy()
			}
		}
	}
	return
}

func (p *Nova) addVm(stat *vmv1.VirtualMachineStatus) {
	if stat == nil || stat.VmStatus == nil || stat.VmStatus.StackName == "" {
		return
	}
	resname := stat.VmStatus.StackName
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.vms[resname]
	if !ok {
		klog.V(2).Infof("add listen nova by name:%v", resname)
		p.vms[resname] = make(map[string]*VmResult)
	}
	return
}

func (p *Nova) update(stat *vmv1.VirtualMachineStatus, netspec *vmv1.ServerSpec) {
	if stat == nil || stat.VmStatus == nil || stat.VmStatus.StackName == "" {
		return
	}
	resname := stat.VmStatus.StackName
	memmaps := make(map[string]int)
	for i, mem := range stat.Members {
		memmaps[mem.Id] = i
	}
	var vmstat []*vmv1.ServerStat
	p.mu.RLock()
	defer p.mu.RUnlock()
	svs, ok := p.vms[resname]
	if !ok {
		return
	}
	for _, vm := range svs {
		index, ok := memmaps[vm.Id]
		if ok {
			klog.V(3).Infof("update nova ResourceStatus: %v", vm)
			vm.DeepCopyInto(netspec.Subnet.NetworkName, stat.Members[index])
		} else {
			newmem := &vmv1.ServerStat{}
			klog.V(3).Infof("update nova ResourceStatus: %v", vm)
			vm.DeepCopyInto(netspec.Subnet.NetworkName, newmem)
			vmstat = append(vmstat, newmem)
		}
	}
	if len(vmstat) != 0 {
		stat.Members = append(stat.Members, vmstat...)
	}
}

func (p *Nova) Process(vm *vmv1.VirtualMachine) (reterr error) {
	var (
		spec      = vm.Spec.Server
		stat      = vm.Status.VmStatus
		removeRes bool
	)
	if spec == nil {
		return nil
	}
	err := validVmSpec(spec)
	if err != nil {
		return err
	}

	defer func() {
		//Remove stack if pod link not exist
		if removeRes {
			if vm.Status.VmStatus != nil {
				p.heat.DeleteStack(vm.Status.VmStatus)
				p.mu.Lock()
				delete(p.vms, vm.Status.VmStatus.StackName)
				p.mu.Unlock()
			}
		} else {
			if reterr == nil {
				if vm.Status.VmStatus != nil {
					p.update(&vm.Status, vm.Spec.Server)
				}
			}
		}
	}()
	if vm.DeletionTimestamp != nil {
		removeRes = true
		return nil
	}
	// 1. prefixName used as filter prefix key
	// 2. rand string to dict same name
	resname := fmt.Sprintf("%s-%s-%s", prefixName(), spec.Name, util.RandStr(5))
	if stat == nil || stat.StackName == "" {
		vm.Status.VmStatus = &vmv1.ResourceStatus{
			StackName: resname,
		}
	} else {
		resname = stat.StackName
	}
	spec.Name = resname
	err = p.heat.Process(manage.Vm, vm)
	if err != nil {
		return err
	}
	p.addVm(&vm.Status)
	return nil
}

func validVmSpec(spec *vmv1.ServerSpec) error {
	if spec.BootImage == "" && spec.BootVolumeId == "" {
		return fmt.Errorf("Boot image or boot volume must not nil both!")
	}
	if spec.Replicas > 9 {
		return fmt.Errorf("replicas must little than 10!")
	}
	return nil
}

func prefixName() []byte {
	return []byte(manage.Vm.String())
}
