package utils

import (
	"context"
	"github.com/go-logr/logr"
)

const (
	loggerCtxKey = "logger"
)

func GetLoggerOrDie(ctx context.Context) logr.Logger {
	logger, ok := ctx.Value(loggerCtxKey).(logr.Logger)
	if !ok {
		panic("context didn't contain logger")
	}
	return logger
}
