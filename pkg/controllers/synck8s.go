package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"net"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/tidwall/gjson"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

type Result struct {
	Ip      string
	PodName string
}

type info struct {
	portmap map[int32]string // should add

	hashid   uint64
	link     string
	isdelete bool
}

type PodIp struct {
	lbinfo map[string]*info
	mu     sync.RWMutex
	logger logr.Logger

	client dynamic.Interface
	ctx    context.Context
}

type resource struct {
	name, namespace string
	group           string
	version         string
	kind            string

	svcname string
}

func NewPodIp(synct time.Duration, logger logr.Logger, client dynamic.Interface) *PodIp {
	pod := &PodIp{
		logger: logger,
		client: client,
		ctx:    context.TODO(),
		lbinfo: make(map[string]*info),
	}
	pod.sync(synct)
	return pod
}

func (p *PodIp) hashinfo(val *info) uint64 {
	buf := bufpool.Get().(*bytes.Buffer)
	buf.Reset()
	defer bufpool.Put(buf)

	if val.isdelete {
		buf.WriteString("t")
	} else {
		buf.WriteString("f")
	}

	buf.WriteString(val.link)
	for port, pt := range val.portmap {
		buf.WriteString(fmt.Sprintf("%d", port))
		buf.WriteString(pt)

	}
	return hashid(buf.Bytes())
}

// TODO add more worker on sync pod
func (p *PodIp) sync(timed time.Duration) {
	go func() {
		var err error
		for {
			select {
			case <-time.NewTimer(timed).C:
				p.mu.RLock()
				for lbip, val := range p.lbinfo {
					hashid := p.hashinfo(val)
					if val.hashid == hashid {
						continue
					}
					p.logger.Info("start sync k8s service", "link", val.link)
					if val.isdelete == true {
						err = p.syncsvc(lbip, val)
						if err != nil {
							p.logger.Info("delete k8s svc failed, but ignore!", "err", err)
						}
						delete(p.lbinfo, lbip)
						continue
					}
					err = p.syncsvc(lbip, val)
					if err != nil {
						p.logger.Info("patch/create k8s svc failed", "err", err)
						continue
					}
					val.hashid = hashid
					p.logger.Info("sync k8s service successs", "lbip", lbip, "link", val.link)
				}
				p.mu.RUnlock()
			}
		}
	}()
}

func (p *PodIp) syncsvc(lbip string, val *info) error {

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
		err = p.client.Resource(gvk).Namespace(res.namespace).Delete(res.svcname, &metav1.DeleteOptions{})
		if err != nil {
			return err
		}
	}

	svcunstruct := serviceExternalUnstract(labels, res.namespace, res.svcname, lbip, val.portmap)
	_, err = p.client.Resource(gvk).Namespace(res.namespace).Get(res.svcname, metav1.GetOptions{})
	if err != nil {
		//create, the err should be not found type
		ns_map[res.namespace] = struct{}{}
		_, err = p.client.Resource(gvk).Namespace(res.namespace).Create(svcunstruct, metav1.CreateOptions{})
		if err != nil {
			return err
		}
	} else {
		//update
		if _, ok := ns_map[res.namespace]; !ok {
			p.logger.Info("patch k8s service", "name", res.svcname, "ns", res.namespace, "value", svcunstruct.Object)
			data, err := json.Marshal(svcunstruct.Object["spec"])
			_, err = p.client.Resource(gvk).Namespace(res.namespace).Patch(res.svcname, types.MergePatchType, data, metav1.PatchOptions{})
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (p *PodIp) LbExist(lbip string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if _, ok := p.lbinfo[lbip]; ok {
		return true
	}
	return false
}

func (p *PodIp) DelLinks(lbip string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if val, ok := p.lbinfo[lbip]; ok {
		p.logger.Info("set delete on k8s service", "lbip", lbip, "link", val.link)
		val.isdelete = true
	}
}

func (p *PodIp) AddLinks(lbip string, link string, portmap map[int32]string) {
	if link == "" {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.logger.Info("link add", "lbip", lbip, "link", link)
	if val, ok := p.lbinfo[lbip]; ok {
		val.link = link
		for k, _ := range val.portmap {
			delete(val.portmap, k)
		}
		val.portmap = portmap
	} else {
		p.lbinfo[lbip] = &info{
			portmap: portmap,
			link:    link,
		}
	}
}

func (p *PodIp) SecondIp(lbip string) []*Result {
	var (
		ips []*Result
		err error
	)
	p.mu.RLock()
	defer p.mu.RUnlock()

	if val, ok := p.lbinfo[lbip]; ok {
		err = getPodSecondIps(p.logger, p.client, val.link, func(podname, ip string) {
			p.logger.Info("Get kuryr ip success", "link", val.link, "ip", ip)
			ips = append(ips, &Result{Ip: ip,
				PodName: podname})
		})
		if err != nil {
			p.logger.Info("Get kuryr ip failed", "link", val.link, "error", err)
		}
	}
	return ips
}

func parseLink(link string, res *resource) error {
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

func kuryrIps(object *unstructured.Unstructured, fn func(string, string)) error {

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
					fn(object.GetName(), tmpip.String())
				}
			}
		}
		return false
	})
	return nil
}

func getLinkLabels(client dynamic.Interface, link string) (map[string]string, *resource, error) {
	var (
		res  = new(resource)
		err  error
		ok   bool
		maps map[string]interface{}
	)
	err = parseLink(link, res)
	if err != nil {
		return nil, nil, err
	}
	gvk := schema.GroupVersionResource{
		Group:    res.group,
		Version:  res.version,
		Resource: res.kind,
	}

	wk, err := client.Resource(gvk).Namespace(res.namespace).Get(res.name, metav1.GetOptions{})
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

func getPodSecondIps(logger logr.Logger, client dynamic.Interface, link string, fn func(string, string)) error {
	var (
		err error
	)

	maps, res, err := getLinkLabels(client, link)
	if err != nil {
		return err
	}

	buf := bufpool.Get().(*bytes.Buffer)
	defer bufpool.Put(buf)
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
	results, err := client.Resource(gvk).Namespace(res.namespace).List(metav1.ListOptions{LabelSelector: buf.String()})
	if err != nil {
		return fmt.Errorf("list pods failed, err=%s", err)
	}
	logger.Info("list pods on resource", "link", link, "label", buf.String())

	for _, result := range results.Items {
		logger.Info("Try find kuryrip", "pod", result.GetSelfLink())
		err = kuryrIps(&result, fn)
		if err != nil {
			return err
		}
	}

	return nil
}

func addList(dst []string, src []string) {
	if src == nil {
		return
	}
	var dst_m = make(map[string]struct{}, len(dst))
	for _, d := range dst {
		dst_m[d] = struct{}{}
	}
	for _, s := range src {
		if _, ok := dst_m[s]; ok {
			continue
		}
		dst = append(dst, s)
	}
}
