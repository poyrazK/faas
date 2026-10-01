package tcpd

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type customerTargetSource struct{ instances []state.Instance }

func (s *customerTargetSource) ListInstancesForApp(context.Context, string) ([]state.Instance, error) {
	return s.instances, nil
}

type customerTargetAdmitter struct{ calls int }

func (a *customerTargetAdmitter) AdmitInstance(_ context.Context, app, _, _, trigger string) (string, string, string, string, int32, bool, int, error) {
	a.calls++
	if app != "app" || trigger != "gateway" {
		panic("unexpected customer admission")
	}
	return "admitted", "node", "deployment", "wake", 0, false, 8080, nil
}

func TestTCPResolverUsesOnlyCustomerInstances(t *testing.T) {
	for _, mode := range []string{"", string(state.InstanceModeNormal), string(state.InstanceModeService), string(state.InstanceModeWorker)} {
		t.Run("customer-mode-"+mode, func(t *testing.T) {
			source := &customerTargetSource{instances: []state.Instance{
				{ID: "mirror", AppID: "app", NodeID: "node", State: string(state.StateRunning), Mode: string(state.InstanceModeMirror)},
				{ID: "foreign", AppID: "other", NodeID: "node", State: string(state.StateRunning)},
				{ID: "customer", AppID: "app", NodeID: "node", State: string(state.StateRunning), Mode: mode},
			}}
			admit := &customerTargetAdmitter{}
			resolver := &StoreTargetResolver{Instances: source, Admitter: admit}
			route := Route{AppID: "app", ListenerName: "echo", GuestPort: 9000, PublicPort: 40100, Protocol: "tcp"}
			for range 8 {
				target, err := resolver.ResolveTarget(context.Background(), route)
				if err != nil || target.InstanceID != "customer" || target.Port != 9000 || admit.calls != 0 {
					t.Fatalf("warm target=%+v err=%v admissions=%d", target, err, admit.calls)
				}
			}
			// Existing mirror/foreign processes cannot suppress customer wake.
			source.instances = source.instances[:2]
			target, err := resolver.ResolveTarget(context.Background(), route)
			if err != nil || target.InstanceID != "admitted" || target.Port != 9000 || admit.calls != 1 {
				t.Fatalf("cold target=%+v err=%v admissions=%d", target, err, admit.calls)
			}
			resolver.Admitter = nil
			if _, err := resolver.ResolveTarget(context.Background(), route); err == nil {
				t.Fatal("mirror/foreign-only route succeeded without customer admission")
			}
		})
	}
}
