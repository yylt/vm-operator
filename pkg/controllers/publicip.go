package controllers

import (
	"fmt"
	"sync"

	vmv1 "easystack.io/vm-operator/pkg/api/v1"
	"easystack.io/vm-operator/pkg/manage"
	"easystack.io/vm-operator/pkg/util"

	"github.com/gophercloud/gophercloud/openstack/networking/v2/extensions/layer3/floatingips"
	"github.com/gophercloud/gophercloud/openstack/networking/v2/ports"
	"github.com/gophercloud/gophercloud/pagination"
	klog "k8s.io/klog/v2"
)

type portResult struct {
	ID     string
	Status string
	Ipv4   string
}

type FipResult struct {
	ID     string
	Status string
	Ip     string
	Name   string
}

func (s *FipResult) DeepCopyFrom(ls *floatingips.FloatingIP) {
	s.ID = ls.ID
	s.Status = ls.Status
	s.Ip = ls.FloatingIP
}

func (s *FipResult) DeepCopy() *FipResult {
	tmp := new(FipResult)
	tmp.ID = s.ID
	tmp.Name = s.Name
	tmp.Status = s.Status
	tmp.Ip = s.Ip
	return tmp
}

func (s *portResult) DeepCopyFrom(ls *ports.Port) {
	s.ID = ls.ID
	s.Status = ls.Status
	if len(ls.FixedIPs) == 0 {
		return
	} else {
		s.Ipv4 = ls.FixedIPs[0].IPAddress
	}
}

func (s *portResult) DeepCopy() *portResult {
	tmp := new(portResult)
	tmp.ID = s.ID
	tmp.Status = s.Status
	tmp.Ipv4 = s.Ipv4
	return tmp
}

type port struct {
	mgr    *manage.OpenMgr
	k8smgr *manage.K8sMgr
	mu     sync.RWMutex
	// key: ns/name which is pod,
	// value: Port
	ports map[string]*portResult
}

func (p *port) addPortStore(page pagination.Page) {
	lists, err := ports.ExtractPorts(page)
	if err != nil {
		klog.Errorf("ports extract page failed:%v", err)
		return
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, port := range lists {
		v, ok := p.ports[port.Name]
		if ok {
			klog.V(2).Infof("callback update port stat:%v", port)
			v.DeepCopyFrom(&port)
		}
	}
	return
}

func newPort(mgr *manage.OpenMgr) *port {
	p := &port{
		mgr:   mgr,
		mu:    sync.RWMutex{},
		ports: make(map[string]*portResult),
	}
	mgr.Regist(manage.Port, p.addPortStore)
	return p
}

func (p *port) addPort(nsname string) *portResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.ports[nsname]
	if !ok {
		klog.V(4).Infof("add listen port by name: %v", nsname)
		p.ports[nsname] = &portResult{}
	}
	return p.ports[nsname].DeepCopy()
}

func (p *port) rm(nsname string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.ports, nsname)
}

// Equal floatingip resource on openstack
type Floatip struct {
	mgr    *manage.OpenMgr
	k8smgr *manage.K8sMgr
	Lb     *LoadBalance
	heat   *Heat
	portop *port

	fmu sync.RWMutex
	//key: portID, because name ont included in floatingip data struct
	//value: FipRessult
	fips map[string]*FipResult

	//key: floating ip , if use static ip
	//value: FipRessult
	statics map[string]*FipResult
}

func NewFloatip(heat *Heat, mgr *manage.OpenMgr, k8smgr *manage.K8sMgr, Lb *LoadBalance) *Floatip {
	fip := &Floatip{
		mgr:     mgr,
		k8smgr:  k8smgr,
		Lb:      Lb,
		heat:    heat,
		portop:  newPort(mgr),
		fmu:     sync.RWMutex{},
		fips:    make(map[string]*FipResult),
		statics: make(map[string]*FipResult),
	}
	mgr.Regist(manage.Fip, fip.addFipStore)
	return fip
}

func (p *Floatip) addFipStore(pages pagination.Page) {

	lists, err := floatingips.ExtractFloatingIPs(pages)
	if err != nil {
		klog.Errorf("floatingips extract page failed:%v", err)
		return
	}
	p.fmu.RLock()
	defer p.fmu.RUnlock()
	for _, fip := range lists {
		v, ok := p.fips[fip.PortID]
		if ok {
			klog.V(3).Infof("callback update floating ip stat: %v", fip)
			v.DeepCopyFrom(&fip)
		}
		staticv, ok := p.statics[fip.FloatingIP]
		if ok {
			klog.V(3).Infof("callback update  static floating ip stat: %v", fip)
			staticv.DeepCopyFrom(&fip)
		}
	}
	return
}

func (p *Floatip) addFip(spec *vmv1.PublicSepc, stat *vmv1.ResourceStatus) {
	if spec == nil || spec.PortId == "" {
		return
	}
	var fipres *FipResult
	if stat != nil {
		fipres = &FipResult{
			ID:     stat.ServerStat.Id,
			Status: stat.ServerStat.ResStat,
			Ip:     stat.ServerStat.Ip,
			Name:   stat.ServerStat.ResName,
		}
	} else {
		fipres = new(FipResult)
	}
	p.fmu.Lock()
	defer p.fmu.Unlock()
	//TODO much floating ip use same portid?
	// should add fixip as another key?
	_, ok := p.fips[spec.PortId]
	if !ok {
		klog.V(2).Infof("add listen floating ip by portId: %v", spec.PortId)
		p.fips[spec.PortId] = fipres
	}
	return
}

func (p *Floatip) updateStaticId(ip string, stat *vmv1.ResourceStatus) string {
	var fipres *FipResult
	if stat != nil {
		fipres = &FipResult{
			ID:     stat.ServerStat.Id,
			Status: stat.ServerStat.ResStat,
			Ip:     stat.ServerStat.Ip,
			Name:   stat.ServerStat.ResName,
		}
	} else {
		fipres = new(FipResult)
	}
	p.fmu.RLock()
	defer p.fmu.RUnlock()
	v, ok := p.statics[ip]
	if ok {
		return v.ID
	} else {
		klog.V(2).Infof("add listen floating ip by floatip: %v", ip)
		p.statics[ip] = fipres
	}
	return ""
}

func (p *Floatip) update(spec *vmv1.PublicSepc, stat *vmv1.ResourceStatus) {
	var (
		v  *FipResult
		ok bool
	)
	p.fmu.RLock()
	defer p.fmu.RUnlock()
	if spec.Address.Ip != "" {
		v, ok = p.statics[spec.Address.Ip]
		if !ok {
			return
		}
	} else {
		v, ok = p.fips[spec.PortId]
		if !ok {
			return
		}
	}

	klog.V(3).Infof("update floating ip ResourceStatus %v", v)
	stat.ServerStat.ResStat = v.Status
	stat.ServerStat.Ip = v.Ip
	stat.ServerStat.Id = v.ID
	stat.ServerStat.ResName = v.Name
}

func (p *Floatip) Process(vm *vmv1.VirtualMachine) (reterr error) {
	var (
		spec           = vm.Spec.Public
		stat           = vm.Status.PubStatus
		nsname, ip, id string
		removeRes      bool
	)

	if spec == nil {
		return nil
	}
	err := validPip(spec)
	if err != nil {
		return err
	}

	defer func() {
		//Remove stack if pod link not exist
		if removeRes {
			klog.V(2).Infof("remove loadbalance resource")
			if nsname != "" {
				p.portop.rm(nsname)
			}
			if vm.Status.PubStatus != nil {
				p.heat.DeleteStack(vm.Status.PubStatus)
				p.fmu.Lock()
				delete(p.fips, id)
				p.fmu.Unlock()
			}
		} else {
			if reterr == nil && id != "" {
				p.update(spec, vm.Status.PubStatus)
			}
		}
	}()
	if vm.DeletionTimestamp != nil {
		removeRes = true
		return nil
	}
	if spec.Link == "" {
		// Try find {portId,fixip} from loadbalance info
		lbres := p.Lb.GetResource(vm)
		if lbres==nil || lbres.Id == "" || lbres.Ip == "" {
			klog.Infof("load balance not ready, can not fetch lb ip, id")
			return nil
		}
		klog.V(3).Infof("fetch load balance ip:%s,id:%v", lbres.Ip, lbres.Id)
		ip = lbres.Ip
		id = lbres.Id
	} else {
		// Try find {portId,fixip} from pod link
		k8res := &manage.Resource{}
		err = manage.ParseLink(spec.Link, k8res)
		if err != nil {
			return err
		}
		if !k8res.IsResource(manage.Pod) {
			return fmt.Errorf("floating Ip only support pod link!")
		}
		nsname = k8res.NamespaceName()
		ok, err := p.k8smgr.IsExist(k8res)
		if err != nil {
			return err
		}
		if !ok {
			// should remove Floatip if exist when k8s resource not found
			removeRes = true
			return fmt.Errorf("k8s resource not found!")
		}
		portres := p.portop.addPort(nsname)
		if portres.Ipv4 != "" && portres.ID != "" {
			ip = portres.Ipv4
			id = portres.ID
		}
		klog.V(3).Infof("fetch pod ip:%s,id:%v", ip, id)
	}
	if ip == "" || id == "" {
		return fmt.Errorf("not found port-fixip and port-id")
	}

	var genname string
	if stat == nil || stat.StackName == "" {
		genname = fmt.Sprintf("%s-%s", manage.Fip.String(), util.RandStr(5))
		vm.Status.PubStatus = &vmv1.ResourceStatus{
			StackName: genname,
		}
	} else {
		genname = stat.StackName
	}
	if spec.Address.Ip != "" {
		spec.FloatIpId = p.updateStaticId(spec.Address.Ip, stat)
		if spec.FloatIpId == "" {
			return fmt.Errorf("not found floating ip by address:%v", spec.Address.Ip)
		}
	}
	spec.Name = genname
	spec.FixIp = ip
	spec.PortId = id
	spec.Mbps = spec.Mbps * 1024

	err = p.heat.Process(manage.Fip, vm)
	if err != nil {
		return err
	}
	p.addFip(spec, stat)
	return nil
}

func (p *Floatip) Stat(vm *vmv1.VirtualMachine) *vmv1.ResourceStatus {
	if vm == nil {
		return nil
	}
	return vm.Status.PubStatus
}

func validPip(pi *vmv1.PublicSepc) error {
	if pi.Address == nil {
		return fmt.Errorf("not found address")
	}
	if pi.Address.Allocate == false && pi.Address.Ip == "" {
		return fmt.Errorf("address not define")
	}
	if pi.Address.Allocate == true && pi.Address.Ip != "" {
		return fmt.Errorf("address must set one about allocate and ip ")
	}

	pi.Name = ""
	pi.PortId = ""
	pi.FixIp = ""
	return nil
}
