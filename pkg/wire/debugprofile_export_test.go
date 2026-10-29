// Test-only exports for the debug profiling endpoint (ADR-20261023200002).

package wire

import (
	"context"
	"log/slog"
	"net"
)

// DebugProfilingEnabled exposes the kill-switch parsing.
func DebugProfilingEnabled(getenv func(string) string) bool { return debugProfilingEnabled(getenv) }

// ValidDebugProfileName exposes the socket-name check.
func ValidDebugProfileName(name string) bool { return validDebugProfileName(name) }

// ListenDebugProfile exposes the platform listener.
func ListenDebugProfile(name string, log *slog.Logger) (net.Listener, string, error) {
	return listenDebugProfile(name, log)
}

// StartDebugProfiling exposes the BootStamps hook.
func StartDebugProfiling(ctx context.Context, name string, log *slog.Logger) {
	startDebugProfiling(ctx, name, log)
}
