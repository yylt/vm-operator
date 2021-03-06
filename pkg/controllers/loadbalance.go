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

type sortIpByIndex struct {
	idx int
	ip  net.IP
}

type sortIps []*sortIpByIndex

func (s sortIps) Swap(i, j int) {
	s[i].idx, s[j].idx = s[j].idx, s[i].idx
	s[i].ip, s[j].ip = s[j].ip, s[i].ip
}

func (s sortIps) Len() int {
	return len(s)
}

func (s sortIps) Less(i, j int) bool {
	return s[i].idx < s[j].idx
}

type LbResult struct {
	Stat string
	Id   string
	Lbid string
	Ip   string
	Name string

	//had sync or not
	sync bool

	//delete operat by user from openstack api
	deleted bool
}

func (s *LbResult) DeepCopy() *LbResult {
	tmp := new(LbResult)
	tmp.Id = s.Id
	tmp.Name = s.Name
	tmp.Lbid = s.Lbid
	tmp.Stat = s.Stat
	tmp.Ip = s.Ip
	return tmp
}

func (s *LbResult) DeepCopyFrom(ls *loadbalancers.LoadBalancer) {

	//TODO make sure which id, that will be used by floating ip
	//s.Id=ls.ID
	s.Id = ls.VipPortID
	s.Name = ls.Name
	s.Lbid = ls.ID
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

	//key: link hashid
	// value: lb name
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
	p.mu.Lock()
	defer p.mu.Unlock()
	exists := make(map[string]struct{}, len(p.lbs))
	for _, lb := range lists {
		v, ok := p.lbs[lb.Name]
		if ok {
			klog.V(3).Infof("callback update loadbalance: %v", lb)
			v.DeepCopyFrom(&lb)
			exists[lb.Name] = struct{}{}
			v.deleted = false
		}
		v, ok = p.lbs[lb.ID]
		if ok {
			klog.V(3).Infof("callback update loadbalance: %v", lb)
			v.DeepCopyFrom(&lb)
			exists[lb.ID] = struct{}{}
			v.deleted = false
		}
	}
	for k, v := range p.lbs {
		v.sync = true
		if _, ok := exists[k]; !ok {
			klog.V(2).Infof("loadbalance(%v) not found", v.Name)
			v.deleted = true
		}
	}
	return
}

func (p *LoadBalance) addLb(stat *vmv1.ResourceStatus, spec *vmv1.LoadBalanceSpec) {
	if stat == nil || stat.StackName == "" {
		return
	}
	var (
		bykey string
	)
	resname := stat.StackName
	lbid := stat.ServerStat.Id
	p.mu.Lock()
	defer p.mu.Unlock()
	if lbid != "" {
		bykey = lbid
		delete(p.lbs, resname)
	} else {
		bykey = resname
	}
	_, ok := p.lbs[bykey]
	if !ok {
		p.lbs[bykey] = &LbResult{
			Stat: stat.ServerStat.ResStat,
			Lbid: stat.ServerStat.Id,
			Ip:   stat.ServerStat.Ip,
			Name: stat.ServerStat.ResName,
			Id:   stat.ServerStat.CreateTime,
		}
		if spec.Link != "" {
			id := util.Hashid(util.Str2bytes(spec.Link))
			p.linkname[id] = bykey
		}
	}
	return
}

func (p *LoadBalance) update(stat *vmv1.ResourceStatus) {
	if stat == nil || stat.StackName == "" {
		klog.Infof("lb update failed: not found resource name")
		return
	}
	var bykey string
	resname := stat.StackName
	id := stat.ServerStat.Id
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.lbs) == 0 {
		return
	}
	if id != "" {
		bykey = id
	} else {
		bykey = resname
	}
	v, ok := p.lbs[bykey]
	if !ok || v.sync == false {
		return
	}
	klog.V(3).Infof("update load balance ResourceStatus:%v", v)
	if v.deleted {
		klog.V(2).Infof("load balance(%v) had been deleted", v.Name)
		stat.ServerStat = vmv1.ServerStat{}
		return
	}
	stat.ServerStat.ResStat = v.Stat
	stat.ServerStat.Ip = v.Ip
	stat.ServerStat.Id = v.Lbid
	stat.ServerStat.ResName = v.Name

	stat.ServerStat.CreateTime = v.Id
}

func (p *LoadBalance) Process(vm *vmv1.VirtualMachine) (reterr error) {
	var (
		spec    = vm.Spec.LoadBalance
		fipSpec = vm.Spec.Public
		stat    = vm.Status.NetStatus
		ips     []string
		fnova   bool
		k8sres  []*manage.Result
	)

	if spec == nil {
		return nil
	}
	//sync floating ip forever, attach fip could be operator in web
	if fipSpec == nil {
		vm.Spec.Public = &vmv1.PublicSepc{}
	}
	if spec.Link == "" {
		fnova = true
	}
	defer func() {
		//Remove stack if pod link not
		if vm.DeletionTimestamp != nil {
			if vm.Status.NetStatus != nil {
				klog.V(2).Infof("remove load balance resource")
				p.mu.Lock()
				delete(p.lbs, vm.Status.NetStatus.Name)
				p.mu.Unlock()
				reterr = p.heat.Process(manage.Lb, vm)
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
		return
	}
	err := validLbSpec(spec)
	if err != nil {
		return err
	}
	if fnova {
		// Try find poolmembers from nova info
		ips = p.nova.GetAllIps(vm)
		if len(ips) == 0 {
			klog.Infof("nova servers not ready, can not fetch members")
			return nil
		}
		sort.Strings(ips)
		klog.V(2).Infof("update server(nova) ip list:%v", ips)
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
		klog.V(4).Infof("find pod ip list:%v", ips)
	}
	for i, _ := range spec.Ports {
		spec.Ports[i].Ips = ips
	}
	var resname string

	if stat == nil || stat.StackName == "" {
		resname = fmt.Sprintf("%s-%s", vm.Name, util.RandStr(6))
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

		sorts sortIps
	)

	// ip in ports is alpha sort
	for _, pm := range spec.LoadBalance.Ports {
		if len(currentips) == 0 {
			for _, addr := range pm.Ips {
				currentips[addr] = struct{}{}
			}
		}
	}
	// fix members
	// if ip-0 in member-1, and member-1 must use ip-0 always.
	template.FindLbMembers(util.Str2bytes(stat.Template), spec.LoadBalance.Name, func(index int, value *gjson.Result) {
		if value.IsObject() {
			ipaddr := value.Get("address").String()
			ipa := net.ParseIP(ipaddr)
			if len(ipa) == 0 {
				klog.Errorf("index %d, address is %s, can not parse", index, ipaddr)
				return
			}
			if _, ok := oldmaps[ipaddr]; ok {
				return
			}
			oldmaps[ipaddr] = struct{}{}

			sorts = append(sorts, &sortIpByIndex{
				idx: index,
				ip:  ipa,
			})
		}
	})
	sort.Sort(sorts)

	// index should start with 0
	// use nil replace when index not found
	// that will keep consistent with last stack template
	i := 0
	for _, v := range sorts {
		for i < v.idx {
			oldips = append(oldips, "")
			i++
		}
		oldips = append(oldips, v.ip.String())
		i++
	}

	if len(oldips) == 0 || len(currentips) == 0 {
		return
	}
	klog.V(2).Info("members old order: ", oldips)
	newips := orderMembers(oldips, currentips)
	klog.V(2).Info("members new order: ", newips)

	// TODO parse old listens, but it is not required.
	// listens can not update now.
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
		if v == "" {
			continue
		}
		srcmap[v] = struct{}{}
	}
	for _, v := range src {
		if _, ok := dst[v]; ok {
			ss = append(ss, v)
		} else {
			ss = append(ss, "")
		}
	}
	for dstk, _ := range dst {
		if _, ok := srcmap[dstk]; !ok {
			klog.V(2).Infof("append ip %v", dstk)
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
