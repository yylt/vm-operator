package manage

import (
	"net"
	"sort"
	"testing"
)

func TestSortResources(t *testing.T) {
	rest := Results([]*Result{
		&Result{
			Ip:      net.ParseIP("1.1.1.1"),
			PodName: "pod1",
		},
		&Result{
			Ip:      net.ParseIP("4.4.4.41"),
			PodName: "pod4",
		},
		&Result{
			Ip:      net.ParseIP("2.2.2.2"),
			PodName: "pod2",
		},
		&Result{
			Ip:      net.ParseIP("3.3.3.31"),
			PodName: "pod3",
		},
	})
	for i, v := range rest {
		t.Logf("index %d, data:%v", i, v)
	}
	sort.Sort(rest)
	t.Log("after sort")
	for i, v := range rest {
		t.Logf("index %d, data:%v", i, v)
	}

}
