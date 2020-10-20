package controllers

import (
	"bytes"
	vmv1 "easystack.io/vm-operator/pkg/api/v1"
	"fmt"
	"time"
)

func (oss *OSService) hashServer(spec *vmv1.ServerSpec) string {
	buf := bufpool.Get().(*bytes.Buffer)
	defer bufpool.Put(buf)
	buf.Reset()
	buf.WriteString(spec.Subnet.SubnetId)
	buf.WriteString(spec.Flavor)
	buf.WriteString(spec.BootImage)
	buf.WriteString(spec.BootVolumeId)
	buf.WriteString(spec.UserData)

	return fmt.Sprintf("%d", hashid(buf.Bytes()))
}

// sync server
// create or update
func (oss *OSService) syncComputer(tmpfpath string, spec *vmv1.VirtualMachineSpec, stat *vmv1.VirtualMachineStatus) error {
	var (
		stackname, netname string
		err                error
		novastat           *vmv1.ResourceStatus
	)

	if stat.VmStatus == nil {
		stat.VmStatus = new(vmv1.ResourceStatus)
	}
	novastat = stat.VmStatus

	if novastat.StackName != "" {
		stackname = novastat.StackName
	} else {
		stackname = randstackName(spec.Server.Name)
		novastat.StackName = stackname
	}
	novastat.Name = spec.Server.Name
	if spec.Server.Subnet != nil {
		netname = spec.Server.Subnet.NetworkName
	}
	err = oss.syncResourceStat(spec.Auth, novastat, tmpfpath)
	if err != nil {
		novastat.Stat = Failed
		return err
	}
	item := oss.WokerM.GetItem(novastat.StackID)
	if item == nil {
		it := NewWorkItem(spec.Auth.ProjectID, novastat.StackID, novastat.StackName, novastat.Name)
		err = oss.WokerM.SetItem(it)
		if err != nil {
			return err
		}
		return nil
	}

	ids := make(map[string]int)
	// Index members, server id as key, server index as value
	for i, mem := range stat.Members {
		ids[mem.Id] = i
	}
	item.Get(func(novas map[string]*ServerItem, st *StackRst) {
		statstr := getStat(st)
		if statstr == "" {
			err = nil
			return
		}
		stat.VmStatus.Stat = statstr
		switch statstr {
		case Succeeded:
			err = NewSuccessErr(st.Reason)
			for _, item := range novas {
				ipstr, ok := item.Ip4addrs[netname]
				if !ok {
					continue
				}
				oss.logger.Info("add server", "server id", item.Id, "server ip", ipstr)
				if v, ok := ids[item.Id]; ok {
					stat.Members[v].Ip = ipstr
					stat.Members[v].Stat = item.Stat
				} else {
					stat.Members = append(stat.Members, &vmv1.ServerStat{
						Id:         item.Id,
						CreateTime: item.CreateTime.Format(time.RFC3339),
						Stat:       item.Stat,
						Ip:         ipstr,
						Name:       item.Name,
					})
				}
			}
		default:
			err = fmt.Errorf(st.Reason)
		}
	})
	return err
}
