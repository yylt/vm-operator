package controllers

import (
	"bytes"
	"time"

	"fmt"
	"hash"
	"hash/crc64"
	"reflect"
	"sync"

	vmv1 "easystack.io/vm-operator/pkg/api/v1"

	"github.com/go-logr/logr"
	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/extensions/startstop"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/openstack/orchestration/v1/stacks"
	"github.com/gophercloud/gophercloud/pagination"
)

const (
	ServerRunStat  = "ACTIVE"
	ServerStopStat = "SHUTOFF"
)

var (
	hash64 hash.Hash64
	hashmu sync.Mutex
)

func init() {
	hash64 = crc64.New(crc64.MakeTable(crc64.ECMA))
}

func hashid(bs []byte) uint64 {
	hashmu.Lock()
	defer hashmu.Unlock()
	hash64.Reset()
	hash64.Write(bs)
	return hash64.Sum64()
}

type ServerRst struct {
	Stat       string    `json:"status"`
	Name       string    `json:"name"`
	Id         string    `json:"id"`
	CreateTime time.Time `json:"created"`

	Addresses map[string][]servers.Address `json:"addresses,omitempty"`
}

type CredentialsRst struct {
	Secret string `json:"secret"`
	Name   string `json:"name"`
	Id     string `json:"id"`
}

type StackRst struct {
	Stat   string                   `json:"id"`
	Reason string                   `json:"stack_status"`
	Id     string                   `json:"stack_status_reason"`
	Outpus []map[string]interface{} `json:"outputs,omitempty"`
}

type Auth struct {
	logger   logr.Logger
	endpoint string
	clients  map[uint64]*gophercloud.ProviderClient
	mu       sync.RWMutex
}

func wrapErr(err error) error {
	if _, ok := err.(*gophercloud.ErrDefault409); ok {
		return nil
	}
	return err
}

func (a *Auth) authByToken(id, token string) (*gophercloud.ProviderClient, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	hashid := hashid([]byte(token))
	if v, ok := a.clients[hashid]; ok {
		return v, nil
	}

	opts := gophercloud.AuthOptions{
		IdentityEndpoint: a.endpoint,
		TokenID:          token,
		TenantID:         id,
	}

	cli, err := openstack.AuthenticatedClient(opts)
	if err != nil {
		return nil, err
	}

	a.clients[hashid] = cli
	return cli, nil
}

func (a *Auth) authByCredential(id, name, secret string) (*gophercloud.ProviderClient, error) {
	buf := bufpool.Get().(*bytes.Buffer)
	defer bufpool.Put(buf)
	buf.Reset()
	buf.WriteString(id)
	hashid := hashid(buf.Bytes())

	a.mu.Lock()
	defer a.mu.Unlock()

	if v, ok := a.clients[hashid]; ok {
		return v, nil
	}
	opts := gophercloud.AuthOptions{
		IdentityEndpoint:            a.endpoint,
		ApplicationCredentialID:     id,
		ApplicationCredentialName:   name,
		ApplicationCredentialSecret: secret,
	}

	cli, err := openstack.AuthenticatedClient(opts)
	if err != nil {
		return nil, err
	}
	a.clients[hashid] = cli
	return cli, nil
}

func (a *Auth) ServerGet(auth *vmv1.AuthSpec, id string) (*ServerRst, error) {
	client, err := a.serverClient(auth)
	if err != nil {
		return nil, err
	}
	rst := new(ServerRst)
	result := servers.Get(client, id)
	err = result.ExtractInto(&rst)
	if err != nil {
		return nil, wrapErr(err)
	}
	return rst, nil
}

func (a *Auth) ServerStop(auth *vmv1.AuthSpec, id string) error {
	client, err := a.serverClient(auth)
	if err != nil {
		return err
	}
	err = startstop.Stop(client, id).ExtractErr()
	return wrapErr(err)
}

func (a *Auth) ServerStart(auth *vmv1.AuthSpec, id string) error {
	client, err := a.serverClient(auth)
	if err != nil {
		return err
	}
	err = startstop.Start(client, id).ExtractErr()
	return wrapErr(err)
}

func (a *Auth) ServerList(auth *vmv1.AuthSpec, opts *servers.ListOpts, fn func(rst *ServerRst) bool) error {
	client, err := a.serverClient(auth)
	if err != nil {
		return err
	}
	pages := servers.List(client, opts)
	if pages.Err != nil {
		errtype := fmt.Sprintf("errType: %s", reflect.TypeOf(err).String())
		a.logger.Info(errtype, "server", "list")
		return pages.Err
	}
	var rst []*ServerRst
	return pages.EachPage(func(page pagination.Page) (bool, error) {
		err = servers.ExtractServersInto(page, &rst)
		if err != nil {
			return false, err
		}
		for _, li := range rst {
			if fn(li) == false {
				return false, nil
			}
		}
		return true, nil
	})
}

func (a *Auth) HeatList(auth *vmv1.AuthSpec, opts *stacks.ListOpts, fn func(rst *StackRst) bool) error {
	client, err := a.heatClient(auth)
	if err != nil {
		return err
	}
	pages := stacks.List(client, opts)
	if pages.Err != nil {
		errtype := fmt.Sprintf("errType: %s", reflect.TypeOf(err).String())
		a.logger.Info(errtype, "stack", "list")
		return pages.Err
	}
	var rst = new(StackRst)
	return pages.EachPage(func(page pagination.Page) (bool, error) {
		lists, err := stacks.ExtractStacks(page)
		if err != nil {

			return false, err
		}
		for _, li := range lists {
			rst.Id = li.ID
			rst.Stat = li.Status
			rst.Reason = li.StatusReason
			if fn(rst) == false {
				return false, nil
			}
		}
		return true, nil
	})
}

func (a *Auth) HeatGet(auth *vmv1.AuthSpec, name, id string) (*StackRst, error) {
	client, err := a.heatClient(auth)
	if err != nil {
		return nil, err
	}

	rst := stacks.Get(client, name, id)
	getresult, err := rst.Extract()
	if err != nil {
		errtype := fmt.Sprintf("errType: %s", reflect.TypeOf(err).String())
		a.logger.Info(errtype, "stack", "get")
		return nil, err
	}
	if getresult.StatusReason != "" {
		read := bytes.NewBuffer([]byte(getresult.StatusReason))
		reason, err := read.ReadBytes('\n')
		if err == nil {
			getresult.StatusReason = string(reason)
		}
	}
	result := &StackRst{
		Id:     getresult.ID,
		Stat:   getresult.Status,
		Reason: getresult.StatusReason,
		Outpus: getresult.Outputs,
	}
	a.logger.Info("stack get", "result value", fmt.Sprintf("%v", result))
	return result, nil
}

func (a *Auth) HeatDelete(auth *vmv1.AuthSpec, name, id string) error {
	client, err := a.heatClient(auth)
	if err != nil {
		return err
	}
	rst := stacks.Delete(client, name, id)
	a.logger.V(3).Info(rst.PrettyPrintJSON(), "stack", "delete")
	return rst.ExtractErr()
}

func (a *Auth) HeatUpdate(auth *vmv1.AuthSpec, name, id string, opts *stacks.UpdateOpts) error {
	client, err := a.heatClient(auth)
	if err != nil {
		return err
	}
	rst := stacks.Update(client, name, id, opts)
	err = rst.ExtractErr()
	if err != nil {
		errtype := fmt.Sprintf("errType: %s", reflect.TypeOf(err).String())
		a.logger.Info(errtype, "stack", "update")
	}
	return err
}

func (a *Auth) HeatCreate(auth *vmv1.AuthSpec, ctOpts *stacks.CreateOpts) (id string, err error) {
	client, err := a.heatClient(auth)
	if err != nil {
		return "", err
	}
	rst := stacks.Create(client, ctOpts)
	result, err := rst.Extract()
	if err != nil {
		errtype := fmt.Sprintf("errType: %s", reflect.TypeOf(err).String())
		a.logger.Info(errtype, "stack", "create")
		return "", err
	}
	a.logger.Info(fmt.Sprintf("result : %+v", result), "heat", "create")
	return result.ID, nil
}

func (a *Auth) heatClient(auth *vmv1.AuthSpec) (*gophercloud.ServiceClient, error) {
	provider, err := a.providerClient(auth)
	if err != nil {
		return nil, err
	}
	return openstack.NewOrchestrationV1(provider, gophercloud.EndpointOpts{})
}

func (a *Auth) serverClient(auth *vmv1.AuthSpec) (*gophercloud.ServiceClient, error) {
	provider, err := a.providerClient(auth)
	if err != nil {
		return nil, err
	}
	return openstack.NewComputeV2(provider, gophercloud.EndpointOpts{})
}

func (a *Auth) identityClient(auth *vmv1.AuthSpec) (*gophercloud.ServiceClient, error) {
	provider, err := a.providerClient(auth)
	if err != nil {
		return nil, err
	}
	return openstack.NewIdentityV3(provider, gophercloud.EndpointOpts{})
}

// Get client
func (a *Auth) providerClient(auth *vmv1.AuthSpec) (*gophercloud.ProviderClient, error) {
	var (
		provcli *gophercloud.ProviderClient
		err     error
	)
	if auth.ProjectID != "" && auth.Token != "" {
		provcli, err = a.authByToken(auth.ProjectID, auth.Token)
		if err == nil {
			return provcli, err
		}
	}
	if auth.CredentialID != "" && auth.CredentialName != "" {
		provcli, err = a.authByCredential(auth.CredentialID, auth.CredentialName, auth.CredentialName)
		if err == nil {
			return provcli, err
		}
	}
	return nil, fmt.Errorf("Authentication failed")
}

func NewAuth(auth_url string, logger logr.Logger) *Auth {
	return &Auth{
		logger:   logger,
		endpoint: auth_url,
		clients:  make(map[uint64]*gophercloud.ProviderClient),
	}
}
