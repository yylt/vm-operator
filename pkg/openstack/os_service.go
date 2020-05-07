package openstack

import (
	"context"
	"fmt"
	"sync"

	"easystack.io/vm-operator/pkg/utils"
	"github.com/go-logr/logr"
	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/orchestration/v1/stacks"
)

const (
	CLOUDADMIN = "drone"
	StackTag   = "ecns-mixapp"
)

const (
	S_CREATE_FAILED      = "CREATE_FAILED"
	S_CREATE_IN_PROGRESS = "CREATE_IN_PROGRESS"
	S_CREATE_COMPLETE    = "CREATE_IN_PROGRESS"
	S_UPDATE_FAILED      = "UPDATE_FAILED"
	S_UPDATE_IN_PROGRESS = "UPDATE_IN_PROGRESS"
	S_UPDATE_COMPLETE    = "UPDATE_IN_PROGRESS"
)

type OSService struct {
	AdminAuthOpt *gophercloud.AuthOptions
	ClientCache  *ClientCache
	ConfigDir    string
}

// use projectID as key
type ClientCache struct {
	mu             sync.Mutex
	clientMap      map[string]*gophercloud.ServiceClient
	userCredential map[string]*UserCredential
}

type UserCredential struct {
	ApplicationCredentialID     string
	ApplicationCredentialSecret string
}

func NewOSService(configDir string, logger logr.Logger) (*OSService, error) {
	// get ECS cloud admin credential info from env
	adminAuthOpt, err := openstack.AuthOptionsFromEnv()
	if err != nil {
		fmt.Print(err)
		return nil, err
	}

	provider, err := openstack.AuthenticatedClient(adminAuthOpt)
	if err != nil {
		logger.Error(err, "Failed to Authenticate to OpenStack")
		return nil, err
	}

	client, err := openstack.NewOrchestrationV1(provider, gophercloud.EndpointOpts{Region: "RegionOne"})
	if err != nil {
		logger.Error(err, "Failed to init heat client")
		return nil, err
	}

	// It's safe for first insert
	cc := new(ClientCache)
	cc.clientMap = make(map[string]*gophercloud.ServiceClient)
	cc.userCredential = make(map[string]*UserCredential)
	cc.clientMap[CLOUDADMIN] = client
	// TODO: init cache for project credential by 'openstack application credential list'

	return &OSService{
		AdminAuthOpt: &adminAuthOpt,
		ClientCache:  cc,
		ConfigDir:    configDir,
	}, nil
}

func (oss *OSService) NewHeatClient(ctx context.Context, projectID string) error {
	logger := utils.GetLoggerOrDie(ctx)

	var client *gophercloud.ServiceClient

	if projectID == CLOUDADMIN {
		provider, err := openstack.AuthenticatedClient(*oss.AdminAuthOpt)
		if err != nil {
			logger.Error(err, "Failed to Authenticate to OpenStack")
			return err
		}

		client, err = openstack.NewOrchestrationV1(provider, gophercloud.EndpointOpts{Region: "RegionOne"})
		if err != nil {
			logger.Error(err, "Failed to init heat client")
			return err
		}
	}

	oss.ClientCache.clientMap[projectID] = client

	return nil
}

func (oss *OSService) GetHeatClient(ctx context.Context, projectID string) (*gophercloud.ServiceClient, error) {
	defer oss.ClientCache.mu.Unlock()
	oss.ClientCache.mu.Lock()

	if client, ok := oss.ClientCache.clientMap[projectID]; ok {
		return client, nil
	}

	err := oss.NewHeatClient(ctx, projectID)
	if err != nil {
		return nil, err
	}

	return oss.ClientCache.clientMap[projectID], nil
}

func (oss *OSService) StackListAll(ctx context.Context) ([]stacks.ListedStack, error) {
	client, err := oss.GetHeatClient(ctx, CLOUDADMIN)
	if err != nil {
		fmt.Printf("Failed to get heat client for %s\n", CLOUDADMIN)
		return nil, err
	}

	listOpts := stacks.ListOpts{AllTenants: true, Tags: StackTag}

	return doStackList(client, listOpts)
}

func doStackList(client *gophercloud.ServiceClient, listOpts stacks.ListOpts) ([]stacks.ListedStack, error) {
	allStackPages, err := stacks.List(client, listOpts).AllPages()
	if err != nil {
		return nil, err
	}

	stackList, err := stacks.ExtractStacks(allStackPages)
	if err != nil {
		return nil, err
	}

	for _, stack := range stackList {
		fmt.Printf("%+v\n", stack)
	}

	return stackList, nil
}

func (oss *OSService) StackCreate(ctx context.Context, createOpts *stacks.CreateOpts) (string, error) {
	// cloudadmin is just for testing
	// TODO: use credential of request project to identify
	// if no credential cache found, create one for request project
	client, err := oss.GetHeatClient(ctx, CLOUDADMIN)
	if err != nil {
		fmt.Printf("Failed to get heat client for %s\n", CLOUDADMIN)
		return "", err
	}

	r := stacks.Create(client, createOpts)
	if r.Err != nil {
		fmt.Printf("Create stack failed with err: %v\n", r.Err)
		return "", err
	}

	createdStack, err := r.Extract()
	if err != nil {
		fmt.Printf("failed to extract stack info\n")
		return "", err
	}
	fmt.Printf("Created Stack: %v", createdStack.ID)

	return createdStack.ID, nil
}
