package controllers

import (
	"bytes"
	"context"
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
	"k8s.io/client-go/dynamic"
)

type info struct {
	ips  map[string]struct{}
	link string

	portmap map[int32]string // should add

	hashid   uint64
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

	for i, _ := range val.ips {
		buf.WriteString(i)
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
					if val.isdelete == true {
						err = p.syncsvc(lbip, val)
						if err != nil {
							p.logger.Error(err, "delete service failed, but ignore!", "lbip", lbip)
						}
						delete(p.lbinfo, lbip)
						continue
					}

					err = getPodSecondIps(p.logger, p.client, val.link, func(ip string) {
						val.ips[ip] = struct{}{}
					})
					if err != nil {
						p.logger.Error(err, "get ips failed", "link", val.link)
					}

					err = p.syncsvc(lbip, val)
					if err != nil {
						p.logger.Error(err, "sync service failed", "lbip", lbip)
					}
					val.hashid = hashid
				}
				p.mu.RUnlock()
			}
		}
	}()
}

func (p *PodIp) syncsvc(lbip string, val *info) error {
	if len(val.ips) == 0 {
		return nil
	}
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
		p.logger.Info("get link label failed", "lbip", lbip, "error", err)
		return err
	}

	if val.isdelete == true {
		err = p.client.Resource(gvk).Namespace(res.namespace).Delete(res.svcname, &metav1.DeleteOptions{})
		if err != nil {
			p.logger.Info("delete svc failed", "name", lbip, "error", err)
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
			_, err = p.client.Resource(gvk).Namespace(res.namespace).Update(svcunstruct, metav1.UpdateOptions{})
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
		val.isdelete = true
	}
}

func (p *PodIp) AddLinks(lbip string, link string, portmap map[int32]string) {
	if link == "" || len(portmap) == 0 {
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
			ips:     make(map[string]struct{}),
			portmap: portmap,
			link:    link,
		}
	}
}

func (p *PodIp) SecondIp(lbip string) []string {
	var ips []string
	p.mu.RLock()
	defer p.mu.RUnlock()

	if v, ok := p.lbinfo[lbip]; ok {
		for ip, _ := range v.ips {
			ips = append(ips, ip)
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
			"kind":       "service",
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

func kuryrIps(object *unstructured.Unstructured, fn func(string)) error {

	networks, found, err := unstructured.NestedString(object.Object, "metadata", "annotations", "k8s.v1.cni.cncf.io/networks-status")
	if err != nil || !found {
		return fmt.Errorf("not found networks-status in annotations")
	}
	jsondata := gjson.Parse(networks)
	if !jsondata.IsArray() {
		return fmt.Errorf("the networks-status in annotations is not list")
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
					fn(tmpip.String())

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
		err = fmt.Errorf("labels not found for resource %s/%s, error=%s", wk.GetNamespace(), wk.GetName(), err)
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

func getPodSecondIps(logger logr.Logger, client dynamic.Interface, link string, fn func(string)) error {
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
		return fmt.Errorf("list pods failed, label=%s, err=%s", buf.String(), err)
	}
	logger.Info("list pods on resource", "resource", link, "label", buf.String())

	for _, result := range results.Items {
		logger.Info("try find kuryrip", "pod", result.GetSelfLink())
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
