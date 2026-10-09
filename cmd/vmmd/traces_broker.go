package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apidgrpc"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

type traceIdentityResolver interface {
	InstanceTraceIdentity(id string) (fcvm.TraceInstanceIdentity, error)
}

type traceAppReader interface {
	AppByID(ctx context.Context, id string) (state.App, error)
}

// traceBroker relays in-guest trace exports to apid (ADR-829). Like the
// profile broker it only bounds frames and stamps host-owned identity: it
// never decompresses or decodes OTLP, which happens in unprivileged apid.
type traceBroker struct {
	identities traceIdentityResolver
	apps       traceAppReader
	client     apidgrpc.GuestSpansClient
	slots      chan struct{}
	timeout    time.Duration
}

func newTraceBroker(identities traceIdentityResolver, apps traceAppReader, client apidgrpc.GuestSpansClient) *traceBroker {
	return &traceBroker{identities: identities, apps: apps, client: client, slots: make(chan struct{}, api.TraceMaxConcurrentUploads), timeout: api.TraceTransportTimeout}
}

// newTraceProblemLogger logs refused or failed guest trace frames at most
// once per reason per 30 seconds; accepted frames are counted by the guest
// vsock transport metric instead.
func newTraceProblemLogger(log *slog.Logger, now func() time.Time) func(instance, reason string, err error) {
	var mu sync.Mutex
	last := map[string]time.Time{}
	return func(instance, reason string, err error) {
		if err == nil && reason == api.TraceIngestAccepted {
			return
		}
		mu.Lock()
		at, seen := last[reason]
		if seen && now().Sub(at) < 30*time.Second {
			mu.Unlock()
			return
		}
		last[reason] = now()
		mu.Unlock()
		log.Warn("guest trace frame not ingested", "instance", instance, "reason", reason, "err", err)
	}
}

// handle serves one frame and always writes exactly one ack byte. The
// returned reason labels the guest vsock transport metric.
func (b *traceBroker) handle(ctx context.Context, instance string, conn net.Conn) (string, error) {
	requestCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	_ = conn.SetDeadline(time.Now().Add(b.timeout))
	ack := api.TraceAckUnavailable
	defer func() { _, _ = conn.Write([]byte{ack}) }()
	select {
	case b.slots <- struct{}{}:
		defer func() { <-b.slots }()
	default:
		ack = api.TraceAckLimited
		return "limited", fmt.Errorf("trace broker busy")
	}
	var length [4]byte
	if _, err := io.ReadFull(conn, length[:]); err != nil {
		return "read", err
	}
	n := binary.BigEndian.Uint32(length[:])
	if n < 2 || n > api.TraceMaxFrameBytes+1 {
		ack = api.TraceAckRejected
		return "protocol", fmt.Errorf("trace frame exceeds bounds")
	}
	frame := make([]byte, int(n))
	if _, err := io.ReadFull(conn, frame); err != nil {
		return "read", err
	}
	codec := frame[0]
	if _, _, ok := api.TraceCodecMediaType(codec); !ok {
		ack = api.TraceAckRejected
		return "protocol", fmt.Errorf("unknown trace frame codec")
	}
	identity, err := b.identities.InstanceTraceIdentity(instance)
	if err != nil {
		ack = api.TraceAckRejected
		return "identity", err
	}
	app, err := b.apps.AppByID(requestCtx, identity.AppID)
	if err != nil {
		return "identity", fmt.Errorf("trace app unavailable")
	}
	if app.AccountID != identity.AccountID || app.Manifest.Tracing == nil || !app.Manifest.Tracing.Enabled {
		ack = api.TraceAckRejected
		return "disabled", fmt.Errorf("tracing disabled for instance")
	}
	resp, err := b.client.IngestGuestSpans(requestCtx, &apidpb.IngestGuestSpansRequest{
		AccountId: identity.AccountID, AppId: identity.AppID, DeploymentId: identity.DeploymentID,
		InstanceId: instance, Codec: uint32(codec), Payload: frame[1:],
	})
	if err != nil {
		return "forward", err
	}
	ack = api.TraceAckForIngestOutcome(resp.GetOutcome())
	return resp.GetOutcome(), nil
}
