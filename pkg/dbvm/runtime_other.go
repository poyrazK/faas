//go:build !linux

package dbvm

import (
	"errors"
	"os"
)

var errHostUnsupported = errors.New("dbvm: database VMs need a Linux host")

func (r *Runtime) prepareJail(*databaseVM, string) error { return errHostUnsupported }

func (r *Runtime) prepareSpareJail(*databaseVM, string) error { return errHostUnsupported }

func unmountJail([]string) {}

func removeJailCgroup(string, string) {}

// Recover is a no-op where database VMs cannot run.
func (r *Runtime) Recover() error { return nil }

func (r *Runtime) startConsole(*databaseVM) (*os.File, error) { return nil, errHostUnsupported }

func (r *Runtime) bindUpgradeBase(*databaseVM, string) error { return errHostUnsupported }
