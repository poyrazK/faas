package storage

import (
	"errors"
	"os"
)

// osCreateTemp is a thin wrapper over os.CreateTemp that exists so
// the `//nolint:forbidigo` rationale lives next to the call. The path
// returned is from os.TempDir() + a process-unique suffix; it's a
// daemon-internal scratch file, NOT a customer path.
func osCreateTemp(dir, pattern string) (*os.File, error) {
	return os.CreateTemp(dir, pattern)
}

// removeTmp best-effort removes a tmp file. Errors are swallowed
// because the caller has already returned its own error; a leftover
// tmp file under /tmp is recoverable on next box reboot.
func removeTmp(p string) error {
	if p == "" {
		return nil
	}
	return os.Remove(p)
}

// osStat wraps os.Stat for the same doc-comment rationale.
func osStat(p string) (os.FileInfo, error) {
	return os.Stat(p)
}

// osOpen is the package's only os.Open entry point (the forbidigo
// rule on the file is broken by a single sealed-call rationale:
// every caller opens a tmp file path produced by osCreateTemp above;
// the path is a daemon-internal scratch file, NOT customer input).
//
// nolint:forbidigo // Path is from osCreateTemp in this same package;
// it's a daemon-internal scratch file under os.TempDir(), not a
// customer-derived path. Same exemption as pkg/rootfs/build.go:212
// and pkg/imaged/handler.go ApplyTarball.
func osOpen(p string) (*os.File, error) {
	return os.Open(p)
}

// errorsIs is a thin wrapper that lets isNotFoundErr be type-stable
// against the stdlib `errors.Is` semantics even if we ever swap the
// implementation (e.g. to a custom predicate over %w chains).
func errorsIs(err, target error) bool {
	return errors.Is(err, target)
}