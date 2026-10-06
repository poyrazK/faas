package gatewayconfirmation

// adr: 609

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type heartbeatProbe struct {
	slot, session string
	called        chan struct{}
	bounded       bool
}

func (p *heartbeatProbe) HeartbeatRuntimeUpgradeGateway(ctx context.Context, slot, session string) error {
	p.slot, p.session = slot, session
	deadline, ok := ctx.Deadline()
	p.bounded = ok && time.Until(deadline) <= api.RuntimeUpgradeGatewayRepairTimeout
	close(p.called)
	<-ctx.Done()
	return ctx.Err()
}

func TestGatewayHeartbeatStartsImmediatelyWithBoundedCancellation(t *testing.T) {
	p := &heartbeatProbe{called: make(chan struct{})}
	slot, session := uuid.NewString(), uuid.NewString()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		RunHeartbeat(ctx, p, slot, session, slog.New(slog.NewTextHandler(io.Discard, nil)))
		close(done)
	}()
	select {
	case <-p.called:
	case <-time.After(3 * time.Second):
		t.Fatal("heartbeat waited for repair or first tick")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not cancel in-flight database call")
	}
	if p.slot != slot || p.session != session || !p.bounded {
		t.Fatal("heartbeat changed identity or lacked a deadline", p)
	}
}
