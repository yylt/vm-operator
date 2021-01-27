package util

import (
	"errors"
	"strings"
)

type Errs strings.Builder

func NewErr() Errs {
	return Errs{}
}

func (e Errs) Add(err error) Errs {
	if err == nil {
		return e
	}

	ebuf := strings.Builder(e)
	ebuf.WriteString(err.Error())
	return e
}

func (e Errs) Error() error {
	ebuf := strings.Builder(e)
	if ebuf.Len() == 0 {
		return nil
	}
	return errors.New(ebuf.String())
}
