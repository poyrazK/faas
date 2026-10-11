package main

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeTraceIdentities map[string]fcvm.TraceInstanceIdentity

func (f fakeTraceIdentities) InstanceTraceIdentity(id string) (fcvm.TraceInstanceIdentity, error) {
	identity, ok := f[id]
	if !ok {
		return fcvm.TraceInstanceIdentity{}, errors.New("not serving")
	}
	return identity, nil
}

type fakeTraceApps map[string]state.App

func (f fakeTraceApps) AppByID(_ context.Context, id string) (state.App, error) {
	app, ok := f[id]
	if !ok {
		return state.App{}, state.ErrNotFound
	}
	return app, nil
}

type fakeGuestSpansClient struct {
	got     *apidpb.IngestGuestSpansRequest
	outcome string
	err     error
}

func (f *fakeGuestSpansClient) IngestGuestSpans(_ context.Context, req *apidpb.IngestGuestSpansRequest) (*apidpb.IngestGuestSpansResponse, error) {
	f.got = req
	if f.err != nil {
		return nil, f.err
	}
	return &apidpb.IngestGuestSpansResponse{Outcome: f.outcome}, nil
}

func (f *fakeGuestSpansClient) Close() error { return nil }

// runTraceBroker sends raw bytes as the guest would and returns the ack.
func runTraceBroker(t *testing.T, b *traceBroker, instance string, raw []byte) (byte, string) {
	t.Helper()
	guest, host := net.Pipe()
	defer func() { _ = guest.Close() }()
	reasonCh := make(chan string, 1)
	go func() {
		reason, _ := b.handle(context.Background(), instance, host)
		_ = host.Close()
		reasonCh <- reason
	}()
	_ = guest.SetDeadline(time.Now().Add(5 * time.Second))
	go func() { _, _ = guest.Write(raw) }()
	var ack [1]byte
	if _, err := io.ReadFull(guest, ack[:]); err != nil {
		t.Fatalf("read ack: %v", err)
	}
	return ack[0], <-reasonCh
}

func traceTestFrame(codec byte, body []byte) []byte {
	frame := make([]byte, 5+len(body))
	binary.BigEndian.PutUint32(frame[:4], uint32(1+len(body)))
	frame[4] = codec
	copy(frame[5:], body)
	return frame
}

func newTestTraceBroker(client *fakeGuestSpansClient, tracing *api.TracingConfig) *traceBroker {
	identities := fakeTraceIdentities{"vm": {AccountID: "acct", AppID: "app", DeploymentID: "dep"}}
	apps := fakeTraceApps{"app": {ID: "app", AccountID: "acct", Manifest: state.AppManifest{Tracing: tracing}}}
	b := newTraceBroker(identities, apps, client)
	b.timeout = 2 * time.Second
	return b
}

func TestTraceBrokerForwardsWithHostIdentity(t *testing.T) {
	client := &fakeGuestSpansClient{outcome: api.TraceIngestAccepted}
	b := newTestTraceBroker(client, &api.TracingConfig{Enabled: true})
	ack, reason := runTraceBroker(t, b, "vm", traceTestFrame(api.TraceCodecGzipJSON, []byte("otlp")))
	if ack != api.TraceAckAccepted || reason != api.TraceIngestAccepted {
		t.Fatalf("ack=%d reason=%q", ack, reason)
	}
	got := client.got
	if got.GetAccountId() != "acct" || got.GetAppId() != "app" || got.GetDeploymentId() != "dep" || got.GetInstanceId() != "vm" {
		t.Fatalf("identity not host-owned: %+v", got)
	}
	if got.GetCodec() != uint32(api.TraceCodecGzipJSON) || string(got.GetPayload()) != "otlp" {
		t.Fatalf("payload altered: codec=%d payload=%q", got.GetCodec(), got.GetPayload())
	}
}

func TestTraceBrokerOutcomeAcks(t *testing.T) {
	for outcome, want := range map[string]byte{
		api.TraceIngestRateLimited: api.TraceAckLimited,
		api.TraceIngestUnavailable: api.TraceAckUnavailable,
		api.TraceIngestDisabled:    api.TraceAckRejected,
		api.TraceIngestInvalid:     api.TraceAckRejected,
		"surprise":                 api.TraceAckRejected,
	} {
		b := newTestTraceBroker(&fakeGuestSpansClient{outcome: outcome}, &api.TracingConfig{Enabled: true})
		if ack, _ := runTraceBroker(t, b, "vm", traceTestFrame(api.TraceCodecProtobuf, []byte("x"))); ack != want {
			t.Fatalf("outcome %q ack=%d, want %d", outcome, ack, want)
		}
	}
	b := newTestTraceBroker(&fakeGuestSpansClient{err: errors.New("apid down")}, &api.TracingConfig{Enabled: true})
	if ack, reason := runTraceBroker(t, b, "vm", traceTestFrame(api.TraceCodecProtobuf, []byte("x"))); ack != api.TraceAckUnavailable || reason != "forward" {
		t.Fatalf("forward error ack=%d reason=%q", ack, reason)
	}
}

func TestTraceBrokerRefusesWithoutForwarding(t *testing.T) {
	oversized := make([]byte, 4)
	binary.BigEndian.PutUint32(oversized, api.TraceMaxFrameBytes+2)
	for _, tc := range []struct {
		name     string
		instance string
		tracing  *api.TracingConfig
		raw      []byte
		reason   string
	}{
		{name: "oversized", instance: "vm", tracing: &api.TracingConfig{Enabled: true}, raw: oversized, reason: "protocol"},
		{name: "codec only", instance: "vm", tracing: &api.TracingConfig{Enabled: true}, raw: traceTestFrame(api.TraceCodecProtobuf, nil), reason: "protocol"},
		{name: "unknown codec", instance: "vm", tracing: &api.TracingConfig{Enabled: true}, raw: traceTestFrame(0x7f, []byte("x")), reason: "protocol"},
		{name: "not serving", instance: "gone", tracing: &api.TracingConfig{Enabled: true}, raw: traceTestFrame(api.TraceCodecProtobuf, []byte("x")), reason: "identity"},
		{name: "tracing off", instance: "vm", tracing: &api.TracingConfig{}, raw: traceTestFrame(api.TraceCodecProtobuf, []byte("x")), reason: "disabled"},
		{name: "tracing unset", instance: "vm", raw: traceTestFrame(api.TraceCodecProtobuf, []byte("x")), reason: "disabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeGuestSpansClient{outcome: api.TraceIngestAccepted}
			b := newTestTraceBroker(client, tc.tracing)
			ack, reason := runTraceBroker(t, b, tc.instance, tc.raw)
			if ack != api.TraceAckRejected || reason != tc.reason {
				t.Fatalf("ack=%d reason=%q, want rejected/%q", ack, reason, tc.reason)
			}
			if client.got != nil {
				t.Fatal("refused frame reached apid")
			}
		})
	}
}

func TestTraceBrokerAccountMismatchRefused(t *testing.T) {
	client := &fakeGuestSpansClient{outcome: api.TraceIngestAccepted}
	identities := fakeTraceIdentities{"vm": {AccountID: "acct", AppID: "app", DeploymentID: "dep"}}
	apps := fakeTraceApps{"app": {ID: "app", AccountID: "other", Manifest: state.AppManifest{Tracing: &api.TracingConfig{Enabled: true}}}}
	b := newTraceBroker(identities, apps, client)
	if ack, _ := runTraceBroker(t, b, "vm", traceTestFrame(api.TraceCodecProtobuf, []byte("x"))); ack != api.TraceAckRejected || client.got != nil {
		t.Fatalf("mismatched account ack=%d forwarded=%v", ack, client.got != nil)
	}
}

func TestTraceBrokerBusy(t *testing.T) {
	b := newTestTraceBroker(&fakeGuestSpansClient{outcome: api.TraceIngestAccepted}, &api.TracingConfig{Enabled: true})
	for i := 0; i < cap(b.slots); i++ {
		b.slots <- struct{}{}
	}
	if ack, reason := runTraceBroker(t, b, "vm", nil); ack != api.TraceAckLimited || reason != "limited" {
		t.Fatalf("busy ack=%d reason=%q", ack, reason)
	}
}
