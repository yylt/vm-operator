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
	Creating  AssemblyPhaseType = "Creating"
	Updating                    = "Updating"
	Deleting                    = "Deleting"
	Succeeded                   = "Succeeded"
	Failed                      = "Failed"
)

// VirtualMachineSpec defines the desired state of VirtualMachine
type VirtualMachineSpec struct {
	Project        ProjectSpec       `json:"project,omitempty"`
	Server         ServerSpec        `json:"server,omitempty"`
	Network        NetworkSpec       `json:"network,omitempty"`
	Volume         []VolumeSpec      `json:"volume,omitempty"`
	SoftwareConfig []byte            `json:"softwareConfig,omitempty"`
	AssemblyPhase  AssemblyPhaseType `json:"assemblyPhase,omitempty"`
	StackID        string            `json:"stackID,omitempty"`
	HeatEvent      []string          `json:"heatEvent,omitempty"`
}

type ProjectSpec struct {
	ProjectID string `json:"projectID,omitempty"`
}

type ServerSpec struct {
	Replicas       int32  `json:"replicas,omitempty"`
	NamePrefix     string `json:"name_prefix,omitempty"`
	Image          string `json:"image,omitempty"`
	Flavor         string `json:"flavor,omitempty"`
	AvailableZone  string `json:"availability_zone,omitempty"`
	KeyName        string `json:"key_name,omitempty"`
	AdminPass      string `json:"admin_pass,omitempty"`
	BootVolumeType string `json:"boot_volume_type,omitempty"`
	BootVolumeSize string `json:"boot_volume_size,omitempty"`
	SecurityGroup  string `json:"security_group,omitempty"`
}

type NetworkSpec struct {
	ExternalNetwork     string `json:"external_network,omitempty"`
	ExistingNetwork     string `json:"existing_network,omitempty"`
	ExistingSubnet      string `json:"existing_subnet,omitempty"`
	PrivateNetworkCidr  string `json:"private_network_cidr,omitempty"`
	PrivateNetworkName  string `json:"private_network_name,omitempty"`
	NeutronAz           string `json:"neutron_az,omitempty"`
	FloatingIp          string `json:"floating_ip,omitempty"`
	FloatingIpBandwidth string `json:"floating_ip_bandwidth,omitempty"`
}

type VolumeSpec struct {
	VolumeName string `json:"volume_name,omitempty"`
	VolumeType string `json:"volume_type,omitempty"`
	VolumeSize string `json:"volume_size,omitempty"`
}

// VirtualMachineStatus defines the observed state of VirtualMachine
type VirtualMachineStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	VmStatus string `json:"vmStatus,omitempty"`
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
