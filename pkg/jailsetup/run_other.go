//go:build !linux

package jailsetup

// Run reports that jail device setup is unsupported on this platform.
func Run(_ []string) bool { return false }
