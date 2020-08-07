package controllers

import (
	"bytes"
	"io/ioutil"

	"fmt"

	vmv1 "easystack.io/vm-operator/pkg/api/v1"
)

func (oss *OSService) hashNetwork(spec *vmv1.LoadBalanceSpec) string {
	buf := bufpool.Get().(*bytes.Buffer)
	defer bufpool.Put(buf)
	buf.Reset()
	buf.WriteString(spec.Subnet.SubnetId)
	buf.WriteString(spec.LbIp)
	for _, portm := range spec.Ports {
		for _, ip := range portm.Ips {
			buf.WriteString(ip)
		}
		buf.WriteString(portm.Protocol)
		buf.WriteString(fmt.Sprintf("%d", portm.Port))
	}

	return fmt.Sprintf("%d", hashid(buf.Bytes()))
}

// sync network
// make sure networkspec and portmaps is not null
func (oss *OSService) syncNet(tmpfpath string, spec *vmv1.VirtualMachineSpec, stat *vmv1.VirtualMachineStatus) (bool, error) {
	var (
		stackname string
		done      bool
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

	data, _ := ioutil.ReadFile(tmpfpath)
	tempHash := fmt.Sprintf("%d", hashid(data))

	result, err := oss.syncResourceStat(spec.Auth, netstat, tmpfpath, tempHash)
	if err != nil {
		netstat.Stat = Failed
		return done, err
	}
	oss.logger.Info(fmt.Sprintf("network result: %+v", result))
	netstat.Stat = getStat(result)
	switch netstat.Stat {
	case Succeeded:
		done = true
		err = fmt.Errorf(result.Reason)
	case Failed:
		err = fmt.Errorf(result.Reason)
	}
	return done, err

}
