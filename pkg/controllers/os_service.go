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
	corev1 "k8s.io/api/core/v1"
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

	StackTag = "ecns-mixapp"

	Succeeded = "Succeeded"
	Failed    = "Failed"

	CheckStep  = "Check"
	ServerStep = "Server"
	LbStep     = "Loadbalance"
)

type SuccessErr struct {
	msg string
}

func (se *SuccessErr) Error() string {
	return se.msg
}
func NewSuccessErr(s string) *SuccessErr {
	return &SuccessErr{
		msg: s,
	}
}

type OSService struct {
	auth    *Auth
	WokerM  *WorkManager
	podsync *PodIp
	engine  *template.Template

	tmpDir       string //need rw mode
	lbtpl, vmtpl string

	logger logr.Logger
}

func NewOSService(lbtplpath, vmtplpath, tmpdir string, identify string, wm *WorkManager, podsync *PodIp, logger logr.Logger) *OSService {
	// get ECS cloud admin credential info from env

	oss := &OSService{
		auth:    NewAuth(identify, logger),
		logger:  logger,
		tmpDir:  tmpdir,
		lbtpl:   "net",
		vmtpl:   "vm",
		WokerM:  wm,
		podsync: podsync,
	}

	tmpl := template.NewTemplate(oss.logger)
	for k, v := range map[string]string{
		oss.lbtpl: lbtplpath,
		oss.vmtpl: vmtplpath,
	} {
		tmpl.AddTempFileMust(k, v)
	}
	oss.engine = tmpl
	wm.Run()
	return oss
}

func (oss *OSService) addMembersByIps(netstat *vmv1.VirtualMachineStatus, ips ...*Result) {
	if len(ips) == 0 {
		return
	}
	var ipmap = make(map[string]struct{}, len(ips))
	for _, member := range netstat.Members {
		ipmap[member.Ip] = struct{}{}
	}
	for _, ip := range ips {
		if _, ok := ipmap[ip.Ip]; ok {
			continue
		}
		ipmap[ip.Ip] = struct{}{}
		netstat.Members = append(netstat.Members, &vmv1.ServerStat{
			Ip: ip.Ip,
			Id: ip.PodName,
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

func (oss *OSService) ServerRecocile(vm *vmv1.VirtualMachine) {
	var (
		isstop bool = true
		err    error
	)
	if vm.Spec.Server == nil || vm.Status.Members == nil {
		return
	}

	oss.logger.Info("Recocile member", "phase", vm.Spec.AssemblyPhase)
	if vm.Spec.AssemblyPhase != vmv1.Stop {
		isstop = false
	}

	for _, v := range vm.Status.Members {
		if v.Stat == ServerRunStat && isstop {
			err = oss.auth.ServerStop(vm.Spec.Auth, v.Id)
			oss.logger.Info("Recocile member failed", "id", v.Id, "err", err)
		} else if v.Stat == ServerStopStat && !isstop {
			err = oss.auth.ServerStart(vm.Spec.Auth, v.Id)
			oss.logger.Info("Recocile member failed", "id", v.Id, "err", err)
		}
	}
	return
}

// All
func (oss *OSService) Reconcile(vm *vmv1.VirtualMachine) (*vmv1.VirtualMachineStatus, error) {
	var (
		err      error
		findIps  bool
		tmpfpath string
		errType  string
	)
	newspec := vm.Spec.DeepCopy()
	newstat := vm.Status.DeepCopy()

	netspec := newspec.LoadBalance
	defer func() {
		oss.setcondition(newstat, errType, err)
	}()
	errType = CheckStep
	if newspec.Server == nil && netspec == nil {
		err = fmt.Errorf("Not found server and loadbalance spec")
		return newstat, err
	}
	errType = ServerStep
	if newspec.Server != nil {
		tmpfpath, err = oss.generateTmpFile(oss.vmtpl, newspec)
		if err != nil {
			oss.logger.Error(err, "generate computer template failed")
			return newstat, err
		}
		if newspec.Server.BootImage == "" && newspec.Server.BootVolumeId == "" {
			err = fmt.Errorf("Boot image or boot volume must not nil both!")
			return newstat, err
		}
		oss.logger.Info("Sync spec server", "server", newspec.Server.Name)
		err = oss.syncComputer(tmpfpath, newspec, newstat)
		if err != nil {
			if _, ok := err.(*SuccessErr); !ok {
				oss.logger.Info("Sync spec server done, Not sync lb.", "msg", err)
				return newstat, err
			}
		}
	}

	errType = LbStep
	if netspec == nil || len(netspec.Ports) == 0 {
		err = fmt.Errorf("Lb spec or lb ports is nil")
		return newstat, err
	}
	// fetch ips from pod, now link is deploy selfLink url
	if netspec.Link != "" {
		if !oss.podsync.LbExist(netspec.LbIp) {
			portmap := make(map[int32]string)
			for _, portm := range netspec.Ports {
				portmap[portm.Port] = portm.Protocol
			}
			oss.podsync.AddLinks(netspec.LbIp, netspec.Link, portmap)
		}
		ips := oss.podsync.SecondIp(netspec.LbIp)
		if ips != nil {
			oss.logger.Info("Add members from link", "link", netspec.Link)
			oss.addMembersByIps(newstat, ips...)
		}
	}
	for _, po := range netspec.Ports {
		for _, mem := range newstat.Members {
			if mem.Ip == "" {
				continue
			}
			// ip had set, should sync loadbalance
			po.Ips = append(po.Ips, mem.Ip)
			findIps = true
		}
	}
	if findIps == false {
		err = fmt.Errorf("Not found ip from server or pod, stop sync loadbalance")
		return newstat, err
	}
	tmpfpath, err = oss.generateTmpFile(oss.lbtpl, newspec)
	if err != nil {
		return newstat, err
	}
	oss.logger.Info("Sync spec loadbalance", "lb", netspec.Name)
	err = oss.syncNet(tmpfpath, newspec, newstat)
	if err != nil {
		if _, ok := err.(*SuccessErr); !ok {
			oss.logger.Info("Sync spec loadbalance failed", "err", err)
			return newstat, err
		}
		err = nil
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
		if vmstat.StackID != "" {
			err = oss.WokerM.DelItem(vmstat.StackID)
			if err != nil {
				vmstat.Template = err.Error()
			} else {
				vmstat.Template = ""
			}
		}
	}

	if netstat != nil {
		netstat.Stat = string(vmv1.Deleting)
		if spec.LoadBalance != nil && spec.LoadBalance.Link != "" {
			oss.podsync.DelLinks(spec.LoadBalance.LbIp)
		}
		if netstat.StackID != "" {
			err = oss.WokerM.DelItem(netstat.StackID)
			if err != nil {
				netstat.Template = err.Error()
			} else {
				netstat.Template = ""
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
		readystat = corev1.ConditionFalse
	)
	if isready {
		readystat = corev1.ConditionTrue
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

func (oss *OSService) syncResourceStat(auth *vmv1.AuthSpec, stat *vmv1.ResourceStatus, tmpfpath string) error {
	var (
		stackname  = stat.StackName
		id         string
		err        error
		updataTemp bool
	)

	defer os.Remove(tmpfpath)

	data, err := ioutil.ReadFile(tmpfpath)
	if err != nil {
		return err
	}

	newhash := fmt.Sprintf("%d", hashid(data))

	if stat.HashId == "" {
		id, err = oss.createStack(stackname, tmpfpath, auth)
		if err != nil {
			oss.logger.Info("Creat stack failed", "error", err, "stackname", stackname)
			return err
		}
		if id == "" {
			err = fmt.Errorf("Id can not fetch by stackname: %s", stackname)
			return err
		}
		oss.logger.Info("Stack create success", "stackname", stackname)
		stat.StackID = id
		stat.HashId = newhash
		updataTemp = true
	} else if stat.HashId != newhash {
		oss.logger.Info("Stack update", "old hashid", stat.HashId, "new hashid", newhash)
		// use drone user to update stack, the stack ownership is also who created
		err = oss.updateStack(stackname, stat.StackID, tmpfpath, nil)
		if err != nil {
			if _, ok := err.(gophercloud.ErrDefault409); !ok {
				// stack exists
				oss.logger.Info("Update stack failed", "stackname", stackname, "error", err)
				return err
			} else {
				oss.logger.Info("stack is updating", "stackname", stackname)
			}
		}
		oss.logger.Info("Update stack success", "stackname", stackname)
		updataTemp = true
		stat.HashId = newhash
	}
	if updataTemp {
		bs, _ := yaml.YAMLToJSON(data)
		stat.Template = string(bs)
	}
	return nil
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
		Tags:    []string{StackTag},
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
		Tags:    []string{StackTag},
	}
	if auth == nil {
		return oss.auth.HeatUpdateWithClient(oss.WokerM.provider, name, id, Opts)
	}
	return oss.auth.HeatUpdate(auth, name, id, Opts)
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

func getStat(rst *StackRst) string {
	if rst == nil {
		return ""
	}
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
		fallthrough
	case S_CREATE_COMPLETE:
		return Succeeded
	default:
		return ""
	}
}
