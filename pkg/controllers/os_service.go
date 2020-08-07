package controllers

import (
	"bytes"
	"encoding/json"
	"fmt"

	"math/rand"
	"os"
	"path"
	"sync"

	"io/ioutil"
	"time"

	vmv1 "easystack.io/vm-operator/pkg/api/v1"
	"easystack.io/vm-operator/pkg/template"

	"github.com/ghodss/yaml"
	"github.com/go-logr/logr"
	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack/orchestration/v1/stacks"
	"github.com/tidwall/gjson"
)

var (
	letters = []rune("0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
	bufpool = sync.Pool{
		New: func() interface{} {
			return new(bytes.Buffer)
		},
	}
)

const (
	S_CREATE_FAILED      = "CREATE_FAILED"
	S_CREATE_IN_PROGRESS = "CREATE_IN_PROGRESS"
	S_CREATE_COMPLETE    = "CREATE_COMPLETE"
	S_UPDATE_FAILED      = "UPDATE_FAILED"
	S_UPDATE_IN_PROGRESS = "UPDATE_IN_PROGRESS"
	S_UPDATE_COMPLETE    = "UPDATE_COMPLETE"
)

const (
	randLen     = 6
	heatTimeout = 10
	tmpPattern  = "tmp-*.txt"

	Succeeded = "Succeeded"
	Failed    = "Failed"
	Running   = "Running"
)

type OSService struct {
	auth *Auth

	podsync      *PodIp
	engine       *template.Template
	tmpDir       string //need rw mode
	stackTag     string
	lbtpl, vmtpl string

	logger logr.Logger
}

func NewOSService(lbtplpath, vmtplpath, tmpdir string, identify string, podsync *PodIp, logger logr.Logger) *OSService {
	// get ECS cloud admin credential info from env

	oss := &OSService{
		auth:     NewAuth(identify, logger),
		logger:   logger,
		tmpDir:   tmpdir,
		lbtpl:    "net",
		vmtpl:    "vm",
		stackTag: "ecns-mixapp",
		podsync:  podsync,
	}

	tmpl := template.NewTemplate(oss.logger)
	for k, v := range map[string]string{
		oss.lbtpl: lbtplpath,
		oss.vmtpl: vmtplpath,
	} {
		tmpl.AddTempFileMust(k, v)
	}
	oss.engine = tmpl
	return oss
}

func (oss *OSService) validOpenstack(spec *vmv1.VirtualMachineSpec) error {
	if &spec == nil {
		return fmt.Errorf("spec not define")
	}
	return nil
}

func (oss *OSService) addIps(members []*vmv1.ServerStat, ips ...string) {
	if len(ips) == 0 {
		return
	}
	var ipmap = make(map[string]struct{}, len(ips))
	for _, member := range members {
		ipmap[member.Ip] = struct{}{}
	}
	for _, ip := range ips {
		if _, ok := ipmap[ip]; ok {
			continue
		}
		members = append(members, &vmv1.ServerStat{
			Ip: ip,
		})
	}
}

func (oss *OSService) generateTmpFile(tpl string, spec *vmv1.VirtualMachineSpec) (string, error) {
	data, err := json.Marshal(spec)
	if err != nil {
		return "", err
	}
	params := template.Parse(gjson.ParseBytes(data))
	tmpfile, err := ioutil.TempFile(oss.tmpDir, tmpPattern)
	if err != nil {
		return "", err
	}
	tmpfpath := path.Join(tmpfile.Name())
	defer func() {
		if err != nil {
			os.Remove(tmpfpath)
		}
	}()

	data, err = oss.engine.RenderByName(tpl, params)
	_, err = tmpfile.Write(data)
	if err != nil {
		return "", err
	}
	err = tmpfile.Close()
	return tmpfpath, err
}

func (oss *OSService) Reconcile(vm *vmv1.VirtualMachine) (*vmv1.VirtualMachineStatus, error) {
	var (
		err      error
		done     bool
		syncnet  bool
		tmpfpath string
	)
	newspec := vm.Spec.DeepCopy()
	newstat := vm.Status.DeepCopy()

	netspec := newspec.LoadBalance

	err = oss.validOpenstack(newspec)
	if err != nil {
		return nil, err
	}

	tmpfpath, err = oss.generateTmpFile(oss.vmtpl, newspec)
	if err != nil {
		oss.logger.Error(err, "generate computer template failed")
		return nil, err
	}

	if newspec.Server != nil {
		oss.logger.Info("start sync server", "name", newspec.Server.Name)
		done, err = oss.syncComputer(tmpfpath, newspec, newstat)
		if err != nil {
			oss.logger.Error(err, "set condition", "name", newspec.Server.Name)
			oss.setcondition(newstat, "server", err)
		}
		if netspec == nil || netspec.Link == "" {
			oss.syncServers(newspec, newstat)
		}
	}

	if netspec == nil {
		return newstat, nil
	}
	if !oss.podsync.LbExist(netspec.LbIp) {
		portmap := make(map[int32]string)
		for _, portm := range netspec.Ports {
			portmap[portm.Port] = portm.Protocol
		}
		oss.podsync.AddLinks(netspec.LbIp, netspec.Link, portmap)
	}

	ips := oss.podsync.SecondIp(netspec.Link)
	if ips != nil {
		oss.logger.Info("add lb-member from links", "link", netspec.Link)
		oss.addIps(newstat.Members, ips...)
	}
	// no members set , should return
	if newstat.Members == nil {
		oss.setReadyCondition(newstat, done, "")
		return newstat, nil
	}

	for _, po := range netspec.Ports {
		for _, mem := range newstat.Members {
			if mem.Ip == "" {
				continue
			}
			// ip had get , should sync loadbalance
			po.Ips = append(po.Ips, mem.Ip)
			syncnet = true
		}
	}
	if syncnet == false {
		return newstat, nil
	}
	tmpfpath, err = oss.generateTmpFile(oss.lbtpl, newspec)
	if err != nil {
		return newstat, err
	}

	oss.logger.Info("start sync network", "name", vm.GetName(), "namespace", vm.GetNamespace())
	done, err = oss.syncNet(tmpfpath, newspec, newstat)
	oss.setReadyCondition(newstat, done, "")
	if err != nil {
		oss.setcondition(newstat, "network", err)
		return newstat, err
	}
	return newstat, err
}

func (oss *OSService) Delete(spec *vmv1.VirtualMachineSpec, stat *vmv1.VirtualMachineStatus) *vmv1.VirtualMachineStatus {
	var (
		err error
	)
	if stat == nil {
		return &vmv1.VirtualMachineStatus{}
	}
	vmstat := stat.VmStatus
	netstat := stat.NetStatus
	if vmstat != nil {
		vmstat.Stat = string(vmv1.Deleting)
		if vmstat.StackID != "" && vmstat.StackName != "" {
			oss.logger.Info("delete server", "id", vmstat.StackID, "name", vmstat.StackName)
			err = oss.auth.HeatDelete(spec.Auth, vmstat.StackName, vmstat.StackID)
			if err != nil {
				oss.logger.Error(err, "delete server stack failed", "stackname", vmstat.StackName)
			}
		}
	}

	if netstat != nil {
		netstat.Stat = string(vmv1.Deleting)
		if spec.LoadBalance != nil && spec.LoadBalance.Link != "" {
			oss.podsync.DelLinks(spec.LoadBalance.LbIp)
		}
		if netstat.StackID != "" && netstat.StackName != "" {
			oss.logger.Info("delete network", "id", netstat.StackID, "name", netstat.StackName)
			err = oss.auth.HeatDelete(spec.Auth, netstat.StackName, netstat.StackID)
			if err != nil {
				oss.logger.Error(err, "delete net stack failed", "stackname", netstat.StackName)
			}
		}
	}
	return stat
}

func (oss *OSService) setcondition(stat *vmv1.VirtualMachineStatus, typestr string, err error) {
	if err == nil {
		return
	}
	for _, cond := range stat.Conditions {
		if cond.Type == typestr {
			if hashid([]byte(cond.Reason)) == hashid([]byte(err.Error())) {
				return
			}
		}
	}
	stat.Conditions = append(stat.Conditions, &vmv1.Condition{
		LastUpdateTime: time.Now().Format(time.RFC3339),
		Reason:         err.Error(),
		Type:           typestr,
	})
}

//update ready condition for application
func (oss *OSService) setReadyCondition(stat *vmv1.VirtualMachineStatus, isready bool, reason string) {
	var (
		readyType = "Ready"
		readystat = "false"
	)
	if isready {
		readystat = "true"
	}
	for _, cond := range stat.Conditions {
		if cond.Type == readyType {
			cond.Status = readystat
			cond.Reason = reason
			return
		}
	}

	stat.Conditions = append(stat.Conditions, &vmv1.Condition{
		LastUpdateTime: time.Now().Format(time.RFC3339),
		Status:         readystat,
		Type:           readyType,
	})

}

func (oss *OSService) syncResourceStat(auth *vmv1.AuthSpec, stat *vmv1.ResourceStatus, tmpfpath, newhash string) (*GetRst, error) {
	var (
		stackname  = stat.StackName
		id         string
		err        error
		updataTemp bool
	)
	if stat.HashId == "" {
		//should create new resource
		id, err = oss.createStack(stackname, tmpfpath, auth)
		if err != nil {
			if _, ok := err.(gophercloud.ErrDefault409); ok {
				// stack exists
				err = oss.auth.HeatList(auth, &stacks.ListOpts{Name: stackname}, func(rst *GetRst) bool {
					id = rst.Id
					return false
				})
			}
			if err != nil {
				oss.logger.Error(err, "Creat stack failed", "stackname", stackname)
				return nil, err
			}
		}
		if id == "" {
			err = fmt.Errorf("Id can not fetch by stackname: %s", stackname)
			return nil, err
		}
		stat.StackID = id
		stat.HashId = newhash
		updataTemp = true
	} else if stat.HashId != newhash {
		err = oss.updateStack(stackname, stat.StackID, tmpfpath, auth)
		if err != nil {
			if _, ok := err.(gophercloud.ErrDefault409); !ok {
				// stack exists
				oss.logger.Error(err, "Update stack failed", "stackname", stackname)
				return nil, err
			} else {
				oss.logger.Info("stack is updating", "stackname", stackname)
			}
		}
		updataTemp = true
	}
	if updataTemp {
		data, _ := ioutil.ReadFile(tmpfpath)
		data, _ = yaml.YAMLToJSON(data)
		stat.Template = string(data)
	}
	return oss.auth.HeatGet(auth, stackname, stat.StackID)
}

func (oss *OSService) createStack(name, tmppath string, auth *vmv1.AuthSpec) (string, error) {
	ctOpts := &stacks.CreateOpts{
		Name: name,
		TemplateOpts: &stacks.Template{
			TE: stacks.TE{
				URL: "file://" + tmppath,
			},
		},
		Timeout: heatTimeout,
		Tags:    []string{oss.stackTag},
	}
	return oss.auth.HeatCreate(auth, ctOpts)
}

func (oss *OSService) updateStack(name, id, tmppath string, auth *vmv1.AuthSpec) error {
	Opts := &stacks.UpdateOpts{
		TemplateOpts: &stacks.Template{
			TE: stacks.TE{
				URL: "file://" + tmppath,
			},
		},
		Timeout: heatTimeout,
		Tags:    []string{oss.stackTag},
	}
	return oss.auth.HeatUpdate(auth, name, id, Opts)
}

func samewith(src, dst []string) bool {
	if dst == nil {
		return true
	}
	if len(src) == 0 || len(src) != len(dst) {
		return false
	}
	dst_map := make(map[string]struct{})
	for _, m := range dst {
		dst_map[m] = struct{}{}
	}

	for _, m := range src {
		if _, ok := dst_map[m]; !ok {
			return false
		}
	}
	return true
}

func randstackName(suffix string) string {
	return fmt.Sprintf("%s-%s", suffix, randSeq(randLen))
}

func randSeq(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

func getStat(rst *GetRst) string {
	switch rst.Stat {
	case S_UPDATE_IN_PROGRESS:
		return string(vmv1.Updating)
	case S_CREATE_IN_PROGRESS:
		return string(vmv1.Creating)
	case S_UPDATE_FAILED:
		fallthrough
	case S_CREATE_FAILED:
		return Failed
	case S_UPDATE_COMPLETE:
		return Succeeded
	case S_CREATE_COMPLETE:
		return Succeeded
	default:
		return ""
	}
}
