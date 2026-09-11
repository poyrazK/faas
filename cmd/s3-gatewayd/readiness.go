package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

type s3ReadinessPinger interface {
	Ping(context.Context) error
}

// buildReadinessProbe is shared by the operator /readyz endpoint and the
// systemd READY=1 bridge. A daemon must not advertise readiness through one
// path while the other reports a dependency failure.
func buildReadinessProbe(ctx context.Context, pool s3ReadinessPinger, every time.Duration) *wire.ReadyzProbe {
	probe := &wire.ReadyzProbe{}
	if pool == nil {
		signal := probe.Register()
		signal.Set(false, "database pool unavailable")
		return probe
	}
	signal, stop := wire.NewPGPingSignal(ctx, pool, every)
	probe.RegisterSignal(signal, stop)
	return probe
}
