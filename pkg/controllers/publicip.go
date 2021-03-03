package controllers

import (
	"fmt"
	"sync"

	vmv1 "easystack.io/vm-operator/pkg/api/v1"
	"easystack.io/vm-operator/pkg/manage"
	"easystack.io/vm-operator/pkg/util"

	"github.com/gophercloud/gophercloud/openstack/networking/v2/extensions/layer3/floatingips"
	"github.com/gophercloud/gophercloud/pagination"
	klog "k8s.io/klog/v2"
)

type FipResult struct {
	ID     string
	Status string
	Ip     string
	PortId string

	//unbind operat by user from openstack api
	unbind bool
	//had sync or not
	sync bool
}

func (s *FipResult) DeepCopyFrom(ls *floatingips.FloatingIP) {
	s.ID = ls.ID
	s.Status = ls.Status
	s.Ip = ls.FloatingIP
	s.PortId = ls.PortID
}

func (s *FipResult) DeepCopy() *FipResult {
	tmp := new(FipResult)
	tmp.ID = s.ID
	tmp.Status = s.Status
	tmp.Ip = s.Ip
	tmp.PortId = s.PortId
	return tmp
}

// Equal floatingip resource on openstack
type Floatip struct {
	mgr    *manage.OpenMgr
	k8smgr *manage.K8sMgr
	Lb     *LoadBalance
	heat   *Heat
	portop *port

	fmu sync.RWMutex

	// create floating ip
	//key: portID,
	//value: FipRessult
	caches map[string]*FipResult

	// should find ip -> id when create association
	// key: floating ip
	// value: fip id
	statics map[string]string
}

func NewFloatip(heat *Heat, mgr *manage.OpenMgr, k8smgr *manage.K8sMgr, Lb *LoadBalance) *Floatip {
	fip := &Floatip{
		mgr:     mgr,
		k8smgr:  k8smgr,
		Lb:      Lb,
		heat:    heat,
		portop:  newPort(mgr),
		fmu:     sync.RWMutex{},
		caches:  make(map[string]*FipResult),
		statics: make(map[string]string),
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
	p.fmu.Lock()
	defer p.fmu.Unlock()
	exists := make(map[string]struct{}, len(p.caches))
	for _, fip := range lists {
		v, ok := p.caches[fip.PortID]
		if ok {
			klog.V(3).Infof("callback update floating ip stat: %v", fip)
			v.DeepCopyFrom(&fip)
			exists[fip.PortID] = struct{}{}
			v.unbind = false
		}
		_, ok = p.statics[fip.FloatingIP]
		if ok {
			p.statics[fip.FloatingIP] = fip.ID
		}
	}
	for k, v := range p.caches {
		v.sync = true
		if _, ok := exists[k]; !ok {
			v.unbind = true
		}
	}
	return
}

// try get floating ip id
// and set listen on statics cache
func (p *Floatip) findFloatingId(ip string) string {
	p.fmu.RLock()
	defer p.fmu.RUnlock()
	v, ok := p.statics[ip]
	if ok {
		return v
	} else {
		klog.V(2).Infof("add listen floating ip(%s) to find id", ip)
		p.statics[ip] = ""
	}
	return ""
}

// add listen by portid
func (p *Floatip) listenByPortId(portid string, stat *vmv1.ResourceStatus) string {
	var fipres *FipResult
	if stat != nil {
		fipres = &FipResult{
			ID:     stat.ServerStat.Id,
			Status: stat.ServerStat.ResStat,
			Ip:     stat.ServerStat.Ip,
		}
	} else {
		fipres = new(FipResult)
	}
	p.fmu.RLock()
	defer p.fmu.RUnlock()
	_, ok := p.caches[portid]
	if !ok {
		klog.V(2).Infof("listen floating ip on portid: %v", portid)
		p.caches[portid] = fipres
	}
	return ""
}

// update stat resStat from cache
func (p *Floatip) update(spec *vmv1.PublicSepc, stat *vmv1.ResourceStatus) {
	var (
		v  *FipResult
		ok bool
	)
	p.fmu.RLock()
	defer p.fmu.RUnlock()
	if len(p.caches) == 0 {
		return
	}
	v, ok = p.caches[spec.PortId]
	if !ok || v.sync == false {
		return
	}
	klog.V(3).Infof("update floating ip ResourceStatus %v", v)

	if v.unbind {
		stat.ServerStat = vmv1.ServerStat{}
		klog.V(2).Infof("portid(%v) had unbinded frpm floating ip!", spec.PortId)
		return
	}
	stat.ServerStat.Ip = v.Ip
	stat.ServerStat.ResStat = v.Status
	stat.ServerStat.Id = v.ID
}

func (p *Floatip) Process(vm *vmv1.VirtualMachine) (reterr error) {
	var (
		spec                     = vm.Spec.Public
		stat                     = vm.Status.PubStatus
		nsname, ip, id           string
		removeRes, justCheckSync bool
		k8res                    = &manage.Resource{}
	)

	if spec == nil {
		return nil
	}

	defer func() {
		//Remove stack if pod link not exist
		if removeRes {
			if nsname != "" {
				klog.V(2).Infof("remove port for k8s resource")
				p.portop.rm(nsname)
			}
			if vm.Status.PubStatus != nil {
				klog.V(2).Infof("remove publicip resource")
				p.fmu.Lock()
				delete(p.caches, id)
				p.fmu.Unlock()
				reterr = p.heat.Process(manage.Fip, vm)
			}
		} else {
			if reterr == nil && id != "" {
				p.update(spec, vm.Status.PubStatus)
			}
		}
	}()
	if spec.Link != "" {
		err := manage.ParseLink(spec.Link, k8res)
		if err != nil {
			return err
		}
		if !k8res.IsResource(manage.Pod) && vm.DeletionTimestamp == nil {
			return fmt.Errorf("floating Ip only support pod link!")
		} else {
			nsname = k8res.NamespaceName()
		}
	}
	if vm.DeletionTimestamp != nil {
		removeRes = true
		return
	}
	err := validPip(spec)
	if err != nil {
		return err
	}

	if spec.Link == "" {
		// Try find {portId,fixip} from loadbalance info
		lbres := p.Lb.GetResource(vm)
		//TODO CreateTime is portId!!!
		if lbres == nil || lbres.CreateTime == "" || lbres.Ip == "" {
			klog.Infof("not found load balance ip and portid")
			return nil
		}
		klog.V(3).Infof("fetch load balance ip:%s,id:%v", lbres.Ip, lbres.CreateTime)
		ip = lbres.Ip
		id = lbres.CreateTime
	} else {
		// Try find {portId,fixip} from pod link
		ok, err := p.k8smgr.IsExist(k8res)
		if err != nil {
			return err
		}
		if !ok {
			reterr = fmt.Errorf("pod %s not found", nsname)
			return
		}
		portres := p.portop.ListenByName(nsname)
		if portres.Ipv4 != "" && portres.ID != "" {
			ip = portres.Ipv4
			id = portres.ID
		}
		klog.V(3).Infof("fetch pod ip:%s,id:%v", ip, id)
	}
	if ip == "" || id == "" {
		reterr = fmt.Errorf("not found port-fixip and port-id")
		return
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

	spec.Name = genname
	spec.FixIp = ip
	spec.PortId = id
	spec.Mbps = spec.Mbps * 1024
	//if address.ip is not, means create FloatingIPAssociation resource
	// 1. floating ip id (which only need when create FloatingIPAssociation)
	// 2. port id
	// 3. lb ip (fix address ip)
	p.listenByPortId(spec.PortId, stat)
	if spec.Address == nil {
		justCheckSync = true
	}
	if spec.Address.Ip == "" && spec.Address.Allocate == false {
		justCheckSync = true
	}
	if justCheckSync {
		klog.V(2).Infof("floating ip bind will sync: %v", !spec.NonSync)
		if spec.NonSync == true {
			reterr = fmt.Errorf("floating ip not need sync")
			return reterr
		}
		return nil
	}
	klog.V(2).Infof("floating ip bind static ip:%v, allocate:%v", spec.Address.Ip, spec.Address.Allocate)
	if spec.Address.Ip != "" {
		spec.FloatIpId = p.findFloatingId(spec.Address.Ip)
		if spec.FloatIpId == "" {
			return fmt.Errorf("not found floating ip by address:%v", spec.Address.Ip)
		}
	}

	return p.heat.Process(manage.Fip, vm)
}

func (p *Floatip) Stat(vm *vmv1.VirtualMachine) *vmv1.ResourceStatus {
	if vm == nil {
		return nil
	}
	return vm.Status.PubStatus
}

func validPip(pi *vmv1.PublicSepc) error {
	if pi.Address != nil {
		if pi.Address.Allocate == true && pi.Address.Ip != "" {
			return fmt.Errorf("address allocate and ip must can not both setted.")
		}
	}

	pi.Name = ""
	pi.PortId = ""
	pi.FixIp = ""
	return nil
}
