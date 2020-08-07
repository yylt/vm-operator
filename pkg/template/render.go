package template

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"sync"
	"text/template"

	"github.com/Masterminds/sprig"
	"github.com/go-logr/logr"
	"github.com/tidwall/gjson"
)

var (
	bufpool = sync.Pool{
		New: func() interface{} {
			return new(bytes.Buffer)
		},
	}
)

type Template struct {
	tpls  map[string]*template.Template
	funcs template.FuncMap
	mu    sync.RWMutex
	log   logr.Logger
}

func NewTemplate(logger logr.Logger) *Template {
	funcmap := sprig.TxtFuncMap()
	funcmap["toChar"] = toChar
	funcmap["intRange"] = intRange

	return &Template{
		tpls:  make(map[string]*template.Template),
		mu:    sync.RWMutex{},
		log:   logger,
		funcs: funcmap,
	}
}

func (t *Template) update(name, filepath string) error {
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

func (t *Template) AddTempFileMust(name string, filepath string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.tpls[name]; ok {
		t.log.Info("render engine", "update name", name)
	}
	err := t.update(name, filepath)
	if err != nil {
		panic(err)
	}
	t.log.Info("render engine", "add name", name, "filepath", filepath)
	return
}

func (t *Template) RenderByName(name string, params interface{}) ([]byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	buf := bufpool.Get().(*bytes.Buffer)
	buf.Reset()
	defer bufpool.Put(buf)
	if v, ok := t.tpls[name]; ok {
		err := v.Execute(buf, params)
		bs := buf.Bytes()
		return bs, err
	}
	err := fmt.Errorf("%s not found template", name)
	t.log.Error(err, "render tpl failed", "name", name)
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
func intRange(end int64) []int64 {
	var i int64
	n := end
	result := make([]int64, n)
	for i = 0; i < n; i++ {
		result[i] = i
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
