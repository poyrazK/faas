// adr: 375
package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type serviceSnapshotRoutingProvider struct {
	serviceProxyProvider
	t *testing.T
}

func (p *serviceSnapshotRoutingProvider) AffinityDeployment(string, string) (string, bool) {
	p.t.Fatal("live affinity read used after admission")
	return "", false
}

func TestServiceRoutingSnapshotRetainsDeploymentAcrossWakeAndRetry(t *testing.T) {
	for _, mode := range []string{"release", "override", "affinity", "weighted"} {
		t.Run(mode, func(t *testing.T) {
			old, next, source, oldRelease, newRelease := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
			policy := serviceSnapshotFixture()
			policy.Routing = &ServiceRoutingSnapshot{CallerDeploymentID: source, ReleaseResolved: true, ReleaseVerdict: "allowed", Weights: []DeploymentWeightsRow{{ID: old, TrafficPercent: 100}}}
			if mode == "release" {
				policy.Routing.ReleaseID, policy.Routing.ReleaseDeploymentID = oldRelease, old
			}
			if mode == "override" {
				policy.Routing.OverrideDeploymentID, policy.Routing.OverrideChecked, policy.Routing.OverrideAllowed = old, true, true
			}
			provider := &serviceSnapshotRoutingProvider{t: t}
			var attempts int
			var revision string
			proxy := NewServiceProxy(ServiceProxyConfig{Provider: provider,
				Policy: func(ctx context.Context, _, _ string, _ bool) (ServicePolicySnapshot, error) {
					inputs, ok := ServicePolicyRoutingInputsFromContext(ctx)
					if !ok || inputs.CallerDeploymentID != source {
						t.Fatal("source identity missing from policy read")
					}
					return policy, nil
				},
				ResolveCallerIdentity: func(context.Context, string) (string, string, error) { return "client", source, nil },
				ResolveRelease: func(context.Context, string, string, string, string) (string, string, error) {
					t.Fatal("mixed release read used")
					return "", "", nil
				},
				ValidateDeployment: func(context.Context, string, string) (bool, error) {
					t.Fatal("mixed override read used")
					return false, nil
				},
				WakeDeployment: func(_ context.Context, app, deployment string) error {
					if app != "orders" || deployment != old {
						t.Fatalf("wake = %s/%s", app, deployment)
					}
					policy.Routing.Weights[0].ID = next
					if mode == "release" {
						policy.Routing.ReleaseID, policy.Routing.ReleaseDeploymentID = newRelease, next
					}
					if mode == "override" {
						policy.Routing.OverrideAllowed = false
					}
					provider.snapshot = ServiceEndpointsSnapshot{AppID: "orders", Endpoints: []ServiceEndpoint{
						{InstanceID: "one", NodeID: "node", DeploymentID: old, Port: 8080}, {InstanceID: "two", NodeID: "node", DeploymentID: old, Port: 8081}, {InstanceID: "new", NodeID: "node", DeploymentID: next, Port: 8082}}}
					return nil
				},
				Forward: func(target Target) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						attempts++
						want := old
						if attempts > 2 {
							want = next
						}
						if target.DeploymentID != want {
							t.Fatalf("attempt %d crossed deployment: %s want %s", attempts, target.DeploymentID, want)
						}
						if attempts <= 2 {
							if got := TrafficPolicyRevision(r.Context()); revision == "" {
								revision = got
							} else if got != revision {
								t.Fatal("retry changed policy proof")
							}
							if mode == "release" && r.Header.Get(api.ReleaseHeader) != oldRelease {
								t.Fatal("release changed after wake")
							}
						}
						if attempts == 1 {
							markStaleTarget(r.Context())
							http.Error(w, "stale", http.StatusServiceUnavailable)
							return
						}
						w.WriteHeader(http.StatusOK)
					})
				},
			})
			call := func() *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodGet, "http://orders.internal/health", nil)
				if mode == "override" {
					r.Header.Set(api.TargetDeploymentHeader, old)
				}
				if mode == "affinity" {
					r.Header.Set(api.VersionKeyHeader, "customer-key")
				}
				w := httptest.NewRecorder()
				proxy.ServeHTTP(w, r)
				return w
			}
			first := call()
			if first.Code != http.StatusOK || attempts != 2 {
				t.Fatalf("admitted = %d attempts=%d body=%s", first.Code, attempts, first.Body.String())
			}
			fresh := call()
			if mode == "override" {
				if fresh.Code != http.StatusUnprocessableEntity || attempts != 2 {
					t.Fatalf("fresh override = %d attempts=%d", fresh.Code, attempts)
				}
			} else if fresh.Code != http.StatusOK || attempts != 3 {
				t.Fatalf("fresh route = %d attempts=%d body=%s", fresh.Code, attempts, fresh.Body.String())
			}
			if fresh.Header().Get(TrafficPolicyRevisionHeader) == revision {
				t.Fatal("changed routing inputs retained old fingerprint")
			}
		})
	}
}

func TestServiceSnapshotAffinityMatchesLivePickerAndProofOmitsRandomSelection(t *testing.T) {
	rows := []DeploymentWeightsRow{{ID: "second", TrafficPercent: 20}, {ID: "first", TrafficPercent: 80}}
	picker := &appPicker{}
	setPickerWeights(picker, buildDeploymentWeights(rows))
	for _, key := range []string{"one", "two", "three", "four"} {
		want, ok := affinityDeployment("app", key, picker)
		got, gotOK := AffinityDeploymentFromWeights("app", key, rows)
		if got != want || gotOK != ok {
			t.Fatalf("cohort %q = %s/%v want %s/%v", key, got, gotOK, want, ok)
		}
	}
	policy := serviceSnapshotFixture()
	policy.Routing.Weights = rows
	proxy := NewServiceProxy(ServiceProxyConfig{Policy: func(context.Context, string, string, bool) (ServicePolicySnapshot, error) { return policy, nil }})
	var proof string
	for _, key := range []string{"one", "two"} {
		r := httptest.NewRequest(http.MethodGet, "http://orders.internal/health", nil)
		r = r.WithContext(WithServicePolicyRoutingInputs(r.Context(), ServicePolicyRoutingInputs{ReleaseValid: true, SelectionKey: key}))
		if proxy.pinServicePolicy(httptest.NewRecorder(), r, "client", "orders", true) {
			t.Fatal("pin failed")
		}
		if got := TrafficPolicyRevision(r.Context()); proof == "" {
			proof = got
		} else if got != proof {
			t.Fatal("random routing choice changed policy fingerprint")
		}
	}
}

func TestServiceRoutingSnapshotRefusesBeforeWakeWithCompatibleStatus(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want int
	}{
		{"gone", http.StatusGone}, {"conflict", http.StatusConflict}, {"invalid release", http.StatusBadRequest},
		{"missing source", http.StatusForbidden}, {"invalid override", http.StatusBadRequest},
		{"override conflicts with release", http.StatusConflict}, {"foreign override", http.StatusUnprocessableEntity},
		{"no deployment", http.StatusServiceUnavailable}, {"missing routing snapshot", http.StatusServiceUnavailable},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			source, deployment, release := uuid.NewString(), uuid.NewString(), uuid.NewString()
			policy := serviceSnapshotFixture()
			policy.Routing = &ServiceRoutingSnapshot{CallerDeploymentID: source, ReleaseResolved: true, ReleaseVerdict: "allowed"}
			r := httptest.NewRequest(http.MethodGet, "http://orders.internal/health", nil)
			switch tc.mode {
			case "gone":
				policy.Routing.ReleaseError, policy.Routing.ReleaseVerdict = ErrReleaseGone, "gone"
			case "conflict":
				policy.Routing.ReleaseError, policy.Routing.ReleaseVerdict = ErrReleaseConflict, "conflict"
			case "invalid release":
				r.Header[api.ReleaseHeader] = []string{release, release}
				policy.Routing = nil
			case "missing source":
				source = ""
				r.Header.Set(api.ReleaseHeader, release)
				policy.Routing = nil
			case "invalid override":
				r.Header[api.TargetDeploymentHeader] = []string{deployment, deployment}
			case "override conflicts with release":
				r.Header[api.TargetDeploymentHeader] = []string{deployment, deployment}
				policy.Routing.ReleaseID, policy.Routing.ReleaseDeploymentID = release, deployment
			case "foreign override":
				r.Header.Set(api.TargetDeploymentHeader, deployment)
				policy.Routing.OverrideChecked, policy.Routing.OverrideDeploymentID = true, deployment
			case "missing routing snapshot":
				policy.Routing = nil
			}
			provider := &serviceProxyProvider{}
			proxy := NewServiceProxy(ServiceProxyConfig{Provider: provider,
				Policy:                func(context.Context, string, string, bool) (ServicePolicySnapshot, error) { return policy, nil },
				ResolveCallerIdentity: func(context.Context, string) (string, string, error) { return "client", source, nil },
				ResolveRelease: func(context.Context, string, string, string, string) (string, string, error) {
					t.Fatal("live release read used")
					return "", "", nil
				},
				Wake:    func(context.Context, string) error { t.Fatal("refused routing woke target"); return nil },
				Forward: func(Target) http.Handler { t.Fatal("refused routing forwarded"); return nil },
			})
			w := httptest.NewRecorder()
			proxy.ServeHTTP(w, r)
			if w.Code != tc.want || provider.calls.Load() != 0 {
				t.Fatalf("refusal = %d want=%d endpoint reads=%d body=%s", w.Code, tc.want, provider.calls.Load(), w.Body.String())
			}
		})
	}
}
