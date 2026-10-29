//go:build !linux

// adr: 20261026113920
package fcvm

import "errors"

var errVolatileJournalLinux = errors.New("volatile resource journal requires Linux")

func kernelBootID() (string, error) { return "", errVolatileJournalLinux }

func onTmpfs(string) (bool, error) { return false, errVolatileJournalLinux }
