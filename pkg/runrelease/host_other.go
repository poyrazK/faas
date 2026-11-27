//go:build !linux

package runrelease

import (
	"context"
	"runtime"
)

func ProbeHost(context.Context) Host {
	return Host{OS: runtime.GOOS, Architecture: runtime.GOARCH, Virtualization: "unsupported"}
}
