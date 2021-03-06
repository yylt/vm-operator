package controllers

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"reflect"
	"strings"
	"sync"

	vmv1 "easystack.io/vm-operator/pkg/api/v1"
	"easystack.io/vm-operator/pkg/manage"
	"easystack.io/vm-operator/pkg/template"
	"easystack.io/vm-operator/pkg/util"
	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/orchestration/v1/stacks"
	"github.com/gophercloud/gophercloud/pagination"
	"github.com/tidwall/gjson"
	klog "k8s.io/klog/v2"
	"sigs.k8s.io/yaml"
)

const (
	heatDoneTimeOut = 60
	tmpPattern      = "tmp-*.txt"

	Succeeded = "Succeeded"
	Failed    = "Failed"

	S_CREATE_FAILED      = "CREATE_FAILED"
	S_CREATE_IN_PROGRESS = "CREATE_IN_PROGRESS"
	S_CREATE_COMPLETE    = "CREATE_COMPLETE"
	S_UPDATE_FAILED      = "UPDATE_FAILED"
	S_UPDATE_IN_PROGRESS = "UPDATE_IN_PROGRESS"
	S_UPDATE_COMPLETE    = "UPDATE_COMPLETE"
)

type StackResult struct {
	ID           string `json:"id"`
	Name         string `json:"stack_name"`
	Status       string `json:"stack_status"`
	StatusReason string `json:"stack_status_reason"`

	//had sync or not
	sync bool
}

type Reorderfn func(spec *vmv1.VirtualMachineSpec, stat *vmv1.ResourceStatus)

func (s *StackResult) DeepCopyFrom(ls *stacks.ListedStack) {
	s.ID = ls.ID
	s.Name = ls.Name
	s.Status = ls.Status
	buf := util.GetBuf()
	buf.WriteString(ls.StatusReason)
	reason, _ := buf.ReadBytes('\n')
	if reason != nil {
		s.StatusReason = string(reason)
	}
	util.PutBuf(buf)
}

func (s *StackResult) DeepCopy() *StackResult {
	tmp := new(StackResult)
	tmp.ID = s.ID
	tmp.Name = s.Name
	tmp.Status = s.Status
	tmp.StatusReason = s.StatusReason
	return tmp
}

type Heat struct {
	engine *template.Template

	opmgr *manage.OpenMgr
	// stackid - stackResutl
	stacks map[string]*StackResult

	mu sync.RWMutex

	tmpdir   string
	endpoint string

	// when stack update, resources must append
	// if the position of resources exchange will update failed
	reorderfuncs map[template.Kind]Reorderfn
}

func NewHeat(engine *template.Template, tmpdir string, opmgr *manage.OpenMgr) *Heat {
	opt, err := openstack.AuthOptionsFromEnv()
	if err != nil {
		panic(err)
	}
	ht := &Heat{
		mu:           sync.RWMutex{},
		engine:       engine,
		opmgr:        opmgr,
		tmpdir:       tmpdir,
		endpoint:     opt.IdentityEndpoint,
		stacks:       make(map[string]*StackResult),
		reorderfuncs: make(map[template.Kind]Reorderfn),
	}

	opmgr.Regist(manage.Heat, ht.addStore)
	return ht
}

//check stack had complete
// means stack is failed or successed
func (h *Heat) isSynced(id string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	v, ok := h.stacks[id]
	if !ok {
		h.stacks[id] = &StackResult{}
		return false
	}
	if !v.sync {
		return v.sync
	}
	if v.Status == S_CREATE_IN_PROGRESS || v.Status == S_UPDATE_IN_PROGRESS {
		return false
	}
	return v.sync
}

func (h *Heat) listenById(id string) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	v, ok := h.stacks[id]
	if !ok {
		h.stacks[id] = &StackResult{}
	} else {
		v.sync = false
	}
}

func (h *Heat) update(stat *vmv1.ResourceStatus) error {
	if stat == nil || stat.StackID == "" {
		return fmt.Errorf("update failed: stackId not found")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	v, ok := h.stacks[stat.StackID]
	if !ok || v.sync == false {
		klog.V(2).Infof("stack id(%v) not synced", stat.StackID)
		return nil
	}
	stat.Stat = getStackStat(v)
	if stat.Stat == Failed {
		if v.StatusReason == "" {
			klog.Info("stack status failed, but no reason")
			return nil
		}
		return fmt.Errorf(v.StatusReason)
	}
	return nil
}

func (h *Heat) addStore(page pagination.Page) {
	lists, err := stacks.ExtractStacks(page)
	if err != nil {
		klog.Errorf("stacks extract page failed:%v", err)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, stack := range lists {
		v, ok := h.stacks[stack.ID]
		if ok {
			klog.V(3).Infof("callback update stack:%v", stack)
			v.DeepCopyFrom(&stack)
		}
	}
	for _, v := range h.stacks {
		v.sync = true
	}
	return
}

// init only in beginning, so do not need mutex
func (h *Heat) RegistReOrderFunc(kind template.Kind, fn Reorderfn) {
	_, ok := h.reorderfuncs[kind]
	if ok {
		klog.Infof("update reorder func on kind %s", kind)
		h.reorderfuncs[kind] = fn
		return
	}
	klog.Infof("add reorder func on kind %s", kind)
	h.reorderfuncs[kind] = fn
}

// the stat on vm must not be nil
func (h *Heat) Process(kind manage.OpResource, vm *vmv1.VirtualMachine) (reterr error) {
	var (
		stat *vmv1.ResourceStatus
		tpl  template.Kind
		isdo bool
	)
	if vm == nil {
		return fmt.Errorf("vm param is nil")
	}

	switch kind {
	case manage.Vm:
		stat = vm.Status.VmStatus
		tpl = template.Vm
	case manage.Fip:
		stat = vm.Status.PubStatus
		tpl = template.Fip
	case manage.Lb:
		stat = vm.Status.NetStatus
		tpl = template.Lb
	default:
		return fmt.Errorf("not found openstack resource %v", kind)
	}
	if vm.DeletionTimestamp != nil {
		stat.Stat = string(vmv1.Deleting)
		err := h.DeleteStack(stat)
		if err != nil {
			klog.Errorf("delete stack failed:%v", err)
		}
		return err
	}
	if stat.StackID != "" && !h.isSynced(stat.StackID) {
		klog.V(2).Infof("stack %s is in Progress Stat, skip update", stat.StackName)
		return h.update(stat)
	}
	fpath, hashid, err := h.generateTmpFile(tpl, &vm.Spec, stat)
	if err != nil {
		klog.Errorf("generate template file failed: %v", err)
		return h.update(stat)
	}
	defer func() {
		if !klog.V(10).Enabled() {
			os.Remove(fpath)
		}
	}()
	if stat.HashId == 0 {
		err = h.createStack(fpath, vm.Spec.Auth, stat)
		if err != nil {
			klog.Errorf("Creat stack failed:%v", err)
			if stat.StackID == "" {
				// stackname should remove, will generate new one next
				stat.StackName = ""
				return h.update(stat)
			}
		}
		stat.HashId = hashid
		stat.Stat = string(vmv1.Creating)
		h.listenById(stat.StackID)
		isdo = true
	}
	if stat.HashId != hashid {
		// use drone user to update stack, the stack ownership is also who created
		err = h.updateStack(fpath, stat, true)
		if err != nil {
			klog.Errorf("update stack failed:%v", err)
		}
		stat.HashId = hashid
		stat.Stat = string(vmv1.Updating)
		h.listenById(stat.StackID)
		isdo = true
	}
	rerr := h.update(stat)
	if isdo == false {
		if rerr != nil && strings.Contains(rerr.Error(), "Create timed out") {
			err = h.updateStack(fpath, stat, false)
			if err != nil {
				klog.Error("update after create timeout failed: %v", err)
			}
			stat.Stat = string(vmv1.Creating)
			h.listenById(stat.StackID)
		}
	}
	return rerr
}

// generate template file
// 1. update stat.Template if hashid not equal
func (h *Heat) generateTmpFile(tpl template.Kind, spec *vmv1.VirtualMachineSpec, stat *vmv1.ResourceStatus) (fpath string, hashid int64, reterr error) {
	// update spec by template
	fn, ok := h.reorderfuncs[tpl]
	if ok {
		fn(spec, stat)
	}

	data, reterr := json.Marshal(spec)
	if reterr != nil {
		return
	}
	params := template.Parse(gjson.ParseBytes(data))
	tmpfile, reterr := ioutil.TempFile(h.tmpdir, tmpPattern)
	if reterr != nil {
		return
	}
	fpath = path.Join(tmpfile.Name())
	defer func() {
		tmpfile.Close()
		if reterr != nil {
			os.Remove(fpath)
		}
	}()

	data, reterr = h.engine.RenderByName(tpl, params)
	_, reterr = tmpfile.Write(data)
	if reterr != nil {
		return
	}
	hashid = util.Hashid(data)
	if stat.HashId == hashid {
		return
	}
	bs, _ := yaml.YAMLToJSON(data)
	stat.Template = string(bs)
	return
}

// TODO if resource on stack yaml can set project_id, it's not needed
func (h *Heat) getClient(as *vmv1.AuthSpec) (*gophercloud.ServiceClient, error) {
	opts := gophercloud.AuthOptions{
		IdentityEndpoint: h.endpoint,
		TokenID:          as.Token,
		TenantID:         as.ProjectID,
	}
	cli, err := openstack.AuthenticatedClient(opts)
	if err != nil {
		return nil, err
	}
	return openstack.NewOrchestrationV1(cli, gophercloud.EndpointOpts{})
}

// NOTE: stat must be not nil!
// 1. update stat.StackId
func (h *Heat) createStack(fpath string, auth *vmv1.AuthSpec, stat *vmv1.ResourceStatus) error {
	cli, err := h.getClient(auth)
	if err != nil {
		return err
	}

	ctOpts := &stacks.CreateOpts{
		Name: stat.StackName,
		TemplateOpts: &stacks.Template{
			TE: stacks.TE{
				URL: "file://" + fpath,
			},
		},
		Timeout: heatDoneTimeOut,
		Tags:    []string{util.StackTag},
	}
	rst := stacks.Create(cli, ctOpts)
	result, err := rst.Extract()
	if result != nil {
		stat.StackID = result.ID
		return nil
	}
	if err != nil {
		klog.Errorf("create stack failed:%v", err)
	}
	err = fmt.Errorf("create stack failed, but no reason")
	return err
}

func (h *Heat) DeleteStack(stat *vmv1.ResourceStatus) error {
	var (
		err error
	)
	defer func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.stacks, stat.StackID)
	}()
	if stat == nil {
		return nil
	}
	if stat.StackID == "" || stat.StackName == "" {
		return nil
	}
	klog.V(2).Infof("start delete stack name(%v) id(%v)", stat.StackName, stat.StackID)
	h.opmgr.WrapClient(func(client *gophercloud.ProviderClient) {
		heatcli, rerr := openstack.NewOrchestrationV1(client, gophercloud.EndpointOpts{})
		if rerr != nil {
			err = rerr
			return
		}
		err = stacks.Delete(heatcli, stat.StackName, stat.StackID).ExtractErr()
		if err != nil {
			klog.Errorf("failed delete stack: %v, err type: %v", err, reflect.TypeOf(err))
			if _, ok := err.(gophercloud.ErrDefault404); ok {
				err = nil
			}
		}
		if err == nil {
			stat.StackID = ""
			stat.StackName = ""
			klog.V(2).Infof("success delete stack")
		}
	})
	return err
}

func (h *Heat) GetStack(id string) *StackResult {
	h.mu.RLock()
	defer h.mu.RUnlock()
	v, ok := h.stacks[id]
	if !ok {
		return nil
	}
	return v.DeepCopy()
}

func (h *Heat) updateStack(fpath string, stat *vmv1.ResourceStatus, patch bool) error {
	var (
		heatcli *gophercloud.ServiceClient
		err     error
		rst     stacks.UpdateResult
	)
	h.opmgr.WrapClient(func(cli *gophercloud.ProviderClient) {
		heatcli, err = openstack.NewOrchestrationV1(cli, gophercloud.EndpointOpts{})
	})

	if err != nil {
		return err
	}

	Opts := &stacks.UpdateOpts{
		TemplateOpts: &stacks.Template{
			TE: stacks.TE{
				URL: "file://" + fpath,
			},
		},
		Timeout: heatDoneTimeOut,
		Tags:    []string{util.StackTag},
	}
	if patch {
		rst = stacks.UpdatePatch(heatcli, stat.StackName, stat.StackID, Opts)
	} else {
		rst = stacks.Update(heatcli, stat.StackName, stat.StackID, Opts)
	}

	err = rst.ExtractErr()
	if err != nil {
		if _, ok := err.(gophercloud.ErrDefault409); ok {
			err = nil
		}
	}
	if err != nil {
		klog.Errorf("update stack failed:%v", err)
	}
	return err
}

func getStackStat(rst *StackResult) string {
	if rst == nil {
		return ""
	}
	switch rst.Status {
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
		return rst.Status
	}
}
