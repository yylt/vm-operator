package controllers

import (
	"easystack.io/vm-operator/pkg/template"
	"fmt"
	"github.com/tidwall/gjson"
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
	heat.RegistReOrderFunc(template.Lb, reorderSpec)
	return lb
}

func (p *LoadBalance) GetResource(vm *vmv1.VirtualMachine) *vmv1.ServerStat {
	if vm == nil || vm.Status.NetStatus == nil {
		return nil
	}
	return &vm.Status.NetStatus.ServerStat
}

func (p *LoadBalance) GetIpByLink(link string) net.IP {
	id := util.Hashid(util.Str2bytes(link))
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
			id := util.Hashid(util.Str2bytes(spec.Link))
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
				klog.V(2).Infof("remove load balance resource")
				p.heat.DeleteStack(vm.Status.NetStatus)
				p.mu.Lock()
				delete(p.lbs, vm.Status.NetStatus.Name)
				p.mu.Unlock()
			}
			if !fnova {
				klog.V(2).Infof("remove link from k8s manager")
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
		klog.V(2).Infof("update server(nova) ip list:%v", ips)
		fnova = true
	} else {
		// Try find poolmembers ip from link
		if !p.k8smgr.LinkIsExist(spec.Link) {
			lbip := net.ParseIP(spec.LbIp)
			p.k8smgr.AddLinks(spec.Link, lbip, spec.Ports, spec.UseService)
		}
		k8sres = p.k8smgr.SecondIp(spec.Link)
		if len(k8sres) == 0 {
			if stat == nil {
				err = fmt.Errorf("not found ip on link and no stack found, skip")
				return err
			}
			klog.V(2).Info("not found ip on link, but still update stack")
		}
	}
	if !fnova {
		for _, v := range k8sres {
			ips = append(ips, v.Ip.String())
		}
		klog.V(2).Infof("update pod ip list:%v", ips)
	}
	for i, _ := range spec.Ports {
		spec.Ports[i].Ips = ips
	}
	var resname string

	if stat == nil || stat.StackName == "" {
		resname = fmt.Sprintf("%s-%s", vm.Name, util.RandStr(5))
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

// NOTE: on reduce situation, we should also ensure index is same with older
//
// such as listen0 (90 and tcp) is miss, and listen1 (80 and udp) is on
// after render, listen0 is disappeard, and listen1 also is (80 and udp)

// less -> much
// much -> less
func reorderSpec(spec *vmv1.VirtualMachineSpec, stat *vmv1.ResourceStatus) {
	if stat == nil || stat.Template == "" || len(spec.LoadBalance.Ports) == 0 {
		return
	}
	var (
		oldips     []string
		currentips = map[string]struct{}{}
		oldmaps    = make(map[string]struct{})
		newports   []*vmv1.PortMap
	)

	for _, pm := range spec.LoadBalance.Ports {
		if len(currentips) == 0 {
			for _, addr := range pm.Ips {
				currentips[addr] = struct{}{}
			}
		}
	}
	//fix members
	template.FindLbMembers(util.Str2bytes(stat.Template), spec.LoadBalance.Name, func(value *gjson.Result) {
		if value.IsObject() {
			ipaddr := value.Get("address").String()
			if _, ok := oldmaps[ipaddr]; ok {
				return
			}
			oldmaps[ipaddr] = struct{}{}
			oldips = append(oldips, ipaddr)
		}
	})
	if len(oldips) == 0 || len(currentips) == 0 {
		return
	}
	klog.V(2).Info("members old order: ", oldips)
	newips := orderMembers(oldips, currentips)
	klog.V(2).Info("members new order: ", newips)

	//TODO parse old listens, but now we do not need!
	for _, dv := range spec.LoadBalance.Ports {
		dv.Ips = nil
		tmp := dv.DeepCopy()
		tmp.Ips = newips
		klog.V(2).Infof("append portmap %v", tmp)
		newports = append(newports, tmp)
	}
	spec.LoadBalance.Ports = newports
	return
}

func orderListens(src []*vmv1.PortMap, dst map[string]*vmv1.PortMap, ips []string) (ss []*vmv1.PortMap) {
	var (
		srcmap = make(map[string]*vmv1.PortMap)
	)
	for _, v := range src {
		key := portMapHashKey(v)
		srcmap[key] = v
	}
	for _, srcv := range src {
		key := portMapHashKey(srcv)
		newpm := srcv.DeepCopy()
		if _, ok := dst[key]; ok {
			newpm.Ips = ips
		} else {
			newpm.Ips = nil
			newpm.Port = 0
		}
		klog.V(2).Infof("reorder listen append portmap %v", newpm)
		ss = append(ss, newpm)
	}
	//TODO dst value is reused
	for dstk, dv := range dst {
		if _, ok := srcmap[dstk]; !ok {
			dv.Ips = ips
			klog.V(2).Infof("reorder listen append portmap %v", dv)
			ss = append(ss, dv)
		}
	}
	return
}

func orderMembers(src []string, dst map[string]struct{}) (ss []string) {
	var (
		srcmap = make(map[string]struct{})
	)
	for _, v := range src {
		srcmap[v] = struct{}{}
	}
	for _, srck := range src {
		if _, ok := dst[srck]; ok {
			ss = append(ss, srck)
		} else {
			ss = append(ss, "")
		}
	}
	for dstk, _ := range dst {
		if _, ok := srcmap[dstk]; !ok {
			klog.V(2).Infof("append ip from dst %v", dstk)
			ss = append(ss, dstk)
		}
	}
	return
}

func validLbSpec(spec *vmv1.LoadBalanceSpec) error {
	if len(spec.Ports) == 0 {
		return fmt.Errorf("not found port-protocol list info")
	}
	for _, port := range spec.Ports {
		if port.Port > 65535 || port.Port <= 0 {
			return fmt.Errorf("port should be less than 65535 and bigger than 0")
		}
	}
	if spec.LbIp != "" {
		if net.ParseIP(spec.LbIp) == nil {
			return fmt.Errorf("parse lb ip(%v) faild", spec.LbIp)
		}
	}
	for i, v := range spec.Ports {
		if v.PodPort == 0 {
			spec.Ports[i].PodPort = v.Port
		}
	}
	return nil
}

func portMapHashKey(v *vmv1.PortMap) string {
	var p int32
	if v.Port == 0 {
		p = v.PodPort
	} else {
		p = v.Port
	}
	return fmt.Sprintf("%s%d", v.Protocol, p)
}
