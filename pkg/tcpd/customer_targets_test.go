package tcpd

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type customerTargetSource struct {
	instances     []state.Instance
	deployments   []state.Deployment
	deploymentErr error
}

func (s *customerTargetSource) ListInstancesForApp(context.Context, string) ([]state.Instance, error) {
	return s.instances, nil
}

type customerTargetAdmitter struct {
	calls                                  int
	selectedDeployment, returnedDeployment string
}

func (a *customerTargetAdmitter) AdmitInstance(_ context.Context, app, deployment, _, trigger string) (string, string, string, string, int32, bool, int, error) {
	a.calls++
	a.selectedDeployment = deployment
	if app != "app" || trigger != "gateway" {
		panic("unexpected customer admission")
	}
	if a.returnedDeployment != "" {
		deployment = a.returnedDeployment
	}
	return "admitted", "node", deployment, "wake", 0, false, 8080, nil
}

func TestTCPResolverUsesOnlyCustomerInstances(t *testing.T) {
	for _, mode := range []string{"", string(state.InstanceModeNormal), string(state.InstanceModeService), string(state.InstanceModeWorker)} {
		t.Run("customer-mode-"+mode, func(t *testing.T) {
			source := &customerTargetSource{instances: []state.Instance{
				{ID: "mirror", AppID: "app", NodeID: "node", State: string(state.StateRunning), Mode: string(state.InstanceModeMirror)},
				{ID: "foreign", AppID: "other", NodeID: "node", State: string(state.StateRunning)},
				{ID: "customer", DeploymentID: "deployment", AppID: "app", NodeID: "node", State: string(state.StateRunning), Mode: mode},
			}}
			admit := &customerTargetAdmitter{}
			resolver := &StoreTargetResolver{Instances: source, Admitter: admit}
			route := Route{ListenerID: "listener", AppID: "app", ListenerName: "echo", GuestPort: 9000, PublicPort: 40100, Protocol: "tcp"}
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

func (s *customerTargetSource) LiveDeployments(context.Context, string) ([]state.Deployment, error) {
	if s.deploymentErr != nil {
		return nil, s.deploymentErr
	}
	if s.deployments != nil {
		return s.deployments, nil
	}
	return []state.Deployment{{ID: "deployment", AppID: "app", Status: state.DeployLive, TrafficPercent: 100}}, nil
}

func TestTCPResolverPinsTrafficBucketOnWarmAndColdPaths(t *testing.T) {
	source := &customerTargetSource{deployments: []state.Deployment{
		{ID: "old", AppID: "app", Status: state.DeployLive},
		{ID: "current", AppID: "app", Status: state.DeployLive, TrafficPercent: 100},
	}, instances: []state.Instance{
		{ID: "old-instance", AppID: "app", DeploymentID: "old", NodeID: "node", State: string(state.StateRunning)},
		{ID: "current-instance", AppID: "app", DeploymentID: "current", NodeID: "node", State: string(state.StateRunning)},
	}}
	admit := &customerTargetAdmitter{}
	resolver := &StoreTargetResolver{Instances: source, Admitter: admit}
	route := Route{ListenerID: "listener", AppID: "app", ListenerName: "echo", GuestPort: 9000, PublicPort: 40100, Protocol: "tcp"}
	target, err := resolver.ResolveTarget(context.Background(), route)
	if err != nil || target.InstanceID != "current-instance" || admit.calls != 0 {
		t.Fatalf("warm target=%+v err=%v admissions=%d", target, err, admit.calls)
	}
	source.instances = source.instances[:1]
	target, err = resolver.ResolveTarget(context.Background(), route)
	if err != nil || target.InstanceID != "admitted" || target.DeploymentID != "current" || admit.selectedDeployment != "current" || admit.calls != 1 {
		t.Fatalf("cold target=%+v err=%v admission=%+v", target, err, admit)
	}
	admit.returnedDeployment = "old"
	if _, err := resolver.ResolveTarget(context.Background(), route); err == nil {
		t.Fatal("admission returned a different deployment")
	}
	before := admit.calls
	source.deployments = []state.Deployment{}
	if _, err := resolver.ResolveTarget(context.Background(), route); err == nil || admit.calls != before {
		t.Fatal("missing serving deployment reached admission")
	}
	unavailable := errors.New("deployment store unavailable")
	source.deploymentErr = unavailable
	if _, err := resolver.ResolveTarget(context.Background(), route); !errors.Is(err, unavailable) || admit.calls != before {
		t.Fatalf("deployment read error=%v admissions=%d", err, admit.calls)
	}
}

func (s *customerTargetSource) AppByID(context.Context, string) (state.App, error) {
	return state.App{ID: "app", AccountID: "account", Status: state.AppActive}, nil
}
func (s *customerTargetSource) TCPListenerByAppAndName(context.Context, string, string) (state.TCPListener, error) {
	return state.TCPListener{ID: "listener", AppID: "app", AccountID: "account", ListenerName: "echo", PublicPort: 40100, GuestPort: 9000, Protocol: "tcp", Enabled: true}, nil
}

func (*customerTargetSource) DomainByName(context.Context, string) (state.CustomDomain, error) {
	return state.CustomDomain{}, state.ErrNotFound
}
