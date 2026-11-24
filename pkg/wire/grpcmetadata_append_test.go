package wire

import (
	"context"
	"reflect"
	"testing"

	"google.golang.org/grpc/metadata"
)

// adr: 570
//
// TestWithCorrelationOutgoingMatchesJoin pins that appending the envelope
// into FromOutgoingContext's copy leaves exactly the metadata the previous
// Join(prev, Pairs(...)) form produced, including when prior metadata already
// carries the same keys.
func TestWithCorrelationOutgoingMatchesJoin(t *testing.T) {
	fields := CorrelationFields{RequestID: "req-1", AppID: "app-1", NodeID: "n1", Trigger: "gateway", QueuedCount: 3}
	for name, prev := range map[string]metadata.MD{
		"no prior metadata": nil,
		"unrelated prior":   metadata.Pairs("authorization", "bearer x", "x-other", "1"),
		"same keys prior":   metadata.Pairs(mdKeyRequestID, "old", mdKeyAppID, "old-app", "x-other", "1"),
	} {
		ctx := context.Background()
		if prev != nil {
			ctx = metadata.NewOutgoingContext(ctx, prev)
		}
		want := metadata.Pairs(correlationPairs(fields)...)
		if p, ok := metadata.FromOutgoingContext(ctx); ok {
			want = metadata.Join(p, want)
		}
		got, _ := metadata.FromOutgoingContext(WithCorrelationOutgoing(ctx, fields))
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s:\n got %v\nwant %v", name, got, want)
		}
		// The caller's metadata is never modified.
		if prev != nil {
			if again, _ := metadata.FromOutgoingContext(ctx); !reflect.DeepEqual(again, prev) {
				t.Errorf("%s: prior metadata changed to %v", name, again)
			}
		}
	}
}

// TestWithRequestCorrelationOutgoingReplacesEnvelope pins that reserved keys
// from a prior hop are replaced (not appended to) and unrelated transport
// metadata is kept.
func TestWithRequestCorrelationOutgoingReplacesEnvelope(t *testing.T) {
	ctx := metadata.NewOutgoingContext(context.Background(),
		metadata.Pairs(mdKeyRequestID, "prior-hop", "authorization", "bearer x"))
	ctx = WithContext(ctx, CorrelationFields{RequestID: "req-2", AppID: "app-2"})
	got, _ := metadata.FromOutgoingContext(WithRequestCorrelationOutgoing(ctx))
	want := metadata.MD{mdKeyRequestID: {"req-2"}, mdKeyAppID: {"app-2"}, "authorization": {"bearer x"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// Without prior outgoing metadata.
	got, _ = metadata.FromOutgoingContext(WithRequestCorrelationOutgoing(WithContext(context.Background(), CorrelationFields{RequestID: "r"})))
	if !reflect.DeepEqual(got, metadata.MD{mdKeyRequestID: {"r"}}) {
		t.Fatalf("no prior metadata: got %v", got)
	}
}

// TestForwardContextWithoutPriorMetadata pins that ForwardContext works when
// ctx carries no outgoing metadata (it used to rely on MD.Copy of nil
// returning an empty map) and replaces a caller-supplied private mark.
func TestForwardContextWithoutPriorMetadata(t *testing.T) {
	got, ok := metadata.FromOutgoingContext(ForwardContext(WithPrivateForward(context.Background())))
	if !ok || !reflect.DeepEqual(got, metadata.MD{privateForwardKey: {"1"}}) {
		t.Fatalf("private: got %v ok=%v", got, ok)
	}
	got, _ = metadata.FromOutgoingContext(ForwardContext(context.Background()))
	if len(got) != 0 {
		t.Fatalf("public: got %v, want empty", got)
	}
	spoofed := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(privateForwardKey, "1"))
	if got, _ := metadata.FromOutgoingContext(ForwardContext(spoofed)); len(got.Get(privateForwardKey)) != 0 {
		t.Fatalf("caller-supplied private mark kept: %v", got)
	}
}
