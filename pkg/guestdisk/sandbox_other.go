//go:build !linux

package guestdisk

import (
	"fmt"
	"os"
	"syscall"
)

func sandboxSysProcAttr() *syscall.SysProcAttr { return nil }

// SandboxMain fails: the sandbox needs Linux namespaces and Landlock.
func SandboxMain([]string) int {
	fmt.Fprintln(os.Stderr, "vmmd-guestdisk: the sandbox requires Linux")
	return helperFailureExit
}

// LandlockABI reports 0: Landlock is Linux-only.
func LandlockABI() int { return 0 }
