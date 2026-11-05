// adr: 20261017 vmmd restart adoption §3 (gateway hold through a planned restart)

package gateway

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
)

// noticeTable is a RestartNoticeReader over a fixed map.
type noticeTable struct {
	until map[string]time.Time
	err   error
	reads atomic.Int32
}

func (n *noticeTable) ListComputeNodeVMMDRestartNotices(context.Context) (map[string]time.Time, error) {
	n.reads.Add(1)
	return n.until, n.err
}

// flakyForwardClient fails its first opens with err, then succeeds.
type flakyForwardClient struct {
	vmmdpb.VmmdClient
	failures int
	err      error
	opens    int
	onOpen   func(int)
}

func (c *flakyForwardClient) ForwardHTTPStream(context.Context, ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse], error) {
	c.opens++
	if c.onOpen != nil {
		c.onOpen(c.opens)
	}
	if c.opens <= c.failures {
		return nil, c.err
	}
	return nil, nil
}

func TestForwardOpenWaitsOutAPlannedVMMDRestart(t *testing.T) {
	const node = "node-1"
	unavailable := status.Error(codes.Unavailable, "connection error: dial unix /run/faas/vmmd.sock: connect: no such file or directory")
	noticed := func() *noticeTable {
		return &noticeTable{until: map[string]time.Time{node: time.Now().Add(time.Minute)}}
	}
	tests := []struct {
		name     string
		notices  *noticeTable
		failures int
		err      error
		timeout  time.Duration
		// at is called before each open with the open's number.
		at        func(open int, table *noticeTable, moved *atomic.Int64)
		wantOpens int
		wantErr   bool
	}{
		// Without a notice an open is retried once after a reconnect: the
		// channel may still be backing off from a vmmd that is back.
		{name: "no notice: one reconnect", notices: &noticeTable{}, failures: 1, err: unavailable, wantOpens: 2},
		{name: "no notice fails after one retry", notices: &noticeTable{}, failures: 5, err: unavailable, wantOpens: 2, wantErr: true},
		{name: "notice for another node", notices: &noticeTable{until: map[string]time.Time{"node-2": time.Now().Add(time.Minute)}}, failures: 5, err: unavailable, wantOpens: 2, wantErr: true},
		{name: "unreadable notices", notices: &noticeTable{err: errors.New("db down")}, failures: 5, err: unavailable, wantOpens: 2, wantErr: true},
		{name: "held until the successor accepts", notices: noticed(), failures: 3, err: unavailable, wantOpens: 4},
		// The successor clears the notice as it starts serving; the channel
		// may not have reconnected yet.
		{name: "one retry after the notice ends", notices: noticed(), failures: 3, err: unavailable,
			at: func(open int, table *noticeTable, moved *atomic.Int64) {
				if open == 3 {
					table.until = map[string]time.Time{}
					moved.Add(int64(restartHoldNoticeAge))
				}
			},
			wantOpens: 4},
		{name: "a notice that ended releases the hold", notices: noticed(), failures: 1000, err: unavailable,
			at: func(open int, table *noticeTable, moved *atomic.Int64) {
				if open == 3 {
					table.until = map[string]time.Time{}
					moved.Add(int64(restartHoldNoticeAge))
				}
			},
			wantOpens: 4, wantErr: true},
		{name: "a refusal is not held", notices: noticed(), failures: 1, err: status.Error(codes.NotFound, "no instance"), wantOpens: 1, wantErr: true},
		{name: "the request bounds the hold", notices: noticed(), failures: 1000, err: unavailable, timeout: 3 * restartHoldRetryEvery, wantOpens: 4, wantErr: true},
		{name: "the hold limit bounds the hold", notices: noticed(), failures: 1000, err: unavailable,
			at: func(open int, _ *noticeTable, moved *atomic.Int64) {
				if open == 3 {
					moved.Store(int64(api.GatewayVMMDRestartHoldSeconds * time.Second))
				}
			},
			wantOpens: 3, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var moved atomic.Int64
			base := time.Now()
			notices := &restartNotices{store: tt.notices, now: func() time.Time { return base.Add(time.Duration(moved.Load())) }}
			inner := &flakyForwardClient{failures: tt.failures, err: tt.err}
			if tt.at != nil {
				inner.onOpen = func(open int) { tt.at(open, tt.notices, &moved) }
			}
			cli := restartHoldClient{VmmdClient: inner, node: node, notices: notices}
			ctx := context.Background()
			if tt.timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tt.timeout)
				defer cancel()
			}
			_, err := cli.ForwardHTTPStream(ctx)
			if (err != nil) != tt.wantErr {
				t.Fatalf("open error = %v, want error %v", err, tt.wantErr)
			}
			if inner.opens < tt.wantOpens-1 || inner.opens > tt.wantOpens {
				t.Fatalf("opens = %d, want %d", inner.opens, tt.wantOpens)
			}
		})
	}
}

// The forwards that fail together when vmmd stops share one notice read.
func TestRestartNoticeReadsAreShared(t *testing.T) {
	table := &noticeTable{until: map[string]time.Time{"n": time.Now().Add(time.Minute)}}
	clock := time.Now()
	notices := &restartNotices{store: table, now: func() time.Time { return clock }}
	for i := 0; i < 10; i++ {
		if !notices.active(context.Background(), "n") {
			t.Fatal("notice not active")
		}
	}
	clock = clock.Add(restartHoldNoticeAge)
	notices.active(context.Background(), "n")
	if got := table.reads.Load(); got != 2 {
		t.Fatalf("notice reads = %d, want 2", got)
	}
	var none *restartNotices
	if none.active(context.Background(), "n") {
		t.Fatal("a nil notice cache reports a notice")
	}
}

func TestNodeClientCacheHoldsOnlyWithNotices(t *testing.T) {
	plain := NewNodeClientCache(nil, nil)
	if _, held := plain.client("n", nil).(restartHoldClient); held {
		t.Fatal("a cache without notices wrapped its client")
	}
	if _, held := NewNodeClientCache(nil, nil).WithRestartNotices(&noticeTable{}).client("n", nil).(restartHoldClient); !held {
		t.Fatal("a cache with notices did not wrap its client")
	}
	if plain.WithRestartNotices(nil).restartNotices != nil {
		t.Fatal("a nil reader installed notices")
	}
}
