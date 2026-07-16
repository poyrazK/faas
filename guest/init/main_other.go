//go:build !linux

package main

import (
	"fmt"
	"os"
)

// guest-init is PID 1 inside a Linux microVM; it does nothing on other platforms.
// This stub exists only so the whole tree builds on developer machines (macOS).
// The real boot logic lives in main_linux.go; its pure helpers (BuildEnv,
// Supervisor) are in app.go/supervise.go and are tested everywhere.
func main() {
	fmt.Fprintln(os.Stderr, "guest-init runs only inside a Linux microVM")
	os.Exit(1)
}
