// Tests for the gRPC-side correlation helpers (issue #517, PR-A). The wire
// shape — six x-faas-* metadata keys — is the contract the schedd → vmmd
// boundary depends on. The tests use the in-memory gRPC metadata API directly
// (no real gRPC connection); the package's FromIncomingContext /
// NewOutgoingContext are the only grpc primitives we need.

package wire_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/metadata"
)

func TestCorrelationRoundTrip(t *testing.T) {
	// Simulate the schedd → vmmd hop. In production, gRPC serialises
	// the outgoing MD to headers and the receiving server
	// deserialises them via FromIncomingContext — this test simulates
	// that round-trip without a real connection so a regression in the
	// wire semantics fails fast and the test stays under -race.
	incoming := metadata.New(map[string]string{
		"x-faas-request-id": "req-from-gatewayd",
	})
	serverCtx := metadata.NewIncomingContext(context.Background(), incoming)

	lifted, ok := wire.CorrelationFromIncoming(serverCtx)
	if !ok {
		t.Fatal("server-side read returned ok=false")
	}
	if lifted.RequestID != "req-from-gatewayd" {
		t.Errorf("RequestID = %q, want req-from-gatewayd", lifted.RequestID)
	}

	// Engine.Wake mints wake_id; the engine layer joins it onto the
	// lifted fields and forwards to vmmd.
	lifted.WakeID = "wake-minted"
	lifted.AppID = "app-1"
	clientCtx := wire.WithCorrelationOutgoing(context.Background(), lifted)

	// Simulate the server side of the receiving vmmd by hand:
	// re-wrap the outbound MD as inbound so the helper exercises
	// its FromIncomingContext path. Real gRPC does this in the
	// transport layer.
	got, ok := wire.CorrelationFromIncoming(metadata.NewIncomingContext(clientCtx, mustOutgoingMD(t, clientCtx)))
	if !ok {
		t.Fatal("client-side read returned ok=false")
	}
	want := wire.CorrelationFields{
		RequestID: "req-from-gatewayd",
		WakeID:    "wake-minted",
		AppID:     "app-1",
	}
	if got != want {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", got, want)
	}
}

// mustOutgoingMD extracts the outbound metadata carrier — used to simulate
// the network layer between two processes in a unit test. Real gRPC does
// this transparently.
func mustOutgoingMD(t *testing.T, ctx context.Context) metadata.MD {
	t.Helper()
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		t.Fatal("ctx has no outgoing metadata")
	}
	return md
}

func TestWithCorrelationOutgoing_SkipsEmptyFields(t *testing.T) {
	ctx := wire.WithCorrelationOutgoing(context.Background(), wire.CorrelationFields{
		RequestID: "req-1",
		// All other fields are empty.
	})
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		t.Fatal("no outgoing metadata")
	}
	if got := md.Get("x-faas-request-id"); len(got) != 1 || got[0] != "req-1" {
		t.Errorf("request-id missing or wrong: %v", got)
	}
	for _, key := range []string{
		"x-faas-wake-id",
		"x-faas-app-id",
		"x-faas-deployment-id",
		"x-faas-instance-id",
		"x-faas-invocation-id",
		"x-faas-trace-id",
		"x-faas-span-id",
	} {
		if got := md.Get(key); len(got) > 0 {
			t.Errorf("expected empty key %q to be skipped, got %v", key, got)
		}
	}
}

func TestWithCorrelationOutgoing_NoFieldsNoOp(t *testing.T) {
	// All fields empty — the helper must NOT add a metadata layer (an
	// empty MD would still mutate the context value, which is observable
	// to FromOutgoingContext callers and undesirable).
	ctx := wire.WithCorrelationOutgoing(context.Background(), wire.CorrelationFields{})
	if _, ok := metadata.FromOutgoingContext(ctx); ok {
		t.Error("empty correlation produced an outgoing MD layer")
	}
}

func TestWithCorrelationOutgoing_JoinsWithExistingLayer(t *testing.T) {
	// A pre-existing outgoing MD must be preserved (the helper uses
	// metadata.Join rather than replace) so transport-level metadata
	// such as trace headers set by other middleware is not dropped.
	base := metadata.NewOutgoingContext(context.Background(),
		metadata.Pairs("x-trace-id", "trace-1"))

	ctx := wire.WithCorrelationOutgoing(base, wire.CorrelationFields{
		RequestID: "req-1",
	})
	md := mustOutgoingMD(t, ctx)
	if got := md.Get("x-faas-request-id"); len(got) != 1 || got[0] != "req-1" {
		t.Errorf("correlation not added: %v", got)
	}
	if got := md.Get("x-trace-id"); len(got) != 1 || got[0] != "trace-1" {
		t.Errorf("pre-existing MD dropped: %v", got)
	}
}

func TestCorrelationFromIncoming_NilCtx(t *testing.T) {
	// Use a typed nil context (interface containing untyped nil) to
	// exercise the helper's documented nil-safety without tripping
	// staticcheck's SA1012 (do not pass a nil Context, even if the
	// function permits it). The helper accepts nil; the test passes
	// a nil through an interface variable so the linter sees a typed
	// value at the call site.
	var nilCtx context.Context // interface nil
	if _, ok := wire.CorrelationFromIncoming(nilCtx); ok {
		t.Error("nil ctx returned ok=true")
	}
}

func TestCorrelationFromIncoming_NoMetadataLayer(t *testing.T) {
	if _, ok := wire.CorrelationFromIncoming(context.Background()); ok {
		t.Error("ctx with no MD returned ok=true")
	}
}

func TestCorrelationFromIncoming_OnlySomeFieldsSet(t *testing.T) {
	// A subset of fields set in the MD must produce a partial struct
	// with ok=true and the unset fields at their zero value.
	md := metadata.New(map[string]string{
		"x-faas-wake-id": "wake-1",
		// request_id / app_id absent
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)
	got, ok := wire.CorrelationFromIncoming(ctx)
	if !ok {
		t.Fatal("ok=false with at least one field set")
	}
	if got.WakeID != "wake-1" {
		t.Errorf("WakeID = %q, want wake-1", got.WakeID)
	}
	if got.RequestID != "" || got.AppID != "" {
		t.Errorf("expected empty RequestID/AppID, got %+v", got)
	}
}

// TestCorrelationRoundTrip_SanitizesAtLift (item 7 review feedback):
// even when a malicious peer smuggles a control char into MD, the
// helper chain (lift → WithCorrelationFields) must keep the
// sanitization on the slog side. This pins the contract end-to-end at
// the wire level.
func TestCorrelationRoundTrip_SanitizesAtLift(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	md := metadata.New(map[string]string{
		// newline injection attempt
		"x-faas-wake-id": "wake\nINJECT",
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	fields, ok := wire.CorrelationFromIncoming(ctx)
	if !ok {
		t.Fatal("lift returned ok=false")
	}

	streamLogger := wire.WithCorrelationFields(base, fields)
	streamLogger.Info("vmmd: stream opened")

	line := strings.TrimSpace(buf.String())
	if strings.Contains(line, "wake\nINJECT") {
		t.Errorf("emitted record contains raw newline: %q", line)
	}
	// The sanitized form (U+00B7 for the control char) must be
	// present instead.
	if !strings.Contains(line, "wake·INJECT") {
		t.Errorf("expected sanitized wake_id with middle dot, got %q", line)
	}
}
