//go:build !linux

// Stub for non-linux builds. Mirrors the listen_resume_linux_other.go
// pattern: the proxy is a no-op so the boot() caller compiles on
// every platform. See guest/init/framework_ready_proxy_linux.go for
// the real implementation.

package main

import "log/slog"

// startFrameworkReadyProxy is a no-op on non-linux platforms. The
// platform contract is "no signal" not "won't boot", so the
// boot() caller logs at Warn and continues.
func startFrameworkReadyProxy(_ *slog.Logger, _ int) error {
	return nil
}

var _ = startFrameworkReadyProxy
