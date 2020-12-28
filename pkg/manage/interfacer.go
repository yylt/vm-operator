package manage

import (
	goctx "context"
)

type context struct {
	ctx goctx.Context
}

type Processor interface {
	Process(ctx context) error
}

// TODO: add require on Processor,
// sort Processor by require (Type: xxx, Name: xxx)
