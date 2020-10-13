package controllers

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"time"

	vmv1 "easystack.io/vm-operator/pkg/api/v1"
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
		stackname string
		novastat  *vmv1.ResourceStatus
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

	data, err := ioutil.ReadFile(tmpfpath)
	if err != nil {
		return err
	}
	tempHash := fmt.Sprintf("%d", hashid(data))

	err = oss.syncResourceStat(spec.Auth, novastat, tmpfpath, tempHash)
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
	for i, mem := range stat.Members {
		ids[mem.Id] = i
	}
	item.Get(func(novas map[string]*ServerItem, st *StackRst) {
		for _, item := range novas {
			if v, ok := ids[item.Id]; ok {
				stat.Members[v].Ip = item.Ip4addr
				stat.Members[v].Stat = item.Stat
			} else {
				stat.Members = append(stat.Members, &vmv1.ServerStat{
					Id:         item.Id,
					CreateTime: item.CreateTime.Format(time.RFC3339),
					Stat:       item.Stat,
					Ip:         item.Ip4addr,
					Name:       item.Name,
				})
			}
		}
		switch getStat(st) {
		case Succeeded:
			err = NewSuccessErr(st.Reason)
			stat.VmStatus.Stat = Succeeded
		case Failed:
			err = fmt.Errorf(st.Reason)
			stat.VmStatus.Stat = Failed
		default:
		}
	})
	return err
}
