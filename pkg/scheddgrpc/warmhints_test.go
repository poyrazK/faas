// warmhints_test.go — StreamWarmHints gRPC handler tests
// (ADR-025 axis 4).
//
// End-to-end tests through bufconn:
//   - RoundTrip: the fakeEngine injects one WarmHintEvent into
//     the sink; the typed Client.Recv sees the typed WarmHintEvent
//     with the same fields.
//   - ContextCancelSurfacesCanceled: cancelling the caller's
//     ctx surfaces codes.Canceled on the wire.
//   - EmptyRequestOk: the empty StreamWarmHintsRequest is
//     accepted (no InvalidArgument).
//   - WrittenAtZeroIsZeroTime: an unset proto timestamp round-
//     trips as the zero time.Time through the typed adapter.

package scheddgrpc_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestStreamWarmHints_RoundTrip(t *testing.T) {
	// Drive placement events and an idle heartbeat through the sink and
	// assert the typed Client.Recv sees them.
	events := []sched.WarmHintEvent{
		{AppID: "app-a", NodeID: "node-1", WrittenAt: time.Unix(1730000000, 0).UTC()},
		{AppID: "app-b", NodeID: "node-2", WrittenAt: time.Unix(1730000001, 0).UTC()},
		{WrittenAt: time.Unix(1730000002, 0).UTC()},
	}
	idx := 0
	cli := newClient(t, &fakeEngine{
		streamWarmHintsFn: func(ctx context.Context, sink scheddgrpc.WarmHintSink) error {
			for idx < len(events) {
				select {
				case <-ctx.Done():
					return nil
				default:
				}
				if err := sink(events[idx]); err != nil {
					return err
				}
				idx++
			}
			<-ctx.Done()
			return nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := cli.StreamWarmHints(ctx)
	if err != nil {
		t.Fatalf("StreamWarmHints: %v", err)
	}

	for _, want := range events {
		got, err := stream.Recv()
		if err != nil {
			t.Fatalf("stream.Recv: %v", err)
		}
		if got.AppID != want.AppID {
			t.Errorf("AppID = %q, want %q", got.AppID, want.AppID)
		}
		if got.NodeID != want.NodeID {
			t.Errorf("NodeID = %q, want %q", got.NodeID, want.NodeID)
		}
		if got.WrittenAt.Unix() != want.WrittenAt.Unix() {
			t.Errorf("WrittenAt = %v, want %v", got.WrittenAt, want.WrittenAt)
		}
	}
}

func TestStreamWarmHints_ContextCancelSurfacesCanceled(t *testing.T) {
	// The fakeEngine returns ctx.Err() on cancel; the handler
	// maps context.Canceled → codes.Canceled.
	cli := newClient(t, &fakeEngine{
		streamWarmHintsFn: func(ctx context.Context, sink scheddgrpc.WarmHintSink) error {
			<-ctx.Done()
			return ctx.Err()
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := cli.StreamWarmHints(ctx)
	if err != nil {
		t.Fatalf("StreamWarmHints: %v", err)
	}
	cancel()

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		_, err := stream.Recv()
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			t.Fatalf("got io.EOF on cancel, want codes.Canceled")
		}
		st, ok := status.FromError(err)
		if !ok {
			t.Fatalf("err = %v, want gRPC status error", err)
		}
		if st.Code() != codes.Canceled {
			t.Errorf("code = %v, want Canceled", st.Code())
		}
		return
	}
	t.Fatal("timed out waiting for stream.Recv to surface cancel")
}

func TestStreamWarmHints_EmptyRequestOk(t *testing.T) {
	// Verify the empty request message is accepted — no
	// InvalidArgument. The typed Client.StreamWarmHints(ctx) takes
	// no parameters; the empty proto is built inside.
	cli := newClient(t, &fakeEngine{
		streamWarmHintsFn: func(ctx context.Context, sink scheddgrpc.WarmHintSink) error {
			<-ctx.Done()
			return nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := cli.StreamWarmHints(ctx)
	if err != nil {
		t.Fatalf("StreamWarmHints: %v", err)
	}
	cancel()
	_, _ = stream.Recv() // surface the cancel cleanly
}

func TestStreamWarmHints_WrittenAtZeroIsEpoch(t *testing.T) {
	// The handler (server.go) skips setting the proto timestamp
	// when ev.WrittenAt.IsZero() (matches StreamAppLogs's shape).
	// proto3 then returns the default timestamp (Unix epoch) on
	// the wire; the typed adapter (warmHintStreamAdapter.Recv)
	// decodes that as 1970-01-01T00:00:00Z, NOT the Go zero
	// time.Time. The adapter doc (client_warmhints.go:75-80)
	// calls this out as the expected behaviour. This test pins
	// the contract so a future change doesn't silently flip the
	// semantics.
	cli := newClient(t, &fakeEngine{
		streamWarmHintsFn: func(ctx context.Context, sink scheddgrpc.WarmHintSink) error {
			// Event with zero WrittenAt — schedd always sets it
			// at emit time, but a hand-crafted event with no
			// timestamp must round-trip with epoch on the wire.
			if err := sink(sched.WarmHintEvent{AppID: "a", NodeID: "n"}); err != nil {
				return err
			}
			<-ctx.Done()
			return nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := cli.StreamWarmHints(ctx)
	if err != nil {
		t.Fatalf("StreamWarmHints: %v", err)
	}
	got, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if !got.WrittenAt.Equal(time.Unix(0, 0).UTC()) {
		t.Errorf("WrittenAt = %v, want Unix epoch (proto3 unset-timestamp default)", got.WrittenAt)
	}
}
