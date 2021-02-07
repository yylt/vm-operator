package controllers

import (
	"easystack.io/vm-operator/pkg/manage"
	"github.com/gophercloud/gophercloud/openstack/networking/v2/ports"
	"github.com/gophercloud/gophercloud/pagination"
	"k8s.io/klog/v2"
	"sync"
)

type portResult struct {
	ID     string
	Status string
	Ipv4   string
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
			klog.V(3).Infof("callback update port stat:%v", port)
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

func (p *port) ListenByName(nsname string) *portResult {
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
