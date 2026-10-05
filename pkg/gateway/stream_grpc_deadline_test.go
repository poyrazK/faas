// adr: 093 — streaming sessions detach the request budget; vmmd must not inherit it.

package gateway

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/reqbudget"
)

// deadlineRecordingClient records the context each vmmd stream is opened with
// and fails the open, so the forwarder returns immediately.
type deadlineRecordingClient struct {
	vmmdpb.VmmdClient
	mu   sync.Mutex
	ctxs []context.Context
}

func (c *deadlineRecordingClient) record(ctx context.Context) error {
	c.mu.Lock()
	c.ctxs = append(c.ctxs, ctx)
	c.mu.Unlock()
	return status.Error(codes.Unavailable, "recorded")
}

func (c *deadlineRecordingClient) ForwardHTTPStream(ctx context.Context, _ ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse], error) {
	return nil, c.record(ctx)
}

func (c *deadlineRecordingClient) ForwardRawStream(ctx context.Context, _ ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.ForwardRawRequest, vmmdpb.ForwardRawResponse], error) {
	return nil, c.record(ctx)
}

// TestStreamForwardersDoNotSendBudgetDeadlineToVMMD reproduces production-us:
// every WebSocket closed at ~30 s even while active, because the raw stream
// was opened with the request budget's deadline and gRPC sent it to vmmd. The
// streaming HTTP path had the same shape. The stream must open without a
// deadline, while the request's cancellation still reaches it.
func TestStreamForwardersDoNotSendBudgetDeadlineToVMMD(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, raw := range []bool{false, true} {
		name := "http"
		if raw {
			name = "raw"
		}
		t.Run(name, func(t *testing.T) {
			parent, cancelParent := context.WithCancel(context.Background())
			defer cancelParent()
			budgetCtx, cancelBudget, _ := reqbudget.WithRemaining(parent, 30*time.Second, 30*time.Second, "test", "stream")
			defer cancelBudget()
			if _, ok := budgetCtx.Deadline(); !ok {
				t.Fatal("setup: request budget carries no deadline")
			}
			r := httptest.NewRequest(http.MethodGet, "http://app.example/", nil).WithContext(budgetCtx)
			client := &deadlineRecordingClient{}
			if raw {
				rawStreamOnceWithEvents(httptest.NewRecorder(), r, client, log, Target{}, nil, nil)
			} else {
				fwdStreamOnceWithEvents(httptest.NewRecorder(), r, client, log, Target{}, nil)
			}
			if len(client.ctxs) != 1 {
				t.Fatalf("stream opens = %d, want 1", len(client.ctxs))
			}
			if deadline, ok := client.ctxs[0].Deadline(); ok {
				t.Fatalf("vmmd stream opened with deadline %s (in %s); gRPC would send it as grpc-timeout", deadline.Format(time.RFC3339Nano), time.Until(deadline).Round(time.Second))
			}
		})
	}
}

// TestGRPCStreamContextKeepsCancellation pins that hiding the deadline does
// not hide its effect: the wrapped context still ends when the session does.
func TestGRPCStreamContextKeepsCancellation(t *testing.T) {
	session, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	wrapped := grpcStreamContext{session}
	if _, ok := wrapped.Deadline(); ok {
		t.Fatal("grpcStreamContext exposed a deadline")
	}
	select {
	case <-wrapped.Done():
	case <-time.After(time.Second):
		t.Fatal("grpcStreamContext did not end with its session")
	}
	if wrapped.Err() == nil {
		t.Fatal("grpcStreamContext ended without an error")
	}
}
