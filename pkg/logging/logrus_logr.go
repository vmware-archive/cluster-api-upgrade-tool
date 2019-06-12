// Copyright 2019 VMware, Inc.
// SPDX-License-Identifier: Apache-2.0

package logging

import (
	"fmt"

	"github.com/go-logr/logr"
	"github.com/sirupsen/logrus"
)

type logrusAdapter struct {
	l *logrus.Entry
}

func NewLogrusLoggerAdapter(l *logrus.Logger) logr.Logger {
	return NewLogrusEntryAdapter(logrus.NewEntry(l))
}

func NewLogrusEntryAdapter(e *logrus.Entry) logr.Logger {
	return &logrusAdapter{
		l: e,
	}
}

func (l *logrusAdapter) withFields(keysAndValues []interface{}) *logrus.Entry {
	if len(keysAndValues)%2 != 0 {
		panic("programmer error - must have an even number of keysAndValues")
	}

	fields := make(logrus.Fields, len(keysAndValues)/2)
	for i := 0; i < len(keysAndValues); i += 2 {
		k, v := keysAndValues[i], keysAndValues[i+1]
		fields[k.(string)] = v
	}

	return l.l.WithFields(fields)
}

func (l *logrusAdapter) Info(msg string, keysAndValues ...interface{}) {
	l.withFields(keysAndValues).Info(msg)
}

func (l *logrusAdapter) Enabled() bool {
	// Don't try to distinguish between info, debug, trace as far as V() levels go
	return true
}

func (l *logrusAdapter) Error(err error, msg string, keysAndValues ...interface{}) {
	l.withFields(keysAndValues).WithError(err).Error(msg)
}

func (l *logrusAdapter) V(level int) logr.InfoLogger {
	panic("not implemented")
}

func (l *logrusAdapter) WithValues(keysAndValues ...interface{}) logr.Logger {
	return NewLogrusEntryAdapter(l.withFields(keysAndValues))
}

const nameKey = "logger"

func (l *logrusAdapter) WithName(name string) logr.Logger {
	if existing := l.l.Data[nameKey]; existing != "" {
		return l.WithValues(nameKey, fmt.Sprintf("%s.%s", existing, name))
	}

	return l.WithValues(nameKey, name)
}
