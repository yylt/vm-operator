package controllers

import (
	"bytes"
	"time"

	"fmt"
	"io/ioutil"

	vmv1 "easystack.io/vm-operator/pkg/api/v1"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/servers"
)

func (oss *OSService) hashServer(spec *vmv1.ServerSpec) string {
	buf := bufpool.Get().(*bytes.Buffer)
	defer bufpool.Put(buf)
	buf.Reset()
	buf.WriteString(spec.Subnet.SubnetId)
	buf.WriteString(spec.Flavor)
	buf.WriteString(spec.BootImage)
	buf.WriteString(spec.UserData)

	return fmt.Sprintf("%d", hashid(buf.Bytes()))
}

func (oss *OSService) syncServers(spec *vmv1.VirtualMachineSpec, stat *vmv1.VirtualMachineStatus) {
	var (
		ids = make(map[string]int)
		ip  string
	)
	for i, mem := range stat.Members {
		ids[mem.Id] = i
	}
	opt := servers.ListOpts{Name: spec.Server.Name}
	err := oss.auth.ServerList(spec.Auth, &opt, func(rst *ServerRst) bool {
		if rst.Name != spec.Server.Name {
			return true
		}
		addrs, ok := rst.Addresses["private"]
		if ok {
			for _, val := range addrs {
				if val.Version == 4 {
					ip = val.Address
					break
				}
			}
		}
		if v, ok := ids[rst.Id]; ok {
			stat.Members[v].Ip = ip
			stat.Members[v].Stat = rst.Stat
		} else {
			stat.Members = append(stat.Members, &vmv1.ServerStat{
				Id:         rst.Id,
				CreateTime: rst.CreateTime.Format(time.RFC3339),
				Stat:       rst.Stat,
				Ip:         ip,
				Name:       rst.Name,
			})
		}
		return true
	})
	if err != nil {
		oss.logger.Error(err, "list servers failed")
	}
}

// sync server
// create or update
func (oss *OSService) syncComputer(tmpfpath string, spec *vmv1.VirtualMachineSpec, stat *vmv1.VirtualMachineStatus) (bool, error) {
	var (
		stackname string
		complate  bool
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

	data, _ := ioutil.ReadFile(tmpfpath)
	tempHash := fmt.Sprintf("%d", hashid(data))
	result, err := oss.syncResourceStat(spec.Auth, stat.VmStatus, tmpfpath, tempHash)
	if err != nil {
		oss.logger.Error(err, "sync resource failed", "stackname", stackname)
		novastat.Stat = Failed
		return complate, err
	}

	novastat.Stat = getStat(result)
	switch novastat.Stat {
	case Succeeded:
		complate = true
		err = fmt.Errorf(result.Reason)
	case Failed:
		err = fmt.Errorf(result.Reason)
	}

	return complate, err
}
