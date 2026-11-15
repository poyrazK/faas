// adr: 570
package gateway

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/trafficdeadline"
)

func serviceSnapshotFixture() ServicePolicySnapshot {
	return ServicePolicySnapshot{InputRevision: "service-inputs-v1:old", Found: true, AliasAllowed: true,
		Routing: &ServiceRoutingSnapshot{Weights: []DeploymentWeightsRow{{ID: "release", TrafficPercent: 100}}},
		Target:  ServiceTarget{AppID: "orders", AppProtocol: api.AppProtocolHTTP2},
		Caller:  ServiceCaller{AppID: "client", AccountID: "account", CallScope: &api.ServiceCallScope{Methods: []string{"GET"}, PathPrefixes: []string{"/health"}}}}
}

func serviceSnapshotRequest(proxy *ServiceProxy) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "http://orders.internal/health", nil)
	r.Header.Set(ServiceProxyCallerAppHeader, "client")
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, r)
	return w
}

func TestServicePolicySnapshotRetainsAccessAndTransportAcrossWakeRetry(t *testing.T) {
	source := serviceSnapshotFixture()
	provider := &serviceProxyProvider{}
	var revision string
	var attempts int
	proxy := NewServiceProxy(ServiceProxyConfig{Provider: provider,
		Policy: func(context.Context, string, string, bool) (ServicePolicySnapshot, error) { return source, nil },
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			t.Fatal("mutable resolver used")
			return ServiceTarget{}, false, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) {
			t.Fatal("mutable authorization used")
			return ServiceCaller{}, nil
		},
		AllowAlias: func(context.Context, string, string) (bool, error) { t.Fatal("mutable alias used"); return false, nil },
		Wake: func(context.Context, string) error {
			source.InputRevision = "service-inputs-v1:new"
			source.Target.AppProtocol = api.AppProtocolGRPC
			source.Caller.CallScope.Methods[0] = http.MethodDelete
			source.AuthorizationError = ErrServiceProxyCallerDenied
			provider.snapshot = ServiceEndpointsSnapshot{AppID: "orders", Endpoints: []ServiceEndpoint{{InstanceID: "one", DeploymentID: "release", NodeID: "node", Port: 8080}, {InstanceID: "two", DeploymentID: "release", NodeID: "node", Port: 8081}}}
			return nil
		},
		Forward: func(Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts++
				if r.Header.Get("x-faas-protocol") != api.AppProtocolHTTP2 {
					t.Error("transport changed after wake")
				}
				if got := TrafficPolicyRevision(r.Context()); revision == "" {
					revision = got
				} else if got != revision {
					t.Error("retry changed revision")
				}
				if attempts == 1 {
					markStaleTarget(r.Context())
					http.Error(w, "stale", http.StatusServiceUnavailable)
					return
				}
				w.Header().Set(TrafficPolicyRevisionHeader, "forged")
				w.Header().Set("Trailer", TrafficPolicyRevisionHeader+", Grpc-Status")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("ok"))
				w.Header().Set(http.TrailerPrefix+TrafficPolicyRevisionHeader, "forged-late")
				w.Header().Set("Grpc-Status", "0")
			})
		},
	})
	w := serviceSnapshotRequest(proxy)
	if w.Code != http.StatusOK || attempts != 2 || w.Header().Get(TrafficPolicyRevisionHeader) != revision || !strings.HasPrefix(revision, "traffic-v1:") {
		t.Fatalf("admitted = %d attempts=%d proof=%q revision=%q", w.Code, attempts, w.Header().Get(TrafficPolicyRevisionHeader), revision)
	}
	if w.Result().Trailer.Get(TrafficPolicyRevisionHeader) != "" || w.Header().Get(http.TrailerPrefix+TrafficPolicyRevisionHeader) != "" {
		t.Fatal("forged proof trailer escaped")
	}
	fresh := serviceSnapshotRequest(proxy)
	if fresh.Code != http.StatusForbidden || fresh.Header().Get(TrafficPolicyRevisionHeader) == revision || attempts != 2 {
		t.Fatalf("fresh = %d proof=%q attempts=%d", fresh.Code, fresh.Header().Get(TrafficPolicyRevisionHeader), attempts)
	}
}

func TestServicePolicySnapshotFailurePrecedesEndpointWakeForward(t *testing.T) {
	for _, mode := range []string{"store", "timeout", "missing revision", "foreign caller"} {
		t.Run(mode, func(t *testing.T) {
			provider := &serviceProxyProvider{}
			proxy := NewServiceProxy(ServiceProxyConfig{Provider: provider,
				Policy: func(ctx context.Context, _, _ string, _ bool) (ServicePolicySnapshot, error) {
					s := serviceSnapshotFixture()
					switch mode {
					case "store":
						return s, errors.New("database offline")
					case "timeout":
						<-ctx.Done()
						return s, nil
					case "missing revision":
						s.InputRevision = ""
					case "foreign caller":
						s.Caller.AppID = "foreign"
					}
					return s, nil
				},
				Wake:    func(context.Context, string) error { t.Fatal("wake reached"); return nil },
				Forward: func(Target) http.Handler { t.Fatal("forward reached"); return nil },
			})
			started := time.Now()
			w := serviceSnapshotRequest(proxy)
			if w.Code != http.StatusServiceUnavailable || provider.calls.Load() != 0 || w.Header().Get(TrafficPolicyRevisionHeader) != "" {
				t.Fatalf("refusal=%d endpoint reads=%d", w.Code, provider.calls.Load())
			}
			if time.Since(started) > time.Second {
				t.Fatal("snapshot read exceeded bound")
			}
		})
	}
}

func TestServicePolicyReliabilityBudgetIncludesSnapshotTime(t *testing.T) {
	provider := &serviceProxyProvider{}
	signer, err := trafficdeadline.New([]byte(strings.Repeat("x", 32)), nil)
	if err != nil {
		t.Fatal(err)
	}
	proxy := NewServiceProxy(ServiceProxyConfig{Provider: provider, TrafficDeadlines: signer,
		Policy: func(ctx context.Context, _, _ string, _ bool) (ServicePolicySnapshot, error) {
			timer := time.NewTimer(70 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				return ServicePolicySnapshot{}, ctx.Err()
			}
			s := serviceSnapshotFixture()
			s.Caller.Reliability = &api.ServiceReliabilityPolicy{TimeoutMS: 30}
			return s, nil
		},
		Wake:    func(context.Context, string) error { t.Fatal("expired call woke target"); return nil },
		Forward: func(Target) http.Handler { t.Fatal("expired call forwarded"); return nil },
	})
	w := serviceSnapshotRequest(proxy)
	if w.Code != http.StatusGatewayTimeout {
		t.Fatalf("expired configured call = %d %q", w.Code, w.Body.String())
	}
}

// A read that overruns its own budget while the call is live gets one more
// attempt; a store error is not retried. Each refusal is logged with its cause.
func TestServicePolicySnapshotRetriesOneStalledRead(t *testing.T) {
	for _, tc := range []struct {
		name      string
		failures  int
		storeErr  bool
		wantCode  int
		wantReads int32
		wantLog   bool
	}{
		{name: "one stall recovers", failures: 1, wantCode: http.StatusServiceUnavailable + 1, wantReads: 2},
		{name: "two stalls refuse", failures: 2, wantCode: http.StatusServiceUnavailable, wantReads: 2, wantLog: true},
		{name: "store error is not retried", failures: 1, storeErr: true, wantCode: http.StatusServiceUnavailable, wantReads: 1, wantLog: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reads atomic.Int32
			var logs bytes.Buffer
			proxy := NewServiceProxy(ServiceProxyConfig{Provider: &serviceProxyProvider{},
				Log: slog.New(slog.NewTextHandler(&logs, nil)),
				Policy: func(ctx context.Context, _, _ string, _ bool) (ServicePolicySnapshot, error) {
					if int(reads.Add(1)) <= tc.failures {
						if tc.storeErr {
							return ServicePolicySnapshot{}, errors.New("database offline")
						}
						<-ctx.Done()
						return ServicePolicySnapshot{}, ctx.Err()
					}
					return serviceSnapshotFixture(), nil
				},
				Wake: func(context.Context, string) error { return nil },
				Forward: func(Target) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable + 1) })
				},
			})
			w := serviceSnapshotRequest(proxy)
			if reads.Load() != tc.wantReads {
				t.Fatalf("policy reads = %d, want %d", reads.Load(), tc.wantReads)
			}
			if tc.wantCode == http.StatusServiceUnavailable && w.Code != tc.wantCode {
				t.Fatalf("code = %d, want 503", w.Code)
			}
			if tc.wantCode != http.StatusServiceUnavailable && strings.Contains(w.Body.String(), "service policy snapshot is unavailable") {
				t.Fatalf("recovered read still refused: %d %s", w.Code, w.Body.String())
			}
			if got := strings.Contains(logs.String(), "policy snapshot unavailable"); got != tc.wantLog {
				t.Fatalf("refusal logged = %v, want %v: %s", got, tc.wantLog, logs.String())
			}
		})
	}
}
