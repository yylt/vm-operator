package manage

import (
	goctx "context"
	"encoding/json"
	"fmt"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"net"
	"strings"
	"sync"
	"time"

	"easystack.io/vm-operator/pkg/util"
	"github.com/tidwall/gjson"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	klog "k8s.io/klog/v2"
)

type K8Res int

const (
	Pod K8Res = iota
)

// 1. Sync service which externalIPs is lb ip
// 2. Record pod ip which belong to the link.
type K8sMgr struct {
	lbinfo map[string]*info
	mu     sync.RWMutex
	ctx    goctx.Context
	stopch chan struct{}
	client dynamic.Interface
}

type Result struct {
	Ip      net.IP
	PodName string
}

type info struct {
	// exist ip when init?
	// if ip not exist, getlbfn will try fetch lbip
	existip bool

	// can be nil
	lbip     net.IP
	isdelete bool
	portmap  map[int32]string // should add
	hashid   int64
	link     string
}

type Resource struct {
	name, namespace string
	group           string
	version         string
	kind            string

	svcname string
}

func (r *Resource) IsResource(res K8Res) bool {
	switch res {
	case Pod:
		return r.kind == "pods"
	default:
		return false
	}
}

func (r *Resource) NamespaceName() string {
	return fmt.Sprintf("%s/%s", r.namespace, r.name)
}

func NewK8sMgr(client dynamic.Interface) *K8sMgr {
	mgr := &K8sMgr{
		client: client,
		ctx:    goctx.Background(),
		stopch: make(chan struct{}),
		lbinfo: make(map[string]*info),
	}
	return mgr
}

func (p *K8sMgr) hashinfo(val *info) int64 {
	buf := util.GetBuf()
	defer util.PutBuf(buf)

	if val.isdelete {
		buf.WriteByte('t')
	} else {
		buf.WriteByte('f')
	}
	buf.WriteString(val.lbip.String())
	buf.WriteString(val.link)
	for port, pt := range val.portmap {
		buf.WriteString(fmt.Sprintf("%d", port))
		buf.WriteString(pt)
	}

	return util.Hashid(buf.Bytes())
}

// TODO add more worker on sync pod
func (p *K8sMgr) loop(timed time.Duration, getlbfn func(link string) net.IP) {
	var err error
	for {
		select {
		case <-p.stopch:
			klog.Info("receive stop signal, exit!")
			return
		case <-time.NewTimer(timed).C:
			p.mu.RLock()
			for link, val := range p.lbinfo {
				if !val.existip {
					val.lbip = getlbfn(link)
				}
				if val.lbip == nil {
					klog.Infof("load balance ip not found, link:%v", val.link)
					continue
				}
				hashid := p.hashinfo(val)
				if val.hashid == hashid {
					continue
				}
				klog.V(4).Infof("start sync k8s service, link:%v", val.link)

				err = p.updateService(val.lbip, val)
				if err != nil {
					klog.Infof("update service failed:%v", err)
				}
				if val.isdelete == true {
					delete(p.lbinfo, link)
				}
				val.hashid = hashid
				klog.V(4).Infof("sync k8s service done, delete:%v, link:%v", val.isdelete, val.link)
			}
			p.mu.RUnlock()
		}
	}
}

func (p *K8sMgr) Run(du time.Duration, getlbfn func(link string) net.IP) {
	go p.loop(du, getlbfn)
}

func (p *K8sMgr) Stop() {
	close(p.stopch)
}

func (p *K8sMgr) updateService(lbip net.IP, val *info) error {

	var (
		gvk = schema.GroupVersionResource{
			Version:  "v1",
			Resource: "services",
		}
		ns_map = make(map[string]struct{})
		link   = val.link
	)

	labels, res, err := getLinkLabels(p.client, link)
	if err != nil {
		return err
	}

	if val.isdelete == true {
		err = p.client.Resource(gvk).Namespace(res.namespace).Delete(p.ctx, res.svcname, metav1.DeleteOptions{})
		if err != nil {
			return err
		}
	}

	svcunstruct := serviceExternalUnstract(labels, res.namespace, res.svcname, lbip.String(), val.portmap)
	_, err = p.client.Resource(gvk).Namespace(res.namespace).Get(p.ctx, res.svcname, metav1.GetOptions{})
	if err != nil {
		//create, the err should be not found type
		ns_map[res.namespace] = struct{}{}
		_, err = p.client.Resource(gvk).Namespace(res.namespace).Create(p.ctx, svcunstruct, metav1.CreateOptions{})
		if err != nil {
			return err
		}
	} else {
		//update
		if _, ok := ns_map[res.namespace]; !ok {
			klog.V(2).Infof("patch k8s service", "name", res.svcname, "ns", res.namespace, "value", svcunstruct.Object)
			data, err := json.Marshal(svcunstruct.Object["spec"])
			_, err = p.client.Resource(gvk).Namespace(res.namespace).Patch(p.ctx, res.svcname, types.MergePatchType, data, metav1.PatchOptions{})
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (p *K8sMgr) LinkIsExist(link string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if _, ok := p.lbinfo[link]; ok {
		return true
	}
	return false
}

func (p *K8sMgr) DelLinks(links ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, v := range links {
		if val, ok := p.lbinfo[v]; ok {
			klog.Infof("set delete flag on link:%v", v)
			val.isdelete = true
		}
	}
}

func (p *K8sMgr) AddLinks(link string, lbip net.IP, portmap map[int32]string) {
	if link == "" {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	klog.Infof("link add lbip:%v, link:%v", lbip, link)
	if val, ok := p.lbinfo[link]; ok {
		val.link = link
		for k, _ := range val.portmap {
			delete(val.portmap, k)
		}
		val.portmap = portmap
	} else {
		p.lbinfo[link] = &info{
			portmap: portmap,
			link:    link,
			lbip:    lbip,
			existip: lbip != nil,
		}
	}
}

func (p *K8sMgr) IsExist(res *Resource) (bool, error) {
	if res == nil {
		return false, fmt.Errorf("resource is nil")
	}
	var (
		ver string
	)
	if res.group != "" {
		ver = fmt.Sprintf("%s/%s", res.group, res.version)
	} else {
		ver = res.version
	}
	gvk := schema.GroupVersionResource{
		Version:  ver,
		Resource: res.kind,
	}
	_, err := p.client.Resource(gvk).Namespace(res.namespace).Get(p.ctx, res.name, metav1.GetOptions{})
	if err != nil {
		if apierrs.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (p *K8sMgr) SecondIp(link string) []*Result {
	var (
		ips []*Result
		err error
	)
	p.mu.RLock()
	defer p.mu.RUnlock()

	if _, ok := p.lbinfo[link]; ok {
		err = getPodSecondIps(p.client, link, func(podname string, ip net.IP) {
			if ip == nil {
				return
			}
			ips = append(ips, &Result{Ip: ip,
				PodName: podname})
		})
		if err != nil {
			klog.Errorf("Get kuryr ip failed: %v", err)
		}
	}
	return ips
}

func ParseLink(link string, res *Resource) error {
	links := strings.Split(link, "/")
	if len(links) == 8 {
		res.group = links[2]
		res.version = links[3]
		res.namespace = links[5]
		res.kind = links[6]
		res.name = links[7]
	} else if len(links) == 7 {
		res.version = links[2]
		res.namespace = links[4]
		res.kind = links[5]
		res.name = links[7]
	} else {
		return fmt.Errorf("parse failed, there are not 7 or 8 elements")
	}
	// link example: /apis/apps/v1/namespaces/test/deployments/pause
	// 		/api/v1/namespaces/default/pods/nginx-xs89a
	res.svcname = fmt.Sprintf("%s-%s", res.namespace, res.name)
	return nil
}

func serviceExternalUnstract(labels map[string]string, namespace, name, lbip string, portmap map[int32]string) *unstructured.Unstructured {
	var (
		ports []map[string]interface{}
	)
	for port, proto := range portmap {
		if proto != "UDP" && proto != "TCP" {
			proto = "TCP"
		}
		ports = append(ports, map[string]interface{}{
			"protocol":   proto,
			"port":       port,
			"targetPort": port,
		})
	}

	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": map[string]interface{}{
				"selector":    labels,
				"ports":       ports,
				"externalIPs": []string{lbip},
			},
		},
	}

}

func kuryrIps(object *unstructured.Unstructured, fn func(string, net.IP)) error {

	networks, found, err := unstructured.NestedString(object.Object, "metadata", "annotations", "k8s.v1.cni.cncf.io/networks-status")
	if err != nil || !found {
		return fmt.Errorf("Not found networks-status in annotations")
	}
	jsondata := gjson.Parse(networks)
	if !jsondata.IsArray() {
		return fmt.Errorf("Networks-status in annotations is not list")
	}

	jsondata.ForEach(func(_, value gjson.Result) bool {
		if !value.IsObject() {
			return true
		}
		if value.Get("name").String() != "kuryr" {
			return true
		}
		if ips := value.Get("ips"); ips.IsArray() {
			for _, ip := range ips.Array() {
				tmpip := net.ParseIP(ip.String())
				if tmpip != nil {
					fn(object.GetName(), tmpip)
				}
			}
		}
		return false
	})
	return nil
}

func getLinkLabels(client dynamic.Interface, link string) (map[string]string, *Resource, error) {
	var (
		res  = new(Resource)
		err  error
		ok   bool
		ctx  = goctx.Background()
		maps map[string]interface{}
	)
	err = ParseLink(link, res)
	if err != nil {
		return nil, nil, err
	}
	gvk := schema.GroupVersionResource{
		Group:    res.group,
		Version:  res.version,
		Resource: res.kind,
	}

	wk, err := client.Resource(gvk).Namespace(res.namespace).Get(ctx, res.name, metav1.GetOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("get resource failed,name=%s, err=%s", res.name, err)
	}
	if gvk.Resource == "pods" {
		maps, ok, err = unstructured.NestedMap(wk.Object, "metadata", "labels")
	} else {
		maps, ok, err = unstructured.NestedMap(wk.Object, "spec", "selector", "matchLabels")
	}
	if err != nil || !ok {
		err = fmt.Errorf("Labels not found for resource %s/%s, error=%s", wk.GetNamespace(), wk.GetName(), err)
		return nil, nil, err
	}
	var retmap = make(map[string]string)

	for k, v := range maps {
		if vstr, ok := v.(string); ok {
			if k != "pod-template-hash" {
				retmap[k] = vstr
			}
		}
	}
	return retmap, res, nil
}

func getPodSecondIps(client dynamic.Interface, link string, fn func(string, net.IP)) error {
	var (
		err error
		ctx = goctx.Background()
	)

	maps, res, err := getLinkLabels(client, link)
	if err != nil {
		return err
	}

	buf := util.GetBuf()
	defer util.PutBuf(buf)
	buf.Reset()
	maplen := len(maps)
	count := 0
	for k, v := range maps {
		count += 1
		buf.WriteString(k)
		buf.WriteByte('=')
		buf.WriteString(v)
		if count != maplen {
			buf.WriteByte(',')
		}
	}

	gvk := schema.GroupVersionResource{
		Version:  "v1",
		Resource: "pods",
	}
	results, err := client.Resource(gvk).Namespace(res.namespace).List(ctx, metav1.ListOptions{LabelSelector: buf.String()})
	if err != nil {
		return fmt.Errorf("list pods failed, err=%s", err)
	}
	for _, result := range results.Items {
		err = kuryrIps(&result, fn)
		if err != nil {
			return err
		}
	}

	return nil
}