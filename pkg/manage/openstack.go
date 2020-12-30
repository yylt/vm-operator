package manage

import (
	"fmt"
	"sync"
	"time"

	"easystack.io/vm-operator/pkg/util"
	klog "k8s.io/klog/v2"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/openstack/networking/v2/extensions/layer3/floatingips"
	"github.com/gophercloud/gophercloud/openstack/networking/v2/extensions/lbaas_v2/loadbalancers"
	"github.com/gophercloud/gophercloud/openstack/networking/v2/ports"
	"github.com/gophercloud/gophercloud/openstack/orchestration/v1/stacks"
	"github.com/gophercloud/gophercloud/pagination"
)

type Filterfn func(pagination.Page)

type OpResource int

const (
	Lb OpResource = iota
	Heat
	Port
	Vm
	Fip
)

func (or OpResource) String() string {
	switch or {
	case Lb:
		return "lb"
	case Heat:
		return "heat"
	case Port:
		return "port"
	case Vm:
		return "nova"
	case Fip:
		return "fip"
	default:
		return ""
	}
}

func (or OpResource) ListPages(pv *gophercloud.ProviderClient) (pagination.Pager, error) {
	switch or {
	case Lb:
		cli, err := openstack.NewNetworkV2(pv, gophercloud.EndpointOpts{})
		if err != nil {

			return pagination.Pager{}, err
		}
		return loadbalancers.List(cli, loadbalancers.ListOpts{}), nil
	case Heat:
		cli, err := openstack.NewOrchestrationV1(pv, gophercloud.EndpointOpts{})
		if err != nil {

			return pagination.Pager{}, err
		}
		return stacks.List(cli, stacks.ListOpts{AllTenants: true, Tags: util.StackTag}), nil
	case Port:
		cli, err := openstack.NewNetworkV2(pv, gophercloud.EndpointOpts{})
		if err != nil {
			return pagination.Pager{}, err
		}
		return ports.List(cli, ports.ListOpts{}), nil
	case Vm:
		cli, err := openstack.NewComputeV2(pv, gophercloud.EndpointOpts{})
		if err != nil {
			return pagination.Pager{}, err
		}
		return servers.List(cli, servers.ListOpts{AllTenants: true}), nil
	case Fip:
		cli, err := openstack.NewNetworkV2(pv, gophercloud.EndpointOpts{})
		if err != nil {
			return pagination.Pager{}, err
		}
		return floatingips.List(cli, floatingips.ListOpts{}), nil
	default:
		return pagination.Pager{}, fmt.Errorf("The resource not support now")
	}
}

type OpenMgr struct {
	provider *gophercloud.ProviderClient

	stopch chan struct{}
	mu     sync.RWMutex
	fns    map[OpResource]Filterfn
}

func NewOpMgr() *OpenMgr {
	om := &OpenMgr{
		provider: mustProviderClient(),
		stopch:   make(chan struct{}),
		fns:      make(map[OpResource]Filterfn),
	}
	return om
}

// should call once, fn should not too many!
func (om *OpenMgr) Regist(k OpResource, fn Filterfn) {
	om.mu.Lock()
	defer om.mu.Unlock()
	if fn == nil {
		return
	}
	_, ok := om.fns[k]
	klog.V(2).Infof("add %s filter function", k.String())
	if ok {
		klog.V(2).Infof("%s function exist, now will update!", k.String())
	}
	om.fns[k] = fn
	return
}

func (om *OpenMgr) Stop() {
	close(om.stopch)
}

func (om *OpenMgr) Run(du time.Duration) {
	go om.Loop(du)
}

func (om *OpenMgr) WrapClient(fn func(client *gophercloud.ProviderClient)) {
	fn(om.provider)
}

func (om *OpenMgr) Loop(du time.Duration) {
	var (
		wg  sync.WaitGroup
		err error
	)
	for {
		select {
		case <-om.stopch:
			klog.Infof("receive stop signal")
			return
		case <-time.NewTimer(du).C:
			klog.V(3).Infof("start fetch openstack resource at %v", time.Now().Format(time.RFC3339))
			for k, fn := range om.fns {
				var (
					tmpk  = k
					tmpfn = fn
				)
				if tmpfn == nil {
					klog.V(2).Infof("not found %s callback function", k.String())
					continue
				}
				wg.Add(1)
				err = util.Submit(func() {
					defer wg.Done()
					pages, err := tmpk.ListPages(om.provider)
					if err != nil {
						klog.Errorf("list %s page failed:%v", tmpk.String(), err)
						return
					}
					allpage, err := pages.AllPages()
					if err != nil {
						klog.Errorf("page %s list failed:%v", tmpk.String(), err)
						return
					}
					klog.V(4).Infof("start %s callback", tmpk.String())
					tmpfn(allpage)
					return
				})
				if err != nil {
					klog.Errorf("submit task fail:%v", err)
				}
			}
			wg.Wait()
			klog.V(3).Infof("end fetch openstack resource at %v", time.Now().Format(time.RFC3339))
		}
	}
}

// Must get provider client
func mustProviderClient() *gophercloud.ProviderClient {
	opt, err := openstack.AuthOptionsFromEnv()
	if err != nil {
		panic(err)
	}
	provider, err := openstack.AuthenticatedClient(opt)
	if err != nil {
		panic(err)
	}

	provider.ReauthFunc = func() error {
		opt, err := openstack.AuthOptionsFromEnv()
		if err != nil {
			return err
		}
		newprov, err := openstack.AuthenticatedClient(opt)
		if err != nil {
			return err
		}
		provider.CopyTokenFrom(newprov)
		return nil
	}
	return provider
}
