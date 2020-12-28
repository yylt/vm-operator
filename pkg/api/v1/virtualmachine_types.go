/*
Copyright 2020 easystack.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type AssemblyPhaseType string

const (
	Creating AssemblyPhaseType = "Creating"
	Updating AssemblyPhaseType = "Updating"
	Deleting AssemblyPhaseType = "Deleting"
	Stop     AssemblyPhaseType = "Stop"
	Start    AssemblyPhaseType = "Start"
	Recreate AssemblyPhaseType = "Recreate"
)

// VirtualMachineSpec defines the desired state of VirtualMachine
type VirtualMachineSpec struct {
	Auth          *AuthSpec         `json:"auth,omitempty"`
	Server        *ServerSpec       `json:"server,omitempty"`
	LoadBalance   *LoadBalanceSpec  `json:"loadbalance,omitempty"`
	AssemblyPhase AssemblyPhaseType `json:"assemblyPhase,omitempty"`
	Public        *PublicSepc       `json:"publicip,omitempty"`
}

type AuthSpec struct {
	ProjectID string `json:"projectID,omitempty"`
	Token     string `json:"token,omitempty"`
}

type PortMap struct {
	Ips      []string `json:"ips,omitempty"`
	Port     int32    `json:"port"`
	Protocol string   `json:"protocol"`
}

type SubnetSpec struct {
	NetworkName string `json:"network_name"`
	NetworkId   string `json:"network_id"`
	SubnetName  string `json:"subnet_name"`
	SubnetId    string `json:"subnet_id"`
}

type VolumeSpec struct {
	VolumeDeleteByVm bool   `json:"volume_delete"`
	VolumeType       string `json:"volume_type"`
	VolumeSize       int32  `json:"volume_size"`
}

type ServerSpec struct {
	Replicas       int32         `json:"replicas"`
	Name           string        `json:"name"`
	BootImage      string        `json:"boot_image,omitempty"`
	BootVolumeId   string        `json:"boot_volume_id,omitempty"`
	Flavor         string        `json:"flavor"`
	KeyName        string        `json:"key_name,omitempty"`
	AdminPass      string        `json:"admin_pass,omitempty"`
	BootVolume     *VolumeSpec   `json:"boot_volume,omitempty"`
	Volumes        []*VolumeSpec `json:"volumes,omitempty"`
	SecurityGroups []string      `json:"security_groups,omitempty"`
	UserData       string        `json:"user_data,omitempty"`

	AvailableZone string      `json:"availability_zone"`
	Subnet        *SubnetSpec `json:"subnet"`
}

type LoadBalanceSpec struct {
	Subnet *SubnetSpec `json:"subnet"`
	Ports  []*PortMap  `json:"port_map,omitempty"`
	Name   string      `json:"name"`
	LbIp   string      `json:"loadbalance_ip,omitempty"`
	Link   string      `json:"link,omitempty"`
}

type PublicSepc struct {
	Mbps    int64       `json:"Mbps,omitempty"`
	Subnet  *SubnetSpec `json:"subnet,omitempty"`
	Address *Address    `json:"address"`

	Link      string `json:"link,omitempty"`
	Name      string `json:"name,omitempty"`
	PortId    string `json:"port_id,omitempty"`
	FixIp     string `json:"fixed_ip,omitempty"`
	FloatIpId string `json:"float_id,omitempty"`
}

type Address struct {
	Allocate bool   `json:"allocate,omitempty"`
	Ip       string `json:"ip,omitempty"`
}

type VirtualMachineStatus struct {
	VmStatus   *ResourceStatus `json:"vmStatus,omitempty"`
	NetStatus  *ResourceStatus `json:"netStatus,omitempty"`
	PubStatus  *ResourceStatus `json:"pubStatus,omitempty"`
	Members    []*ServerStat   `json:"members,omitempty"`
	Conditions []*Condition    `json:"conditions,omitempty"`
}

type Condition struct {
	LastUpdateTime string `json:"lastUpdateTime,omitempty"`
	Type           string `json:"type,omitempty"` //should add Ready type for application
	Reason         string `json:"reason,omitempty"`
}

type ServerStat struct {
	Id         string `json:"id,omitempty"`
	CreateTime string `json:"creationTimestamp,omitempty"`
	ResStat    string `json:"resstat,omitempty"`
	Ip         string `json:"ip,omitempty"`
	ResName    string `json:"resname,omitempty"`
}

type ResourceStatus struct {
	ServerStat ServerStat `json:"serverStat,omitempty"`
	StackID    string     `json:"stackID,omitempty"`
	StackName  string     `json:"stackName,omitempty"`
	HashId     int64      `json:"hashid"`
	Name       string     `json:"name"`
	Stat       string     `json:"phase,omitempty"`
	Template   string     `json:"template,omitempty"`
}

// +kubebuilder:object:root=true
// VirtualMachine is the Schema for the virtualmachines API
type VirtualMachine struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VirtualMachineSpec   `json:"spec,omitempty"`
	Status VirtualMachineStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// VirtualMachineList contains a list of VirtualMachine
type VirtualMachineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VirtualMachine `json:"items"`
}

func init() {
	SchemeBuilder.Register(&VirtualMachine{}, &VirtualMachineList{})
}
