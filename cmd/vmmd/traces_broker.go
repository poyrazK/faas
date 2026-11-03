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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type traceIdentityResolver interface {
	InstanceTraceIdentity(id string) (fcvm.TraceInstanceIdentity, error)
}

type traceAppReader interface {
	AppByID(ctx context.Context, id string) (state.App, error)
}

// traceBroker relays in-guest trace and log exports to apid (ADR-958,
// ADR-213 runtime adapter). Like the profile broker it only bounds frames and
// stamps host-owned identity: it never decompresses or decodes OTLP, which
// happens in unprivileged apid. The codec byte's logs bit picks the RPC.
//
// Admission is per VM first: a VM holds at most
// api.TraceMaxConcurrentUploads streams, counted from accept, so a guest that
// opens streams and then sends nothing stalls only itself. The node-wide
// forward slots are taken once a frame has been read in full, and cover only
// the app check and the apid RPC (logs/traces audit 2026-10-31 T7: one 4-slot
// node limit taken before the read let a single VM block every tenant).
type traceBroker struct {
	identities traceIdentityResolver
	apps       traceAppReader
	client     apidgrpc.GuestSpansClient
	slots      chan struct{} // node-wide forwards in flight
	timeout    time.Duration
	now        func() time.Time

	mu       sync.Mutex
	perVM    map[string]int // streams being read or forwarded, per instance
	appCache map[string]traceAppEntry
}

// traceAppEntry is the part of an app row the broker checks per frame,
// cached so a busy exporter does not read the app row for every frame.
type traceAppEntry struct {
	accountID string
	enabled   bool
	expires   time.Time
}

const (
	// traceAppCacheTTL bounds how long a tracing change on the app row
	// takes to reach the broker.
	traceAppCacheTTL = 10 * time.Second
	// traceAppCacheMax bounds the cache; a node runs far fewer apps.
	traceAppCacheMax = 4096
)

func newTraceBroker(identities traceIdentityResolver, apps traceAppReader, client apidgrpc.GuestSpansClient) *traceBroker {
	return &traceBroker{
		identities: identities,
		apps:       apps,
		client:     client,
		slots:      make(chan struct{}, api.TraceMaxConcurrentForwards),
		timeout:    api.TraceTransportTimeout,
		now:        time.Now,
		perVM:      make(map[string]int),
		appCache:   make(map[string]traceAppEntry),
	}
}

// acquireVM takes one of instance's stream slots.
func (b *traceBroker) acquireVM(instance string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.perVM[instance] >= api.TraceMaxConcurrentUploads {
		return false
	}
	b.perVM[instance]++
	return true
}

func (b *traceBroker) releaseVM(instance string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.perVM[instance] <= 1 {
		delete(b.perVM, instance)
		return
	}
	b.perVM[instance]--
}

// appTracing returns the app's account and whether its manifest enables
// tracing, from the cache while a lookup is younger than traceAppCacheTTL.
// Lookup errors are not cached.
func (b *traceBroker) appTracing(ctx context.Context, appID string) (traceAppEntry, error) {
	now := b.now()
	b.mu.Lock()
	entry, ok := b.appCache[appID]
	b.mu.Unlock()
	if ok && now.Before(entry.expires) {
		return entry, nil
	}
	app, err := b.apps.AppByID(ctx, appID)
	if err != nil {
		return traceAppEntry{}, err
	}
	entry = traceAppEntry{
		accountID: app.AccountID,
		enabled:   app.Manifest.Tracing != nil && app.Manifest.Tracing.Enabled,
		expires:   now.Add(traceAppCacheTTL),
	}
	b.mu.Lock()
	if len(b.appCache) >= traceAppCacheMax {
		for id, cached := range b.appCache {
			if !now.Before(cached.expires) {
				delete(b.appCache, id)
			}
		}
		if len(b.appCache) >= traceAppCacheMax {
			clear(b.appCache)
		}
	}
	b.appCache[appID] = entry
	b.mu.Unlock()
	return entry, nil
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
	// Released before the ack is written, so the guest bridge, which frees
	// its own slot after reading the ack, never finds this one still held.
	if !b.acquireVM(instance) {
		ack = api.TraceAckLimited
		return "limited", fmt.Errorf("trace broker busy for instance")
	}
	defer b.releaseVM(instance)
	codec, logs, payload, reason, err := readTraceFrame(conn)
	if err != nil {
		if reason == "protocol" {
			ack = api.TraceAckRejected
		}
		return reason, err
	}
	identity, err := b.identities.InstanceTraceIdentity(instance)
	if err != nil {
		ack = api.TraceAckRejected
		return "identity", err
	}
	select {
	case b.slots <- struct{}{}:
		defer func() { <-b.slots }()
	default:
		ack = api.TraceAckLimited
		return "limited", fmt.Errorf("trace broker busy")
	}
	app, err := b.appTracing(requestCtx, identity.AppID)
	if err != nil {
		return "identity", fmt.Errorf("trace app unavailable")
	}
	if app.accountID != identity.AccountID || !app.enabled {
		ack = api.TraceAckRejected
		return "disabled", fmt.Errorf("tracing disabled for instance")
	}
	req := &apidpb.IngestGuestSpansRequest{
		AccountId: identity.AccountID, AppId: identity.AppID, DeploymentId: identity.DeploymentID,
		InstanceId: instance, Codec: uint32(codec), Payload: payload,
	}
	outcome, reason, err := b.forward(requestCtx, req, logs)
	if err != nil {
		if reason == "unsupported" {
			ack = api.TraceAckRejected
		}
		return reason, err
	}
	ack = api.TraceAckForIngestOutcome(outcome)
	return outcome, nil
}

// readTraceFrame reads one length-prefixed frame within the connection's
// deadline and api.TraceMaxFrameBytes, and splits its codec byte. On error
// the reason is "read", or "protocol" for a frame that breaks the protocol.
func readTraceFrame(conn io.Reader) (codec byte, logs bool, payload []byte, reason string, err error) {
	var length [4]byte
	if _, err := io.ReadFull(conn, length[:]); err != nil {
		return 0, false, nil, "read", err
	}
	n := binary.BigEndian.Uint32(length[:])
	if n < 2 || n > api.TraceMaxFrameBytes+1 {
		return 0, false, nil, "protocol", fmt.Errorf("trace frame exceeds bounds")
	}
	frame := make([]byte, int(n))
	if _, err := io.ReadFull(conn, frame); err != nil {
		return 0, false, nil, "read", err
	}
	codec, logs = api.SplitTraceCodec(frame[0])
	if _, _, ok := api.TraceCodecMediaType(codec); !ok {
		return 0, false, nil, "protocol", fmt.Errorf("unknown trace frame codec")
	}
	return codec, logs, frame[1:], "", nil
}

// forward sends one frame to apid and returns its ingest outcome. An apid
// from before log forwarding (release N-1) cannot ingest logs and answers
// Unimplemented; the reason "unsupported" tells the caller to refuse the
// frame permanently so the SDK stops retrying.
func (b *traceBroker) forward(ctx context.Context, req *apidpb.IngestGuestSpansRequest, logs bool) (outcome, reason string, err error) {
	if !logs {
		resp, err := b.client.IngestGuestSpans(ctx, req)
		if err != nil {
			return "", "forward", err
		}
		return resp.GetOutcome(), "", nil
	}
	resp, err := b.client.IngestGuestLogs(ctx, req)
	if status.Code(err) == codes.Unimplemented {
		return "", "unsupported", err
	}
	if err != nil {
		return "", "forward", err
	}
	return resp.GetOutcome(), "", nil
}
