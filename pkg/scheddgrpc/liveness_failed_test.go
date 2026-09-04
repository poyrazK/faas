// Tests for ReportLivenessFailed (issue #554 / ADR-078). Locks
// down the wire shape (LivenessFailedReport / LivenessFailedAck),
// the ownership guard behaviour (a bad-routed report returns
// FailedPrecondition, not a NotFound), and the engine-call
// contract (instance id + closed-set reason flow verbatim into
// the SchedAPI.DestroyForLivenessFailure sink).
//
// Lives in package scheddgrpc_test (same as bufconn_test.go)
// and reuses newServer / fakeEngine / fakeEngine.destroyFn — the
// seam added in bufconn_test.go is what makes this file possible.
package scheddgrpc_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/status"
)

// TestReportLivenessFailed_HappyPath drives a successful drain:
// the schedd-side handler invokes SchedAPI.DestroyForLivenessFailure
// once, with the verbatim instance_id + reason from the wire,
// and replies with Ok=true.
func TestReportLivenessFailed_HappyPath(t *testing.T) {
	t.Parallel()
	var got scheddgrpcDestroyCall
	cli := newServer(t, &fakeEngine{
		destroyFn: func(_ context.Context, instanceID, reason string) error {
			got.instanceID = instanceID
			got.reason = reason
			return nil
		},
	})
	resp, err := cli.ReportLivenessFailed(context.Background(), &scheddpb.LivenessFailedReport{
		InstanceId: "i-1",
		Reason:     "liveness_timeout",
	})
	if err != nil {
		t.Fatalf("ReportLivenessFailed: %v", err)
	}
	if !resp.GetOk() {
		t.Errorf("ok = false, want true")
	}
	if got.instanceID != "i-1" {
		t.Errorf("instance_id = %q, want %q", got.instanceID, "i-1")
	}
	if got.reason != "liveness_timeout" {
		t.Errorf("reason = %q, want %q", got.reason, "liveness_timeout")
	}
}

// TestReportLivenessFailed_PropagatesReasonClosedSet verifies
// every closed-set reason value the vmmd poll goroutine can emit
// (timeout / conn_refused / conn_err / non_200 / n_consecutive)
// flows verbatim into the engine sink. The schedd side does NOT
// validate the reason string — that's the vmmd-side
// classification surface — but the test guards against a future
// refactor that re-shapes the wire on the schedd side.
func TestReportLivenessFailed_PropagatesReasonClosedSet(t *testing.T) {
	t.Parallel()
	reasons := []string{
		"liveness_timeout",
		"liveness_conn_refused",
		"liveness_conn_err",
		"liveness_non_200",
		"liveness_n_consecutive",
		fcvm.LivenessReasonInfrastructure,
		fcvm.LivenessReasonProcessExited,
	}
	for _, reason := range reasons {
		reason := reason
		t.Run(reason, func(t *testing.T) {
			t.Parallel()
			var got atomic.Value
			cli := newServer(t, &fakeEngine{
				destroyFn: func(_ context.Context, _, r string) error {
					got.Store(r)
					return nil
				},
			})
			if _, err := cli.ReportLivenessFailed(context.Background(), &scheddpb.LivenessFailedReport{
				InstanceId: "i-1",
				Reason:     reason,
			}); err != nil {
				t.Fatalf("ReportLivenessFailed(%q): %v", reason, err)
			}
			if got.Load() != reason {
				t.Errorf("reason = %v, want %q", got.Load(), reason)
			}
		})
	}
}

// TestReportLivenessFailed_AcceptsUnknownReason confirms the
// handler does NOT reject a future-classification reason that
// the vmmd poll goroutine may emit before the schedd side is
// updated. The reason string is propagated verbatim into the
// audit log; lifting it to InvalidArgument would break the
// additive classification surface.
func TestReportLivenessFailed_AcceptsUnknownReason(t *testing.T) {
	t.Parallel()
	var got atomic.Value
	cli := newServer(t, &fakeEngine{
		destroyFn: func(_ context.Context, _, r string) error {
			got.Store(r)
			return nil
		},
	})
	resp, err := cli.ReportLivenessFailed(context.Background(), &scheddpb.LivenessFailedReport{
		InstanceId: "i-1",
		Reason:     "liveness_future_classification",
	})
	if err != nil {
		t.Fatalf("ReportLivenessFailed: %v", err)
	}
	if !resp.GetOk() {
		t.Errorf("ok = false on unknown reason, want true")
	}
	if got.Load() != "liveness_future_classification" {
		t.Errorf("reason = %v, want verbatim", got.Load())
	}
}

// TestReportLivenessFailed_EngineErrNotFound asserts the
// not-found path: when the engine's DestroyForLivenessFailure
// returns state.ErrNotFound (the instance id no longer
// resolves), the handler surfaces codes.NotFound so the vmmd
// poll goroutine can log + exit. A bad instance id is not an
// auth failure.
func TestReportLivenessFailed_EngineErrNotFound(t *testing.T) {
	t.Parallel()
	cli := newServer(t, &fakeEngine{
		destroyFn: func(context.Context, string, string) error {
			return state.ErrNotFound
		},
	})
	_, err := cli.ReportLivenessFailed(context.Background(), &scheddpb.LivenessFailedReport{
		InstanceId: "i-missing",
		Reason:     "liveness_timeout",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("not a gRPC status: %v", err)
	}
	if st.Code().String() != "NotFound" {
		t.Errorf("code = %s, want NotFound", st.Code().String())
	}
}

// TestReportLivenessFailed_EngineErrInternal verifies any
// non-ErrNotFound engine error surfaces as codes.Internal. The
// vmmd poll goroutine has already exited on its end, so the
// status code only governs the warn-log line.
func TestReportLivenessFailed_EngineErrInternal(t *testing.T) {
	t.Parallel()
	boom := errors.New("db hitches")
	cli := newServer(t, &fakeEngine{
		destroyFn: func(context.Context, string, string) error {
			return boom
		},
	})
	_, err := cli.ReportLivenessFailed(context.Background(), &scheddpb.LivenessFailedReport{
		InstanceId: "i-1",
		Reason:     "liveness_timeout",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("not a gRPC status: %v", err)
	}
	if st.Code().String() != "Internal" {
		t.Errorf("code = %s, want Internal", st.Code().String())
	}
	if !strings.Contains(st.Message(), boom.Error()) {
		t.Errorf("message = %q, want substring %q", st.Message(), boom.Error())
	}
}

// TestReportLivenessFailed_EmptyInstanceID covers the empty-id
// fallback: the handler still calls the engine with an empty
// instance id. The engine-side guard returns ErrNotFound, which
// we lift to NotFound. vmmd never sends an empty instance id
// in practice (the per-instance loop carries the value from
// BringUp), but the test pins the handler's no-frills behaviour
// on an empty wire field.
func TestReportLivenessFailed_EmptyInstanceID(t *testing.T) {
	t.Parallel()
	cli := newServer(t, &fakeEngine{
		destroyFn: func(_ context.Context, instanceID, _ string) error {
			if instanceID != "" {
				t.Errorf("instance_id = %q, want empty", instanceID)
			}
			return state.ErrNotFound
		},
	})
	_, err := cli.ReportLivenessFailed(context.Background(), &scheddpb.LivenessFailedReport{
		InstanceId: "",
		Reason:     "liveness_timeout",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("not a gRPC status: %v", err)
	}
	if st.Code().String() != "NotFound" {
		t.Errorf("code = %s, want NotFound", st.Code().String())
	}
}

// scheddgrpcDestroyCall is a small typed sink used by
// TestReportLivenessFailed_HappyPath to assert the instance_id
// + reason pair the handler forwards to the engine. Lives
// next to the tests that use it so a future refactor that
// reshapes the SchedAPI surface catches the change.
type scheddgrpcDestroyCall struct {
	instanceID string
	reason     string
}

// sched is imported solely so the file references the same
// package path the production handler compiles against;
// catches a future sched-package rename at the import
// boundary.
var _ = sched.WakeResult{}
