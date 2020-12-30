package controllers

import (
	"fmt"
	"net"
	"sort"
	"sync"

	vmv1 "easystack.io/vm-operator/pkg/api/v1"
	"easystack.io/vm-operator/pkg/manage"
	"easystack.io/vm-operator/pkg/util"

	"github.com/gophercloud/gophercloud/openstack/networking/v2/extensions/lbaas_v2/loadbalancers"
	"github.com/gophercloud/gophercloud/pagination"
	klog "k8s.io/klog/v2"
)

type LbResult struct {
	Stat string
	Id   string
	Ip   string
	Name string
}

func (s *LbResult) DeepCopy() *LbResult {
	tmp := new(LbResult)
	tmp.Id = s.Id
	tmp.Name = s.Name
	tmp.Stat = s.Stat
	tmp.Ip = s.Ip
	return tmp
}

func (s *LbResult) DeepCopyFrom(ls *loadbalancers.LoadBalancer) {

	//TODO make sure which id, that will be used by floating ip
	//s.Id=ls.ID
	s.Id = ls.VipPortID
	s.Name = ls.Name
	s.Ip = ls.VipAddress
	s.Stat = ls.OperatingStatus
}

type LoadBalance struct {
	mgr    *manage.OpenMgr
	k8smgr *manage.K8sMgr
	nova   *Nova
	heat   *Heat

	mu sync.RWMutex
	//key: lbname
	//value: LbResult
	lbs map[string]*LbResult

	linkname map[int64]string
}

func NewLoadBalance(heat *Heat, mgr *manage.OpenMgr, k8smgr *manage.K8sMgr, nova *Nova) *LoadBalance {
	lb := &LoadBalance{
		mgr:      mgr,
		k8smgr:   k8smgr,
		nova:     nova,
		heat:     heat,
		lbs:      make(map[string]*LbResult),
		linkname: make(map[int64]string),
	}
	mgr.Regist(manage.Lb, lb.addLbStore)
	return lb
}

func (p *LoadBalance) GetResource(vm *vmv1.VirtualMachine) *vmv1.ServerStat {
	if vm == nil || vm.Status.NetStatus == nil {
		return nil
	}
	return &vm.Status.NetStatus.ServerStat
}

func (p *LoadBalance) GetIpByLink(link string) net.IP {
	id := util.Hashid([]byte(link))
	p.mu.RLock()
	defer p.mu.RUnlock()
	v, ok := p.linkname[id]
	if !ok {
		return nil
	}
	lb, ok := p.lbs[v]
	if !ok {
		return nil
	}
	return net.ParseIP(lb.Ip)
}

func (p *LoadBalance) addLbStore(page pagination.Page) {
	lists, err := loadbalancers.ExtractLoadBalancers(page)
	if err != nil {
		klog.Errorf("loadbalancers extract page failed:%v", err)
		return
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, lb := range lists {
		v, ok := p.lbs[lb.Name]
		if ok {
			klog.V(3).Infof("callback update loadbalance: %v", lb)
			v.DeepCopyFrom(&lb)
		}
	}
	return
}

func (p *LoadBalance) addLb(stat *vmv1.ResourceStatus, spec *vmv1.LoadBalanceSpec) {
	if stat == nil || stat.StackName == "" {
		return
	}
	resname := stat.StackName
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.lbs[resname]
	if !ok {
		p.lbs[resname] = &LbResult{
			Stat: stat.ServerStat.ResStat,
			Id:   stat.ServerStat.Id,
			Ip:   stat.ServerStat.Ip,
			Name: stat.ServerStat.ResName,
		}
		if spec.Link != "" {
			id := util.Hashid([]byte(spec.Link))
			p.linkname[id] = resname
		}
	}
	return
}

func (p *LoadBalance) update(stat *vmv1.ResourceStatus) {
	if stat == nil || stat.StackName == "" {
		klog.Infof("lb update failed: not found resource name")
		return
	}
	resname := stat.StackName
	p.mu.RLock()
	defer p.mu.RUnlock()
	v, ok := p.lbs[resname]
	if !ok {
		return
	}
	klog.V(3).Infof("update load balance ResourceStatus:%v", v)
	stat.ServerStat.ResStat = v.Stat
	stat.ServerStat.Ip = v.Ip
	stat.ServerStat.Id = v.Id
	stat.ServerStat.ResName = v.Name
}

func (p *LoadBalance) Process(vm *vmv1.VirtualMachine) (reterr error) {
	var (
		spec             = vm.Spec.LoadBalance
		stat             = vm.Status.NetStatus
		ips              []string
		fnova, removeRes bool
		k8sres           []*manage.Result
	)

	if spec == nil {
		return nil
	}
	err := validLbSpec(spec)
	if err != nil {
		return err
	}

	defer func() {
		//Remove stack if pod link not exist
		if removeRes {
			if vm.Status.NetStatus != nil {
				p.heat.DeleteStack(vm.Status.NetStatus)
				p.mu.Lock()
				delete(p.lbs, vm.Status.NetStatus.Name)
				p.mu.Unlock()
			}
			if !fnova {
				p.k8smgr.DelLinks(spec.Link)
			}
		} else {
			if reterr == nil {
				if vm.Status.NetStatus != nil {
					p.update(vm.Status.NetStatus)
				}
				if !fnova && len(k8sres) != 0 {
					//TODO update members from k8sres
				}
			}
		}
	}()
	if vm.DeletionTimestamp != nil {
		removeRes = true
		return nil
	}
	if spec.Link == "" {
		// Try find poolmembers from nova info
		ips = p.nova.GetAllIps(vm)
		if len(ips) == 0 {
			klog.Infof("nova servers not ready, can not fetch members")
			return nil
		}
		sort.Strings(ips)
		klog.V(3).Infof("fetch nova pool ip:%v", ips)
		fnova = true
	} else {
		// Try find poolmembers ip from link
		if !p.k8smgr.LinkIsExist(spec.Link) {
			lbip := net.ParseIP(spec.LbIp)
			portmaps := make(map[int32]string)
			for _, po := range spec.Ports {
				portmaps[po.Port] = po.Protocol
			}
			p.k8smgr.AddLinks(spec.Link, lbip, portmaps)
		}
		k8sres = p.k8smgr.SecondIp(spec.Link)
		if len(k8sres) == 0 {
			return fmt.Errorf("k8s pod ip not ready!")
		}
	}
	if !fnova {
		for _, v := range k8sres {
			ips = append(ips, v.Ip.String())
			klog.V(3).Infof("fetch k8s pool ip:%v", ips)
		}
	}
	for i, _ := range spec.Ports {
		spec.Ports[i].Ips = ips
	}
	var resname string

	if stat == nil || stat.StackName == "" {
		resname = fmt.Sprintf("%s-%s", spec.Name, util.RandStr(5))
		vm.Status.NetStatus = &vmv1.ResourceStatus{
			StackName: resname,
		}
	} else {
		resname = stat.StackName
	}
	spec.Name = resname
	err = p.heat.Process(manage.Lb, vm)
	if err != nil {
		return err
	}
	p.addLb(vm.Status.NetStatus, spec)
	return nil
}

func validLbSpec(spec *vmv1.LoadBalanceSpec) error {
	if len(spec.Ports) == 0 {
		return fmt.Errorf("not found port-protocol list info")
	}
	for _, port := range spec.Ports {
		if port.Port > 65535 {
			return fmt.Errorf("port should be less than 65535")
		}
	}
	if spec.LbIp != "" {
		if net.ParseIP(spec.LbIp) == nil {
			return fmt.Errorf("loadbalance ip(%v) can not parse", spec.LbIp)
		}
	}
	return nil
}
