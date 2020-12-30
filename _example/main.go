package main

import (

	"fmt"
	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/openstack/orchestration/v1/stacks"
	"github.com/gophercloud/gophercloud/pagination"
	"reflect"
	"time"
)
type ServerRst struct {
	Stat       string    `json:"status"`
	Name       string    `json:"name"`
	Id         string    `json:"id"`
	CreateTime time.Time `json:"created"`

	Addresses map[string][]servers.Address `json:"addresses"`
}
func HeatList(client *gophercloud.ServiceClient, opts *stacks.ListOpts) error {
	pages := stacks.List(client, opts)
	if pages.Err != nil {
		errtype := fmt.Sprintf("errType: %s", reflect.TypeOf(pages.Err).String())
		fmt.Printf("stack list failed, err:%v, type:%s\n",pages.Err,errtype)
		return pages.Err
	}
	fmt.Printf("list stacks")
	return pages.EachPage(func(page pagination.Page) (bool, error) {
		lists, err := stacks.ExtractStacks(page)
		if err != nil {

			return false, err
		}
		for _, li := range lists {
			fmt.Printf("id %s, status %s, Reason %s\n",li.ID,li.Status,li.StatusReason)
		}
		return true, nil
	})
}


func novaList(client *gophercloud.ServiceClient, opts *servers.ListOpts) error {
	pages := servers.List(client, opts)
	if pages.Err != nil {
		errtype := fmt.Sprintf("errType: %s", reflect.TypeOf(pages.Err).String())
		fmt.Printf("nova list failed, err:%v, type:%s\n",pages.Err,errtype)
		return pages.Err
	}
	fmt.Printf("list servers")
	return pages.EachPage(func(page pagination.Page) (bool, error) {
		rst,err := servers.ExtractServers(page)
		if err != nil {
			return false, err
		}
		for _, li := range rst {
			fmt.Printf("id %s, status %s, name %s\n",li.ID,li.Status,li.Name)
		}
		return true, nil
	})
}
func deleteNova(client *gophercloud.ServiceClient, id string) error{
	return servers.Delete(client,id).ExtractErr()
}


/*
export OS_IDENTITY_API_VERSION=3
export OS_DOMAIN_NAME="Default"
export OS_PROJECT_NAME="service"
export OS_USER_DOMAIN_NAME="Default"
export OS_USERNAME="drone"
export OS_PASSWORD="fqcfBmYy"
export OS_AUTH_URL='http://keystone-api.openstack.svc.cluster.local:80/v3'
export OS_REGION_NAME="RegionOne"
 */
func main() {

	opts, err :=openstack.AuthOptionsFromEnv()
	if err!= nil {
		panic(err)
	}

	provider, err := openstack.AuthenticatedClient(opts)
	if err != nil {
		panic(err)
	}

	novac, err := openstack.NewComputeV2(provider, gophercloud.EndpointOpts{})
	if err != nil {
		panic(err)
	}
	fmt.Println(novaList(novac,&servers.ListOpts{AllTenants:true}))
	//fmt.Println(deleteNova(novac,"fee074ee-6d88-4b07-ad47-625d46b9fce6"))
	heatc ,err := openstack.NewOrchestrationV1(provider, gophercloud.EndpointOpts{})
	if err != nil {
		panic(err)
	}
	fmt.Println(HeatList(heatc,&stacks.ListOpts{AllTenants:true}))
}
