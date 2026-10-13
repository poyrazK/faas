// adr: 590
package sched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wakeownershipwire"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/metadata"
)

type ownershipRouterVMM struct {
	fakeRouterVMM
	observed context.Context
}

func (v *ownershipRouterVMM) CheckCheckpointWakeOwnershipSupport(context.Context) error { return nil }

func (v *ownershipRouterVMM) CreateColdBoot(ctx context.Context, instance string, app AppSpec) (*WakeOutcome, error) {
	v.observed = ctx
	return v.fakeRouterVMM.CreateColdBoot(ctx, instance, app)
}
func (v *ownershipRouterVMM) CreateFromSnapshot(ctx context.Context, instance string, app AppSpec, snap SnapshotRef) (*WakeOutcome, error) {
	v.observed = ctx
	return v.fakeRouterVMM.CreateFromSnapshot(ctx, instance, app, snap)
}
func (v *ownershipRouterVMM) CreatePausedFromSnapshot(ctx context.Context, instance string, app AppSpec, snap SnapshotRef) (*WakeOutcome, error) {
	return v.CreateFromSnapshot(ctx, instance, app, snap)
}

func TestVMMRouterRetainedWakeOwnership(t *testing.T) {
	for _, method := range []string{"cold_boot", "restore", "paused_restore"} {
		t.Run(method, func(t *testing.T) {
			frame := state.ProjectEnvironmentCloneWakeDispatch{InstanceID: uuid.NewString(), AppID: uuid.NewString(), DeploymentID: uuid.NewString(), NodeID: uuid.NewString(), WakeID: uuid.NewString(), RequestReason: "environment-clone:" + uuid.NewString(), Method: "restore", DispatchedAt: time.Now().UTC().Round(0)}
			if method == "cold_boot" {
				frame.Method = method
			}
			v := &ownershipRouterVMM{}
			r := NewVMMRouter(nil, nil, nil)
			r.SetClient(frame.NodeID, v)
			r.WithCloneWakeDispatchRecorder(func(context.Context, string, string, string, string, string) error {
				t.Fatal("admission recorded twice")
				return nil
			})
			prepared := 0
			mode := "error"
			denied := errors.New("admission uncertain")
			r.WithCloneWakeDispatchPreparer(func(_ context.Context, instance, app, dep, node, requested string) (*state.ProjectEnvironmentCloneWakeDispatch, error) {
				prepared++
				if instance != frame.InstanceID || app != frame.AppID || dep != frame.DeploymentID || node != frame.NodeID || requested != frame.Method {
					t.Fatal("changed admission payload")
				}
				switch mode {
				case "error":
					return nil, denied
				case "foreign":
					foreign := frame
					foreign.AppID = uuid.NewString()
					return &foreign, nil
				case "ordinary":
					return nil, nil
				default:
					return &frame, nil
				}
			})
			app := AppSpec{AppID: frame.AppID, DeploymentID: frame.DeploymentID}
			call := func() error {
				ctx := wire.WithContext(t.Context(), wire.CorrelationFields{WakeID: "untrusted-trace"})
				snap := SnapshotRef{DeploymentID: frame.DeploymentID}
				var err error
				switch method {
				case "cold_boot":
					_, err = r.CreateColdBoot(ctx, frame.NodeID, frame.InstanceID, app)
				case "restore":
					_, err = r.CreateFromSnapshot(ctx, frame.NodeID, frame.InstanceID, app, snap)
				default:
					_, err = r.CreatePausedFromSnapshot(ctx, frame.NodeID, frame.InstanceID, app, snap)
				}
				return err
			}
			if err := call(); !errors.Is(err, denied) || len(v.instanceCalls) != 0 {
				t.Fatal("uncertain admission sent RPC", err)
			}
			mode = "foreign"
			if err := call(); !errors.Is(err, state.ErrConflict) || len(v.instanceCalls) != 0 {
				t.Fatal("foreign ownership sent RPC", err)
			}
			mode = "allowed"
			if err := call(); err != nil || len(v.instanceCalls) != 1 {
				t.Fatal("original admission lost", err)
			}
			// The real VMM client adds correlation metadata at this same boundary.
			outgoing := wire.WithCorrelationOutgoing(v.observed, wire.CorrelationFields{WakeID: "untrusted-trace"})
			md, _ := metadata.FromOutgoingContext(outgoing)
			got, err := wakeownershipwire.Incoming(metadata.NewIncomingContext(t.Context(), md), frame.NodeID)
			if err != nil || got != frame {
				t.Fatal("tracing replaced retained original ownership", err)
			}
			mode = "ordinary"
			if err := call(); err != nil || len(v.instanceCalls) != 2 {
				t.Fatal("ordinary source wake blocked", err)
			}
			md, _ = metadata.FromOutgoingContext(v.observed)
			if wakeownershipwire.IncomingPresent(metadata.NewIncomingContext(t.Context(), md)) {
				t.Fatal("ordinary source acquired clone envelope")
			}
			if prepared != 4 {
				t.Fatal("wrong admission count")
			}
		})
	}
}

func TestVMMRouterWakeOwnershipUnsupportedBeforeAdmission(t *testing.T) {
	for _, cli := range []VMM{&fakeRouterVMM{}, &VMMClient{}} {
		r := NewVMMRouter(nil, nil, nil)
		r.SetClient("node", cli)
		r.WithCloneWakeDispatchPreparer(func(context.Context, string, string, string, string, string) (*state.ProjectEnvironmentCloneWakeDispatch, error) {
			t.Fatal("unsupported receiver charged dispatch")
			return nil, nil
		})
		if out, err := r.CreateColdBoot(t.Context(), "node", "instance", AppSpec{}); !errors.Is(err, state.ErrConflict) || out != nil {
			t.Fatal("unsupported receiver accepted ownership", err)
		}
	}
}
