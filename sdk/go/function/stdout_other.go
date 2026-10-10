//go:build !linux

package function

import (
	"io"
	"os"
)

// reserveStdout keeps the original stdout for the protocol and sends later
// os.Stdout writes to stderr. The runner is Linux-only; this variant keeps
// local builds on other systems working.
func reserveStdout() io.Writer {
	out := os.Stdout
	os.Stdout = os.Stderr
	return out
}
