// +build !ignore_autogenerated

// Code generated by controller-gen. DO NOT EDIT.

package v1

import (
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Address) DeepCopyInto(out *Address) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Address.
func (in *Address) DeepCopy() *Address {
	if in == nil {
		return nil
	}
	out := new(Address)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AuthSpec) DeepCopyInto(out *AuthSpec) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AuthSpec.
func (in *AuthSpec) DeepCopy() *AuthSpec {
	if in == nil {
		return nil
	}
	out := new(AuthSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Condition) DeepCopyInto(out *Condition) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Condition.
func (in *Condition) DeepCopy() *Condition {
	if in == nil {
		return nil
	}
	out := new(Condition)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LoadBalanceSpec) DeepCopyInto(out *LoadBalanceSpec) {
	*out = *in
	if in.Subnet != nil {
		in, out := &in.Subnet, &out.Subnet
		*out = new(SubnetSpec)
		**out = **in
	}
	if in.Ports != nil {
		in, out := &in.Ports, &out.Ports
		*out = make([]*PortMap, len(*in))
		for i := range *in {
			if (*in)[i] != nil {
				in, out := &(*in)[i], &(*out)[i]
				*out = new(PortMap)
				(*in).DeepCopyInto(*out)
			}
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LoadBalanceSpec.
func (in *LoadBalanceSpec) DeepCopy() *LoadBalanceSpec {
	if in == nil {
		return nil
	}
	out := new(LoadBalanceSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PortMap) DeepCopyInto(out *PortMap) {
	*out = *in
	if in.Ips != nil {
		in, out := &in.Ips, &out.Ips
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PortMap.
func (in *PortMap) DeepCopy() *PortMap {
	if in == nil {
		return nil
	}
	out := new(PortMap)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PublicSepc) DeepCopyInto(out *PublicSepc) {
	*out = *in
	if in.Subnet != nil {
		in, out := &in.Subnet, &out.Subnet
		*out = new(SubnetSpec)
		**out = **in
	}
	if in.Address != nil {
		in, out := &in.Address, &out.Address
		*out = new(Address)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PublicSepc.
func (in *PublicSepc) DeepCopy() *PublicSepc {
	if in == nil {
		return nil
	}
	out := new(PublicSepc)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ResourceStatus) DeepCopyInto(out *ResourceStatus) {
	*out = *in
	out.ServerStat = in.ServerStat
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ResourceStatus.
func (in *ResourceStatus) DeepCopy() *ResourceStatus {
	if in == nil {
		return nil
	}
	out := new(ResourceStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ServerSpec) DeepCopyInto(out *ServerSpec) {
	*out = *in
	if in.BootVolume != nil {
		in, out := &in.BootVolume, &out.BootVolume
		*out = new(VolumeSpec)
		**out = **in
	}
	if in.Volumes != nil {
		in, out := &in.Volumes, &out.Volumes
		*out = make([]*VolumeSpec, len(*in))
		for i := range *in {
			if (*in)[i] != nil {
				in, out := &(*in)[i], &(*out)[i]
				*out = new(VolumeSpec)
				**out = **in
			}
		}
	}
	if in.SecurityGroups != nil {
		in, out := &in.SecurityGroups, &out.SecurityGroups
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.Subnet != nil {
		in, out := &in.Subnet, &out.Subnet
		*out = new(SubnetSpec)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ServerSpec.
func (in *ServerSpec) DeepCopy() *ServerSpec {
	if in == nil {
		return nil
	}
	out := new(ServerSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ServerStat) DeepCopyInto(out *ServerStat) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ServerStat.
func (in *ServerStat) DeepCopy() *ServerStat {
	if in == nil {
		return nil
	}
	out := new(ServerStat)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *SubnetSpec) DeepCopyInto(out *SubnetSpec) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new SubnetSpec.
func (in *SubnetSpec) DeepCopy() *SubnetSpec {
	if in == nil {
		return nil
	}
	out := new(SubnetSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *VirtualMachine) DeepCopyInto(out *VirtualMachine) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new VirtualMachine.
func (in *VirtualMachine) DeepCopy() *VirtualMachine {
	if in == nil {
		return nil
	}
	out := new(VirtualMachine)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *VirtualMachine) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *VirtualMachineList) DeepCopyInto(out *VirtualMachineList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]VirtualMachine, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new VirtualMachineList.
func (in *VirtualMachineList) DeepCopy() *VirtualMachineList {
	if in == nil {
		return nil
	}
	out := new(VirtualMachineList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *VirtualMachineList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *VirtualMachineSpec) DeepCopyInto(out *VirtualMachineSpec) {
	*out = *in
	if in.Auth != nil {
		in, out := &in.Auth, &out.Auth
		*out = new(AuthSpec)
		**out = **in
	}
	if in.Server != nil {
		in, out := &in.Server, &out.Server
		*out = new(ServerSpec)
		(*in).DeepCopyInto(*out)
	}
	if in.LoadBalance != nil {
		in, out := &in.LoadBalance, &out.LoadBalance
		*out = new(LoadBalanceSpec)
		(*in).DeepCopyInto(*out)
	}
	if in.Public != nil {
		in, out := &in.Public, &out.Public
		*out = new(PublicSepc)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new VirtualMachineSpec.
func (in *VirtualMachineSpec) DeepCopy() *VirtualMachineSpec {
	if in == nil {
		return nil
	}
	out := new(VirtualMachineSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *VirtualMachineStatus) DeepCopyInto(out *VirtualMachineStatus) {
	*out = *in
	if in.VmStatus != nil {
		in, out := &in.VmStatus, &out.VmStatus
		*out = new(ResourceStatus)
		**out = **in
	}
	if in.NetStatus != nil {
		in, out := &in.NetStatus, &out.NetStatus
		*out = new(ResourceStatus)
		**out = **in
	}
	if in.PubStatus != nil {
		in, out := &in.PubStatus, &out.PubStatus
		*out = new(ResourceStatus)
		**out = **in
	}
	if in.Members != nil {
		in, out := &in.Members, &out.Members
		*out = make([]*ServerStat, len(*in))
		for i := range *in {
			if (*in)[i] != nil {
				in, out := &(*in)[i], &(*out)[i]
				*out = new(ServerStat)
				**out = **in
			}
		}
	}
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]*Condition, len(*in))
		for i := range *in {
			if (*in)[i] != nil {
				in, out := &(*in)[i], &(*out)[i]
				*out = new(Condition)
				**out = **in
			}
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new VirtualMachineStatus.
func (in *VirtualMachineStatus) DeepCopy() *VirtualMachineStatus {
	if in == nil {
		return nil
	}
	out := new(VirtualMachineStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *VolumeSpec) DeepCopyInto(out *VolumeSpec) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new VolumeSpec.
func (in *VolumeSpec) DeepCopy() *VolumeSpec {
	if in == nil {
		return nil
	}
	out := new(VolumeSpec)
	in.DeepCopyInto(out)
	return out
}
