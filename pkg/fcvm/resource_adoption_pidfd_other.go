//go:build !linux

package fcvm

// Guests run only on Linux; elsewhere there is no pidfd to check.
var adoptionPidfdCheck func(pid int, want resourceProcessIdentity) error
