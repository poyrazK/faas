package signd_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	signdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/signd/v1"
	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/signd"
	"github.com/onebox-faas/faas/pkg/storagekey"
)

// flaky fails the first fails calls with err, then signs through next.
type flaky struct {
	next  signdpb.SigndClient
	err   error
	fails int
	calls int
}

func (f *flaky) SignArtifact(ctx context.Context, in *signdpb.SignArtifactRequest, opts ...grpc.CallOption) (*signdpb.SignArtifactResponse, error) {
	f.calls++
	if f.calls <= f.fails {
		return nil, f.err
	}
	return f.next.SignArtifact(ctx, in, opts...)
}

// prod 2026-11-20 18:18: signd restarted during a release and two deploys
// that had already published their layers failed as deploy_failed on the
// Unavailable answer. The client now retries Unavailable, and only that.
func TestClientRetriesUnavailableSign(t *testing.T) {
	key := storagekey.AppLayer("alice", depID)
	t.Run("a signd restart is ridden out", func(t *testing.T) {
		f := newFixture(t)
		rpc := &flaky{next: direct{srv: f.srv, node: node}, err: status.Error(codes.Unavailable, "connection refused"), fails: 2}
		c := signd.NewClient(rpc, f.objects, nil)
		var waits []time.Duration
		c.SetRetryWait(func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil })
		if err := c.SignDigest(context.Background(), key, cosign.SigKeyFor(key), testDigest, 1); err != nil {
			t.Fatalf("SignDigest after two Unavailable answers: %v", err)
		}
		if rpc.calls != 3 || len(waits) != 2 {
			t.Fatalf("calls=%d waits=%v, want 3 calls and 2 waits", rpc.calls, waits)
		}
	})
	t.Run("a refusal is not retried", func(t *testing.T) {
		f := newFixture(t)
		rpc := &flaky{next: direct{srv: f.srv, node: node}, err: status.Error(codes.PermissionDenied, "not this node's build"), fails: 1}
		c := signd.NewClient(rpc, f.objects, nil)
		c.SetRetryWait(func(context.Context, time.Duration) error { t.Fatal("waited to retry a refusal"); return nil })
		err := c.SignDigest(context.Background(), key, cosign.SigKeyFor(key), testDigest, 1)
		if status.Code(errors.Unwrap(err)) != codes.PermissionDenied || rpc.calls != 1 {
			t.Fatalf("err=%v calls=%d, want the refusal after one call", err, rpc.calls)
		}
	})
	t.Run("an outage longer than the budget fails", func(t *testing.T) {
		f := newFixture(t)
		rpc := &flaky{next: direct{srv: f.srv, node: node}, err: status.Error(codes.Unavailable, "down"), fails: 1000}
		c := signd.NewClient(rpc, f.objects, nil)
		c.SetRetryWait(func(context.Context, time.Duration) error { return nil })
		if err := c.SignDigest(context.Background(), key, cosign.SigKeyFor(key), testDigest, 1); status.Code(errors.Unwrap(err)) != codes.Unavailable {
			t.Fatalf("err=%v, want Unavailable after the retry budget", err)
		}
		if rpc.calls < 2 || rpc.calls > 10 {
			t.Fatalf("calls=%d, want a bounded number of retries", rpc.calls)
		}
	})
}

const testDigest = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
