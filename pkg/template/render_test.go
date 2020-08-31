package template

import (
	"encoding/json"
	"os"
	"testing"

	vmv1 "easystack.io/vm-operator/pkg/api/v1"
	"github.com/go-logr/logr"
	"github.com/tidwall/gjson"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	log    logr.Logger
	engine *Template
)

func init() {
	log = zap.New(zap.WriteTo(os.Stdout))
	engine = NewTemplate(log)
}

func TestAddTempFileMust(t *testing.T) {
	engine.AddTempFileMust("net", "./files/loadbalance.tpl")
	engine.AddTempFileMust("vm", "./files/vm.tpl")
}

func TestRenderByName(t *testing.T) {
	var volume = &vmv1.VolumeSpec{
		VolumeDeleteByVm: true,
		VolumeType:       "xxx",
		VolumeSize:       3,
	}
	var paramlist = []vmv1.VirtualMachineSpec{
		vmv1.VirtualMachineSpec{
			Auth: &vmv1.AuthSpec{},
			Server: &vmv1.ServerSpec{
				Replicas:   2,
				Name:       "abc",
				BootImage:  "a.iso",
				BootVolume: volume,
				Flavor:     "1-2-4",
				Subnet: &vmv1.SubnetSpec{
					SubnetId: "default",
				},
				Volumes: []*vmv1.VolumeSpec{
					&vmv1.VolumeSpec{
						VolumeDeleteByVm: true,
						VolumeType:       "xxx",
						VolumeSize:       3,
					},
				},
			},
			LoadBalance: &vmv1.LoadBalanceSpec{
				Subnet: &vmv1.SubnetSpec{
					SubnetId: "default",
				},
				LbIp: "1.1.1.1",
				Name: "net",
				Ports: []*vmv1.PortMap{
					&vmv1.PortMap{
						Ips:      []string{"1.2.3.4", "1.1.1.1"},
						Port:     0,
						Protocol: "TCP",
					},
				},
			},

			AssemblyPhase: "",
		},
	}
	for _, v := range paramlist {
		bs, err := json.Marshal(&v)
		if err != nil {
			t.Fatalf(err.Error())
		}
		params := Parse(gjson.ParseBytes(bs))
		bs, err = engine.RenderByName("vm", params)
		t.Logf("vm: %s err:%s", bs, err)
		bs, err = engine.RenderByName("net", params)
		t.Logf("net: %s err:%s", bs, err)
	}
}
