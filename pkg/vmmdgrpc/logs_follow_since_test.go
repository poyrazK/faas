package vmmdgrpc_test

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
)

// `gregale logs -f --since 15m` sends a follow stream with no cursor and a
// time bound. vmmd treated cursor 0 as "new lines only", so the window the
// customer asked for was never replayed; it now replays the retained lines
// inside the bound, then tails.
func TestLogs_FollowWithSinceReplaysTheWindow(t *testing.T) {
	ring := logbuf.New(1 << 20)
	bound := time.Now().Add(-time.Minute)
	for _, line := range []string{"first\n", "second\n"} {
		if _, err := ring.Write("stdout", []byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	cl := startLogsTestClient(t, &fakeVMM{
		logRingFn: func(string) *logbuf.Ring { return ring },
	})
	follow := true
	stream, err := cl.Logs(context.Background(), &vmmdpb.LogsRequest{
		Instance:       "inst-1",
		Follow:         &follow,
		SinceWrittenAt: timestamppb.New(bound),
	})
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	var got []string
	for len(got) < 2 {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		f, err := recvWithCtx(ctx, stream)
		cancel()
		if err != nil {
			t.Fatalf("replayed %q, then: %v", got, err)
		}
		if !f.GetIsGap() {
			got = append(got, f.GetLine())
		}
	}
	if got[0] != "first" || got[1] != "second" {
		t.Fatalf("replay = %q, want the retained window", got)
	}
}
