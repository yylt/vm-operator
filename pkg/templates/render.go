package templates

import (
	"fmt"
	"github.com/flosch/pongo2"
	"io/ioutil"
	"os"
	"strings"
)

type VmTemplate struct {
	SrcTplDir string
	DstTplDir string
	TplCtx    pongo2.Context
}

func New(srcTplDir string, dstTplDir string, tplCtx pongo2.Context) *VmTemplate {
	return &VmTemplate{
		SrcTplDir: srcTplDir,
		DstTplDir: dstTplDir,
		TplCtx:    tplCtx,
	}
}

func (vt *VmTemplate) RenderToFile() error {
	tf, err := vt.getRawTemplateFiles()
	if err != nil {
		fmt.Println("Fail to get vm django template files")
		return err
	}
	for _, fName := range tf {
		rawTpl := strings.Join([]string{vt.SrcTplDir, fName}, "/")
		tpl, err := pongo2.FromFile(rawTpl)
		if err != nil {
			return err
		}

		out, err := tpl.Execute(vt.TplCtx)
		if err != nil {
			return err
		}

		// write render result to template file
		err = os.MkdirAll(vt.DstTplDir, 0755)
		if err != nil {
			fmt.Printf("mkdir failed: %s", vt.DstTplDir)
			return err
		}
		cookedTpl := strings.Join([]string{vt.DstTplDir, fName}, "/")
		err = ioutil.WriteFile(cookedTpl, []byte(out), 0644)
		if err != nil {
			return err
		}
	}

	return nil
}

func (vt *VmTemplate) getRawTemplateFiles() ([]string, error) {
	tf := make([]string, 0, 10)
	files, err := ioutil.ReadDir(vt.SrcTplDir)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		tf = append(tf, f.Name())
	}

	return tf, nil
}
