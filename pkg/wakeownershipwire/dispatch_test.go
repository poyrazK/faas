// adr: 590
package wakeownershipwire

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/metadata"
)

func fixture() state.ProjectEnvironmentCloneWakeDispatch {
	return state.ProjectEnvironmentCloneWakeDispatch{InstanceID: uuid.NewString(), AppID: uuid.NewString(), DeploymentID: uuid.NewString(), NodeID: uuid.NewString(), WakeID: uuid.NewString(), RequestReason: "environment-clone:" + uuid.NewString(), Method: "restore", DispatchedAt: time.Date(2026, 10, 11, 12, 0, 0, 123456789, time.UTC)}
}

func TestDispatchPreservesOriginalOwnership(t *testing.T) {
	for _, method := range []string{"cold_boot", "restore"} {
		frame := fixture()
		frame.Method = method
		ctx := metadata.NewOutgoingContext(t.Context(), metadata.Pairs("x-faas-deployment-id", "unrelated", "authorization", "existing-private-channel"))
		outgoing, err := Outgoing(ctx, frame)
		if err != nil {
			t.Fatal(err)
		}
		md, _ := metadata.FromOutgoingContext(outgoing)
		got, err := Incoming(metadata.NewIncomingContext(t.Context(), md), frame.NodeID)
		if err != nil || got != frame {
			t.Fatal("original ownership changed", err)
		}
		if md.Get("x-faas-deployment-id")[0] != "unrelated" || md.Get("authorization")[0] != "existing-private-channel" {
			t.Fatal("unrelated metadata changed")
		}
		if _, err := Outgoing(outgoing, frame); err == nil {
			t.Fatal("existing authority silently overwritten")
		}
	}
}

func TestDispatchRejectsAmbiguousOrForeignEnvelope(t *testing.T) {
	frame := fixture()
	outgoing, err := Outgoing(t.Context(), frame)
	if err != nil {
		t.Fatal(err)
	}
	original, _ := metadata.FromOutgoingContext(outgoing)
	for _, key := range []string{"version", "instance", "app", "deployment", "node", "wake", "reason", "method", "dispatched-at"} {
		for _, mode := range []string{"missing", "duplicate", "empty", "invalid"} {
			t.Run(key+"/"+mode, func(t *testing.T) {
				md := original.Copy()
				switch mode {
				case "missing":
					md.Delete(prefix + key)
				case "duplicate":
					md.Append(prefix+key, md.Get(prefix + key)[0])
				case "empty":
					md.Set(prefix+key, "")
				case "invalid":
					md.Set(prefix+key, "invalid")
				}
				got, err := Incoming(metadata.NewIncomingContext(t.Context(), md), frame.NodeID)
				if err == nil || got != (state.ProjectEnvironmentCloneWakeDispatch{}) {
					t.Fatal("ambiguous envelope accepted")
				}
			})
		}
	}
	md := original.Copy()
	md.Set(prefix+"future-capability", "unknown")
	if got, err := Incoming(metadata.NewIncomingContext(t.Context(), md), frame.NodeID); err == nil || got != (state.ProjectEnvironmentCloneWakeDispatch{}) {
		t.Fatal("unknown capability dropped")
	}
	for _, node := range []string{"", "invalid", uuid.NewString()} {
		if got, err := Incoming(metadata.NewIncomingContext(t.Context(), original), node); err == nil || got != (state.ProjectEnvironmentCloneWakeDispatch{}) {
			t.Fatal("foreign or unconfigured node accepted")
		}
	}
	if got, err := Incoming(metadata.NewIncomingContext(t.Context(), metadata.Pairs("x-faas-instance-id", frame.InstanceID, "x-faas-wake-id", frame.WakeID)), frame.NodeID); err == nil || got != (state.ProjectEnvironmentCloneWakeDispatch{}) {
		t.Fatal("tracing metadata substituted for original dispatch")
	}
}

func TestDispatchOutgoingRejectsIncompleteOriginal(t *testing.T) {
	for _, change := range []func(*state.ProjectEnvironmentCloneWakeDispatch){
		func(x *state.ProjectEnvironmentCloneWakeDispatch) { x.InstanceID = uuid.Nil.String() },
		func(x *state.ProjectEnvironmentCloneWakeDispatch) { x.AppID = "" },
		func(x *state.ProjectEnvironmentCloneWakeDispatch) { x.DeploymentID = "" },
		func(x *state.ProjectEnvironmentCloneWakeDispatch) { x.NodeID = "" },
		func(x *state.ProjectEnvironmentCloneWakeDispatch) { x.WakeID = "" },
		func(x *state.ProjectEnvironmentCloneWakeDispatch) { x.RequestReason = "ordinary" },
		func(x *state.ProjectEnvironmentCloneWakeDispatch) { x.Method = "paused_restore" },
		func(x *state.ProjectEnvironmentCloneWakeDispatch) { x.DispatchedAt = time.Time{} },
	} {
		frame := fixture()
		change(&frame)
		if ctx, err := Outgoing(t.Context(), frame); err == nil || ctx != nil {
			t.Fatal("incomplete original record encoded")
		}
	}
}
