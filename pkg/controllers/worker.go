package controllers

import (
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/openstack/orchestration/v1/stacks"
	"github.com/gophercloud/gophercloud/pagination"
)

type ServerItem struct {
	Stat       string
	Name       string
	Id         string
	CreateTime time.Time

	Ip4addr string
}

// If ServerName is not null, it is nova stack else lb stack
type WorkItem struct {
	PrjtID     string
	StackId    string
	StackName  string
	ServerName string

	mu sync.RWMutex

	//Server Id as key
	servers map[string]*ServerItem
	stack   *StackRst
}

func NewWorkItem(prjid, stackid, stackname, serverName string) *WorkItem {
	return &WorkItem{
		PrjtID:     prjid,
		StackId:    stackid,
		ServerName: serverName,
		StackName:  stackname,
		mu:         sync.RWMutex{},
		servers:    make(map[string]*ServerItem),
	}
}

func (i *WorkItem) Deepcopy(rst stacks.ListedStack) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.stack == nil {
		i.stack = &StackRst{}
	}
	i.stack.Id = rst.ID
	i.stack.Reason = rst.StatusReason
	i.stack.Stat = rst.Status
}

func (i *WorkItem) DeepcopyServer(s *ServerRst) {
	var (
		serv *ServerItem
		ok   bool
	)
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.servers == nil {
		i.servers = make(map[string]*ServerItem)
	}
	serv, ok = i.servers[s.Id]
	if !ok {
		serv = new(ServerItem)
		i.servers[s.Id] = serv
	}
	serv.Stat = s.Stat
	serv.Name = s.Name
	serv.CreateTime = s.CreateTime
	serv.Id = s.Id
Done:
	for _, addrs := range s.Addresses {
		for _, val := range addrs {
			if val.Version == 4 {
				serv.Ip4addr = val.Address
				break Done
			}
		}
	}
}

func (i *WorkItem) Get(fn func(novas map[string]*ServerItem, st *StackRst)) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	fn(i.servers, i.stack)
}

// TODO(y) search resources by AllTenants ?
type WorkManager struct {
	provider *gophercloud.ProviderClient

	du     time.Duration
	stopch chan struct{}
	mu     sync.RWMutex

	log   logr.Logger
	items map[string]*WorkItem
}

func NewWorkers(du time.Duration, logger logr.Logger) *WorkManager {
	wm := &WorkManager{
		provider: MustProviderClient(),
		du:       du,
		mu:       sync.RWMutex{},
		stopch:   make(chan struct{}),
		items:    make(map[string]*WorkItem),
		log:      logger,
	}

	return wm
}

func (wm *WorkManager) GetItem(id string) *WorkItem {
	wm.mu.RLock()
	defer wm.mu.RUnlock()
	return wm.items[id]
}

func (wm *WorkManager) Stop() {
	close(wm.stopch)
}

func (wm *WorkManager) DelItem(id string) error {
	item := wm.GetItem(id)
	if item == nil {
		return fmt.Errorf("Not found id:%s item", id)
	}
	heatc, err := openstack.NewOrchestrationV1(wm.provider, gophercloud.EndpointOpts{})
	if err != nil {
		return err
	}
	//TODO(y) delete server?
	rst := stacks.Delete(heatc, item.StackName, id)
	return rst.ExtractErr()
}

func (wm *WorkManager) SetItem(it *WorkItem) error {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	if it == nil {
		return fmt.Errorf("Item is nil")
	}
	_, ok := wm.items[it.StackId]
	if ok {
		return fmt.Errorf("Aleardy set item %s", it.StackId)
	}
	wm.items[it.StackId] = it
	return nil
}

func (wm *WorkManager) Run() {
	go func() {
		var (
			serverNames = make(map[string]*WorkItem)
			rst         []*ServerRst
		)
		for {
			select {
			case <-wm.stopch:
				wm.log.WithValues("model", "wm").Info("stop receive!")
				return
			case <-time.NewTimer(wm.du).C:
				heatc, err := openstack.NewOrchestrationV1(wm.provider, gophercloud.EndpointOpts{})
				if err != nil {
					wm.log.WithValues("model", "wm").Info("heat client failed", "err", err.Error())
					continue
				}
				novac, err := openstack.NewComputeV2(wm.provider, gophercloud.EndpointOpts{})
				if err != nil {
					wm.log.WithValues("model", "wm").Info("nova client failed", "err", err.Error())
					continue
				}
				novas := servers.List(novac, servers.ListOpts{AllTenants: true})
				//TODO tag should add ?
				pages := stacks.List(heatc, stacks.ListOpts{AllTenants: true, Tags: StackTag})

				wm.mu.RLock()
				err = pages.EachPage(func(page pagination.Page) (b bool, err error) {
					lists, err := stacks.ExtractStacks(page)
					if err != nil {
						return false, err
					}
					for index, li := range lists {
						item, ok := wm.items[li.ID]
						if ok {
							wm.log.V(2).Info(fmt.Sprintf("stack %d ,value:%v", index, li))
							if item.ServerName != "" {
								serverNames[item.ServerName] = item
							}
							item.Deepcopy(li)
						}
					}
					return true, nil
				})
				wm.mu.RUnlock()
				if err != nil {
					wm.log.WithValues("model", "wm").Info("heat extract failed", "err", err.Error())
				}
				err = novas.EachPage(func(page pagination.Page) (bool, error) {
					err = servers.ExtractServersInto(page, &rst)
					if err != nil {
						return false, err
					}
					if len(rst) == 0 {
						return true, nil
					}
					for index, serv := range rst {
						if item, ok := serverNames[serv.Name]; ok {
							wm.log.V(2).Info(fmt.Sprintf("server %d ,value:%v", index, serv))
							item.DeepcopyServer(serv)
						}
					}
					return true, nil
				})
				if err != nil {
					wm.log.WithValues("model", "wm").Info("server extract failed", "err", err.Error())
				}

			}
		}
	}()
}

// Must get provider client
func MustProviderClient() *gophercloud.ProviderClient {
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
