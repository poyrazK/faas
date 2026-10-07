package tcpd

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

type tlsPolicySource struct {
	intent    state.TCPListener
	domain    state.CustomDomain
	instances []state.Instance
	reads     int
}

func (*tlsPolicySource) AppByID(context.Context, string) (state.App, error) {
	return state.App{ID: "app", AccountID: "account", Status: state.AppActive}, nil
}
func (s *tlsPolicySource) TCPListenerByAppAndName(context.Context, string, string) (state.TCPListener, error) {
	return s.intent, nil
}
func (s *tlsPolicySource) DomainByName(context.Context, string) (state.CustomDomain, error) {
	return s.domain, nil
}
func (*tlsPolicySource) LiveDeployments(context.Context, string) ([]state.Deployment, error) {
	return []state.Deployment{{ID: "deployment", AppID: "app", Status: "live", TrafficPercent: 100}}, nil
}
func (s *tlsPolicySource) ListInstancesForApp(context.Context, string) ([]state.Instance, error) {
	s.reads++
	return s.instances, nil
}

type tlsPolicyAdmitter struct{ calls int }

func (a *tlsPolicyAdmitter) AdmitInstance(_ context.Context, _, deployment, _, _ string) (string, string, string, string, int32, bool, int, error) {
	a.calls++
	return "admitted", "node", deployment, "wake", 0, false, 9000, nil
}

func TestTLSRuntimeOwnershipBeforeWarmSelectionAndColdAdmission(t *testing.T) {
	for _, warm := range []bool{false, true} {
		for _, tc := range []struct {
			name    string
			change  func(*tlsPolicySource)
			allowed bool
		}{
			{"owned", func(*tlsPolicySource) {}, true},
			{"revoked", func(s *tlsPolicySource) { s.domain.VerifiedAt = time.Time{} }, false},
			{"foreign", func(s *tlsPolicySource) { s.domain.AppID = "other" }, false},
			{"environment", func(s *tlsPolicySource) { s.domain.EnvironmentID = "environment" }, false},
			{"wrong-domain", func(s *tlsPolicySource) { s.domain.Domain = "other.example" }, false},
			{"changed-policy", func(s *tlsPolicySource) { s.intent.TLSHostname = "other.example" }, false},
			{"passthrough-change", func(s *tlsPolicySource) { s.intent.TLSMode = api.TCPListenerTLSPassthrough; s.intent.TLSHostname = "" }, false},
		} {
			suffix := "cold"
			if warm {
				suffix = "warm"
			}
			t.Run(tc.name+"/"+suffix, func(t *testing.T) {
				source := &tlsPolicySource{intent: state.TCPListener{ID: "listener", AppID: "app", AccountID: "account", ListenerName: "echo", PublicPort: 40100, GuestPort: 9000, Protocol: "tcp", Enabled: true, TLSMode: api.TCPListenerTLSTerminate, TLSHostname: "echo.example"}, domain: state.CustomDomain{Domain: "echo.example", AppID: "app", VerifiedAt: time.Now()}}
				if warm {
					source.instances = []state.Instance{{ID: "warm", AppID: "app", DeploymentID: "deployment", NodeID: "node", State: string(state.StateRunning)}}
				}
				tc.change(source)
				admit := &tlsPolicyAdmitter{}
				resolver := &StoreTargetResolver{Instances: source, Admitter: admit}
				route := Route{ListenerID: "listener", AppID: "app", AccountID: "account", ListenerName: "echo", PublicPort: 40100, GuestPort: 9000, Protocol: "tcp", TLSHostname: "echo.example"}
				target, err := resolver.ResolveTarget(t.Context(), route)
				if !tc.allowed {
					if err == nil || source.reads != 0 || admit.calls != 0 {
						t.Fatalf("rejected ownership reached workload: target=%+v err=%v reads=%d admits=%d", target, err, source.reads, admit.calls)
					}
					return
				}
				want := "admitted"
				calls := 1
				if warm {
					want = "warm"
					calls = 0
				}
				if err != nil || target.InstanceID != want || admit.calls != calls || source.reads != 1 {
					t.Fatalf("owned target=%+v err=%v reads=%d admits=%d", target, err, source.reads, admit.calls)
				}
			})
		}
	}
}
