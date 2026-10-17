// adr: 1359
package vmmdgrpc

import (
	"context"
	"testing"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm/activity"
)

func TestRunningSuspendForwardRequiresUnambiguousSyntheticDispatch(t *testing.T) {
	for _, mode := range []string{"valid", "missing_source", "duplicate_identity", "second_attempt", "invalid_id"} {
		t.Run(mode, func(t *testing.T) {
			tracker := activity.New(nil)
			s := &Server{activity: tracker}
			id := uuid.NewString()
			req := &vmmdpb.ForwardHTTPRequestInit{Instance: "vm", Headers: []*vmmdpb.Header{{Name: api.InvocationIDHeader, Value: id}, {Name: api.InvocationAttemptHeader, Value: "1"}, {Name: api.InvocationSourceHeader, Value: "async_invoke"}}}
			switch mode {
			case "missing_source":
				req.Headers = req.Headers[:2]
			case "duplicate_identity":
				req.Headers = append(req.Headers, &vmmdpb.Header{Name: api.InvocationIDHeader, Value: id})
			case "second_attempt":
				req.Headers[1].Value = "2"
			case "invalid_id":
				req.Headers[0].Value = "unbound"
			}
			done := s.beginDispatchActivity(req)
			if tracker.OnlyDispatch("vm", id, 1) != (mode == "valid") {
				t.Fatal("unproven forward received an in-flight pause exception")
			}
			if n, _ := tracker.Inflight("vm"); n != 1 {
				t.Fatal("request missing from ordinary busy guard")
			}
			done()
			if n, _ := tracker.Inflight("vm"); n != 0 {
				t.Fatal("forward did not settle activity")
			}
		})
	}
}

type heldSuspendVMM struct {
	stubVMM
	held bool
}

func (v *heldSuspendVMM) RehearseSuspendResume(context.Context, string, string) error { return nil }
func (v *heldSuspendVMM) RehearsalCheckpointHeld(string, string) bool                 { return v.held }

func TestRunningSuspendRPCPreservesOriginalForwardingSession(t *testing.T) {
	for _, held := range []bool{false, true} {
		s := New(&heldSuspendVMM{held: held}, nil, "test", nil)
		instance := uuid.NewString()
		ctx, release := s.forwardSessions.track(t.Context(), instance)
		_, err := s.RehearseSuspendResume(t.Context(), &vmmdpb.RehearseSuspendResumeRequest{Instance: instance, ReceiptId: uuid.NewString()})
		if err != nil {
			t.Fatal(err)
		}
		if (ctx.Err() == nil) != held {
			t.Fatal("RPC closed the bridge holding the enrolled job")
		}
		release()
	}
}
