package controllers

import (
	"bytes"
	"fmt"
	"sort"

	vmv1 "easystack.io/vm-operator/pkg/api/v1"
)

func (oss *OSService) hashNetwork(spec *vmv1.LoadBalanceSpec) string {
	buf := bufpool.Get().(*bytes.Buffer)
	defer bufpool.Put(buf)
	var (
		proto, ips []string
		port       []int
	)
	buf.Reset()
	buf.WriteString(spec.Subnet.SubnetId)
	buf.WriteString(spec.LbIp)
	for _, portm := range spec.Ports {
		for _, ip := range portm.Ips {
			ips = append(ips, ip)
		}
		proto = append(proto, portm.Protocol)
		port = append(port, int(portm.Port))
	}
	sort.Strings(proto)
	sort.Strings(ips)
	sort.Ints(port)
	for _, v := range proto {
		buf.WriteString(v)
	}
	for _, v := range ips {
		buf.WriteString(v)
	}
	for _, v := range port {
		buf.WriteString(fmt.Sprintf("%d", v))
	}
	return fmt.Sprintf("%d", hashid(buf.Bytes()))
}

// sync network
// make sure networkspec and portmaps is not null
func (oss *OSService) syncNet(tmpfpath string, spec *vmv1.VirtualMachineSpec, stat *vmv1.VirtualMachineStatus) error {
	var (
		stackname string
		netstat   *vmv1.ResourceStatus
	)

	if stat.NetStatus == nil {
		stat.NetStatus = new(vmv1.ResourceStatus)
	}
	netstat = stat.NetStatus

	if netstat.StackName != "" {
		stackname = netstat.StackName
	} else {
		stackname = randstackName(spec.LoadBalance.Name)
		netstat.StackName = stackname
	}

	netstat.Name = spec.LoadBalance.Name

	err := oss.syncResourceStat(spec.Auth, netstat, tmpfpath)
	if err != nil {
		netstat.Stat = Failed
		return err
	}
	item := oss.WokerM.GetItem(netstat.StackID)
	if item == nil {
		err = oss.WokerM.SetItem(NewWorkItem(spec.Auth.ProjectID, netstat.StackID, netstat.StackName, ""))
		if err != nil {
			return err
		}
		return nil
	}
	item.Get(func(_ map[string]*ServerItem, st *StackRst) {
		statstr := getStat(st)
		if statstr == "" {
			err = nil
			return
		}
		netstat.Stat = statstr
		switch statstr {
		case Succeeded:
			err = NewSuccessErr(st.Reason)
		default:
			err = fmt.Errorf(st.Reason)
		}
	})
	return err
}
