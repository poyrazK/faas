// White-box test for the `ops == nil` default branch in Server.New
// (server.go:62-64). The bufconn/client tests always pass a
// non-nil wire.OpsMetrics so they don't exercise the default.
// This file is in `package scheddgrpc` so it can call New
// directly with a nil ops — the only way to verify the
// production-side fallback to wire.NewOpsMetrics("schedd").
package scheddgrpc

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// noopEngine is a SchedAPI implementation that returns zero values
// for every method. Lives next to the test that uses it.
//
// Intentionally a separate type from the `fakeEngine` defined in
// bufconn_test.go (line 28). That one is in `package scheddgrpc_test`
// and powers the bufconn integration tests; this one is in
// `package scheddgrpc` and is the in-package white-box stub.
// Sharing the definition would require a third test package and
// a non-test helper file in the production tree — the cost
// outweighs the value of de-duplicating 12 lines of stub code.
type noopEngine struct{}

func (noopEngine) Wake(context.Context, string, string) (sched.WakeResult, error) {
	return sched.WakeResult{}, nil
}
func (noopEngine) AdmitInstance(context.Context, string, string) (sched.WakeResult, error) {
	return sched.WakeResult{}, nil
}
func (noopEngine) EnsureWake(context.Context, string) (sched.CoordOutcome, error) {
	return sched.CoordOutcome{}, nil
}
func (noopEngine) ReportActivity(context.Context, []state.InstanceTouch) (int, error) {
	return 0, nil
}
func (noopEngine) ParkWithReason(context.Context, string, string) error { return nil }
func (noopEngine) StreamAppLogs(context.Context, string, int64, time.Time, string, LogFrameSink) error {
	return nil
}
func (noopEngine) StreamWarmHints(context.Context, WarmHintSink) error { return nil }
func (noopEngine) CapacitySink() CapacitySink {
	return func(sched.CapacityReport) error { return nil }
}
func (noopEngine) NodeKeyRegistry() *sched.NodeKeyRegistry { return nil }

// DestroyForLivenessFailure (issue #554 / ADR-078) — stub to
// satisfy the SchedAPI interface; the white-box nil-ops test
// doesn't exercise the handler body.
func (noopEngine) DestroyForLivenessFailure(context.Context, string, string) error { return nil }

// TestServerNew_NilOpsUsesDefault confirms the
// "ops == nil → wire.NewOpsMetrics(\"schedd\")" fallback
// (server.go:62-64). The constructor must not panic on nil ops
// because ad-hoc test harnesses and the /test/throwaway scripts
// sometimes don't carry a metrics registry. A panic here would
// kill a daemon boot.
//
// The test is a plain call + nil check; the panic guarantee
// falls out of "if New panics, the test fails". No
// `defer recover()` ceremony — that's the wrong shape for a
// non-recovering expected outcome.
func TestServerNew_NilOpsUsesDefault(t *testing.T) {
	s := New(noopEngine{}, nil, nil)
	if s == nil {
		t.Fatal("New returned nil Server")
	}
	if s.ops == nil {
		t.Fatal("ops not defaulted; the nil-check branch is unreachable")
	}
}
