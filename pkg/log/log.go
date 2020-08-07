package log

import (
	ctrl "sigs.k8s.io/controller-runtime"
)

func Info(msg string, kvs ...interface{}) {
	ctrl.Log.Info(msg, kvs...)
}

func Error(err error, msg string, kvs ...interface{}) {
	ctrl.Log.Error(err, msg, kvs...)
}
