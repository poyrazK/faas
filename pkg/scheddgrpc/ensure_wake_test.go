// ensure_wake_test.go — fill pkg/scheddgrpc coverage of the
// EnsureWake RPC body (server.go:422-460) and the
// coordMethodToWakeMethod helper (server.go:468-473) via bufconn.
//
// Targets:
//   - EnsureWake: happy path returns the CoordInstance fields
//   - EnsureWake: engine error → grpcerr.ToStatus
//   - EnsureWake: nil Instance with nil err → Internal problem
//     (the defensive branch at server.go:445-451)
//   - EnsureWake: non-nil out.Err → grpcerr.ToStatus
//   - coordMethodToWakeMethod: ColdBoot true → WAKE_COLD_BOOT
//   - coordMethodToWakeMethod: ColdBoot false → WAKE_RESTORE

package scheddgrpc_test

import (
	"context"
	"errors"
	"testing"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ensureWakeEngine wraps fakeEngine to inject a configurable
// EnsureWake handler. The default fakeEngine.EnsureWake delegates
// to Wake; tests that want a specific CoordOutcome shape override
// ensureWakeFn.
type ensureWakeEngine struct {
	*fakeEngine
	ensureWakeFn func(ctx context.Context, appID, trigger string) (sched.CoordOutcome, error)
}

func (e *ensureWakeEngine) EnsureWake(ctx context.Context, appID, trigger string) (sched.CoordOutcome, error) {
	if e.ensureWakeFn != nil {
		return e.ensureWakeFn(ctx, appID, trigger)
	}
	return e.fakeEngine.EnsureWake(ctx, appID, trigger)
}

// TestEnsureWake_HappyPath: engine returns a fully-populated
// CoordOutcome; the server surfaces every CoordInstance field on
// the EnsureWakeResponse.
func TestEnsureWake_HappyPath(t *testing.T) {
	cli := newServer(t, &ensureWakeEngine{
		fakeEngine: &fakeEngine{},
		ensureWakeFn: func(context.Context, string, string) (sched.CoordOutcome, error) {
			return sched.CoordOutcome{
				Instance: &sched.CoordInstance{
					InstanceID:   "ins-1",
					NodeID:       "node-A",
					DeploymentID: "dep-1",
					WakeID:       "wake-1",
					Port:         9090,
					ColdBoot:     false, // → WAKE_RESTORE
				},
			}, nil
		},
	})
	resp, err := cli.EnsureWake(context.Background(), &scheddpb.EnsureWakeRequest{AppId: "app-1"})
	if err != nil {
		t.Fatalf("EnsureWake: %v", err)
	}
	if resp.GetInstanceId() != "ins-1" {
		t.Errorf("instance_id = %q", resp.GetInstanceId())
	}
	if resp.GetNodeId() != "node-A" {
		t.Errorf("node_id = %q", resp.GetNodeId())
	}
	if resp.GetDeploymentId() != "dep-1" {
		t.Errorf("deployment_id = %q", resp.GetDeploymentId())
	}
	if resp.GetWakeId() != "wake-1" {
		t.Errorf("wake_id = %q", resp.GetWakeId())
	}
	if resp.GetPort() != 9090 {
		t.Errorf("port = %d", resp.GetPort())
	}
	if resp.GetMethod() != scheddpb.WakeMethod_WAKE_RESTORE {
		t.Errorf("method = %v, want WAKE_RESTORE", resp.GetMethod())
	}
}

// TestEnsureWake_ColdBoot: CoordInstance.ColdBoot=true maps to
// WAKE_COLD_BOOT on the wire (coordMethodToWakeMethod helper).
func TestEnsureWake_ColdBoot(t *testing.T) {
	cli := newServer(t, &ensureWakeEngine{
		fakeEngine: &fakeEngine{},
		ensureWakeFn: func(context.Context, string, string) (sched.CoordOutcome, error) {
			return sched.CoordOutcome{
				Instance: &sched.CoordInstance{
					InstanceID: "ins-cb",
					NodeID:     "node-A",
					ColdBoot:   true,
				},
			}, nil
		},
	})
	resp, err := cli.EnsureWake(context.Background(), &scheddpb.EnsureWakeRequest{AppId: "app-cb"})
	if err != nil {
		t.Fatalf("EnsureWake: %v", err)
	}
	if resp.GetMethod() != scheddpb.WakeMethod_WAKE_COLD_BOOT {
		t.Errorf("method = %v, want WAKE_COLD_BOOT", resp.GetMethod())
	}
}

// TestEnsureWake_EngineErrorPropagates: the engine returns an
// admission error → the server lifts it to a gRPC status via
// toProblem + grpcerr.ToStatus. api.ErrCapacity carries an
// RFC 7807 problem that toProblem unwraps.
func TestEnsureWake_EngineErrorPropagates(t *testing.T) {
	cli := newServer(t, &ensureWakeEngine{
		fakeEngine: &fakeEngine{},
		ensureWakeFn: func(context.Context, string, string) (sched.CoordOutcome, error) {
			return sched.CoordOutcome{}, api.ErrCapacity("no RAM headroom")
		},
	})
	_, err := cli.EnsureWake(context.Background(), &scheddpb.EnsureWakeRequest{AppId: "app-x"})
	if err == nil {
		t.Fatal("err = nil, want capacity error")
	}
	if got := status.Code(err); got != codes.ResourceExhausted {
		t.Errorf("code = %v, want ResourceExhausted", got)
	}
}

// TestEnsureWake_NilInstanceReturnsInternal pins the defensive
// branch at server.go:445-451: a successful EnsureWake with a nil
// Instance is a programming bug; the server must surface Internal
// rather than return a phantom 200 with empty fields.
func TestEnsureWake_NilInstanceReturnsInternal(t *testing.T) {
	cli := newServer(t, &ensureWakeEngine{
		fakeEngine: &fakeEngine{},
		ensureWakeFn: func(context.Context, string, string) (sched.CoordOutcome, error) {
			return sched.CoordOutcome{Instance: nil}, nil // nil instance + nil err
		},
	})
	_, err := cli.EnsureWake(context.Background(), &scheddpb.EnsureWakeRequest{AppId: "app-bug"})
	if err == nil {
		t.Fatal("err = nil, want Internal")
	}
	if got := status.Code(err); got != codes.Internal {
		t.Errorf("code = %v, want Internal", got)
	}
}

// TestEnsureWake_NonNilOutErr: the leader's outcome carries an
// error even when the engine's direct return is nil (the
// single-flight path stamps wake-level failures onto out.Err so
// followers inherit them). Use api.CodeNotFound so grpcerr's
// code-to-gRPC table maps the error to NotFound rather than the
// default Internal branch.
func TestEnsureWake_NonNilOutErr(t *testing.T) {
	cli := newServer(t, &ensureWakeEngine{
		fakeEngine: &fakeEngine{},
		ensureWakeFn: func(context.Context, string, string) (sched.CoordOutcome, error) {
			return sched.CoordOutcome{
				Err: api.NewProblem(404, api.CodeNotFound, "app gone", ""),
			}, nil
		},
	})
	_, err := cli.EnsureWake(context.Background(), &scheddpb.EnsureWakeRequest{AppId: "app-deleted"})
	if err == nil {
		t.Fatal("err = nil, want not-found problem")
	}
	if got := status.Code(err); got != codes.NotFound {
		t.Errorf("code = %v, want NotFound", got)
	}
}

// TestEnsureWake_GenericEngineError: a non-problem error from the
// engine falls through to grpcerr.ToStatus's default mapping.
func TestEnsureWake_GenericEngineError(t *testing.T) {
	cli := newServer(t, &ensureWakeEngine{
		fakeEngine: &fakeEngine{},
		ensureWakeFn: func(context.Context, string, string) (sched.CoordOutcome, error) {
			return sched.CoordOutcome{}, errors.New("kaboom")
		},
	})
	_, err := cli.EnsureWake(context.Background(), &scheddpb.EnsureWakeRequest{AppId: "app-x"})
	if err == nil {
		t.Fatal("err = nil, want error")
	}
	if got := status.Code(err); got == codes.OK {
		t.Errorf("code = OK, want non-OK")
	}
}

// Compile-time witness: the fakeEngine-with-EnsureWake wrapper
// satisfies scheddgrpc.SchedAPI (it forwards every method to the
// inner *fakeEngine and overrides EnsureWake).
var _ scheddgrpc.SchedAPI = (*ensureWakeEngine)(nil)

// TestEnsureWake_AtCapacityRefusalsAreTyped pins production-us hunt #9,
// H9-169: both refusals reached a worker app's visitors as
// "500 schedd operation failed: sched: at capacity".
func TestEnsureWake_AtCapacityRefusalsAreTyped(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		code   string
		status int
	}{
		{"no request listener", sched.ErrNoRequestListener, api.CodeValidation, api.StatusForCode(api.CodeValidation)},
		{"at capacity", sched.ErrAtCapacity, api.CodeCapacity, 503},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cli := newServer(t, &ensureWakeEngine{
				fakeEngine: &fakeEngine{},
				ensureWakeFn: func(context.Context, string, string) (sched.CoordOutcome, error) {
					return sched.CoordOutcome{Err: tc.err}, tc.err
				},
			})
			_, err := cli.EnsureWake(context.Background(), &scheddpb.EnsureWakeRequest{AppId: "app-x"})
			p, ok := grpcerr.FromStatus(err)
			if !ok || p == nil {
				t.Fatalf("EnsureWake err = %v; want a typed problem", err)
			}
			if p.Code != tc.code || p.Status != tc.status {
				t.Fatalf("problem = %s %d %q; want %s %d", p.Code, p.Status, p.Title, tc.code, tc.status)
			}
		})
	}
}
