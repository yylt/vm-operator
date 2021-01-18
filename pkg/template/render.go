package template

import (
	"fmt"
	"io/ioutil"
	"text/template"

	"easystack.io/vm-operator/pkg/util"

	"github.com/Masterminds/sprig"
	"github.com/tidwall/gjson"
	klog "k8s.io/klog/v2"
)

type Kind int

const (
	Lb Kind = iota
	Fip
	Vm
)

func (or Kind) String() string {
	switch or {
	case Lb:
		return "lb"
	case Vm:
		return "nova"
	case Fip:
		return "fip"
	default:
		return ""
	}
}

// Init at beginning, so ths tpls do not need locker
type Template struct {
	tpls  map[Kind]*template.Template
	funcs template.FuncMap
}

func NewTemplate() *Template {
	funcmap := sprig.TxtFuncMap()
	funcmap["toChar"] = toChar
	funcmap["intRange"] = intRange

	return &Template{
		tpls:  make(map[Kind]*template.Template),
		funcs: funcmap,
	}
}

func (t *Template) update(name Kind, filepath string) error {
	bs, err := ioutil.ReadFile(filepath)
	if err != nil {
		return err
	}
	t.tpls[name], err = template.New("").Funcs(t.funcs).Parse(string(bs))
	if err != nil {
		return err
	}
	return nil
}

func (t *Template) AddTempFileMust(name Kind, filepath string) {
	err := t.update(name, filepath)
	if err != nil {
		panic(err)
	}
	klog.Infof("add template type:%v, filepath:%v", name.String(), filepath)
	return
}

func (t *Template) RenderByName(name Kind, params interface{}) ([]byte, error) {
	buf := util.GetBuf()
	defer util.PutBuf(buf)
	if v, ok := t.tpls[name]; ok {
		err := v.Execute(buf, params)
		bs := buf.Bytes()
		return bs, err
	}
	err := fmt.Errorf("not found template by name:%v", name.String())
	return nil, err
}

//97 - a
func toChar(v interface{}) string {
	switch v := v.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case int:
		return fmt.Sprintf("%c", rune(v))
	case int32:
		return fmt.Sprintf("%c", rune(v))
	case int64:
		return fmt.Sprintf("%c", rune(v))
	default:
	}
	return ""
}

//intRange
func intRange(end interface{}) []int32 {
	var i int64
	switch v := end.(type) {
	case string:
		return nil
	case []byte:
		return nil
	case int:
		i = int64(v)
	case int32:
		i = int64(v)
	case int64:
		i = int64(v)
	default:
		return nil
	}

	result := make([]int32, i)
	for k := 0; int64(k) < i; k++ {
		result[k] = int32(k)
	}
	return result
}

func Parse(result gjson.Result) interface{} {
	if result.IsArray() {
		var rets []interface{}
		result.ForEach(func(_, value gjson.Result) bool {
			rets = append(rets, Parse(value))
			return true
		})
		return rets
	}
	if result.IsObject() {
		var rets = make(map[string]interface{})
		result.ForEach(func(key, value gjson.Result) bool {
			if key.Type == gjson.String {
				rets[key.String()] = Parse(value)
			}
			return true
		})
		return rets
	}
	switch result.Type {
	case gjson.False:
		return false
	case gjson.Number:
		return result.Int()
	case gjson.String:
		return result.String()
	case gjson.True:
		return true
	default:
		return ""
	}
}
