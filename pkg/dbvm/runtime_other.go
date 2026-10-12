//go:build !linux

package dbvm

import "errors"

var errHostUnsupported = errors.New("dbvm: database VMs need a Linux host")

func (r *Runtime) prepareJail(*databaseVM, string) error { return errHostUnsupported }

func unmountJail([]string) {}

func removeJailCgroup(string, string) {}

// Recover is a no-op where database VMs cannot run.
func (r *Runtime) Recover() error { return nil }
