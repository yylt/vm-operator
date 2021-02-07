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
}

func NewFloatip(heat *Heat, mgr *manage.OpenMgr, k8smgr *manage.K8sMgr, Lb *LoadBalance) *Floatip {
	fip := &Floatip{
		mgr:    mgr,
		k8smgr: k8smgr,
		Lb:     Lb,
		heat:   heat,
		portop: newPort(mgr),
		fmu:    sync.RWMutex{},
		caches: make(map[string]*FipResult),
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
	v, ok := p.caches[portid]
	if ok {
		return v.ID
	} else {
		klog.V(3).Infof("add listen floating ip on portid: %v", portid)
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
	stat.ServerStat.ResStat = v.Status
	stat.ServerStat.Id = v.ID
	stat.ServerStat.Ip = v.Ip

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
			klog.V(2).Infof("remove publicip resource")
			if nsname != "" {
				p.portop.rm(nsname)
			}
			if vm.Status.PubStatus != nil {
				p.heat.DeleteStack(vm.Status.PubStatus)
				p.fmu.Lock()
				delete(p.caches, id)
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
		if lbres == nil || lbres.Id == "" || lbres.Ip == "" {
			klog.Infof("not found load balance ip and portid")
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
			reterr = fmt.Errorf("k8s resource not found!")
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
	if spec.Address.Ip != "" {
		spec.FloatIpId = p.listenByPortId(spec.PortId, stat)
		if spec.FloatIpId == "" {
			return fmt.Errorf("not found floating ip by address:%v", spec.Address.Ip)
		}
	}

	err = p.heat.Process(manage.Fip, vm)
	if err != nil {
		return err
	}
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
