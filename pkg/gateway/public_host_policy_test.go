// adr: 531
package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/trafficrevocation"
)

type unavailableHostPolicyBackend struct {
	*fakeBackend
	wait bool
}

func (b unavailableHostPolicyBackend) LookupHostPolicy(ctx context.Context, _ string) (App, bool, error) {
	if b.wait {
		<-ctx.Done()
		return b.app, true, nil // Even a loader ignoring expiry cannot publish.
	}
	return App{}, false, errors.New("authoritative host policy unavailable")
}

func TestPublicHostPolicyUnavailableRefusesBeforeGuestOrWake(t *testing.T) {
	for _, wait := range []bool{false, true} {
		t.Run(map[bool]string{false: "outage", true: "expired"}[wait], func(t *testing.T) {
			h, backend, _ := newTestHandler(t)
			h.backend = unavailableHostPolicyBackend{fakeBackend: backend, wait: wait}
			forwarded := false
			h.WithForwarding(func(Target) http.Handler {
				return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { forwarded = true })
			})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil))
			if rec.Code != http.StatusServiceUnavailable || backend.admits != 0 || forwarded {
				t.Fatalf("host refusal=%d wake=%d forwarded=%v", rec.Code, backend.admits, forwarded)
			}
		})
	}
}

func TestPublicHostPolicyOwnerFailureCannotBecomeSyntheticRoute(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	backend.app.AccountID = "account"
	h.backend = unavailableHostPolicyBackend{fakeBackend: backend}
	entry := &HostEntry{Host: backend.host, Route: []EdgeRuleResolved{{ID: "route", AccountID: "account", TargetAppSlug: "target"}}}
	if err := entry.SealPolicy(); err != nil {
		t.Fatal(err)
	}
	targetLoads := 0
	h.WithEdgeRules(hostPolicyRouteMatcher{snapshotTestMatcher{entry: entry}}, func(context.Context, string) (App, bool) {
		targetLoads++
		return App{ID: "target", AccountID: "account", Plan: api.PlanPro}, true
	}, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil))
	if rec.Code != http.StatusServiceUnavailable || targetLoads != 0 || backend.admits != 0 {
		t.Fatalf("unverified owner accepted synthetic route: status=%d targets=%d wakes=%d", rec.Code, targetLoads, backend.admits)
	}
}

type hostPolicyRouteMatcher struct{ snapshotTestMatcher }

func (m hostPolicyRouteMatcher) MatchRoute(ctx context.Context, host, _, _ string) *EdgeRuleResolved {
	entry, ok := PinnedHostPolicy(ctx, host)
	if !ok || len(entry.Route) == 0 {
		return nil
	}
	return &entry.Route[0]
}

type routeSourcePolicyBackend struct {
	*fakeBackend
	source  App
	found   bool
	onAdmit func()
}

func (b *routeSourcePolicyBackend) LookupHostPolicy(context.Context, string) (App, bool, error) {
	return b.source, b.found, nil
}

func (b *routeSourcePolicyBackend) Admit(ctx context.Context, app, deployment, scope, trigger string, maximum int) (string, WakeMethod, bool, error) {
	if b.onAdmit != nil {
		b.onAdmit()
	}
	return b.fakeBackend.Admit(ctx, app, deployment, scope, trigger, maximum)
}

func publicRouteSourceHandlerFixture(t *testing.T, claimed bool) (*Handler, *routeSourcePolicyBackend, string) {
	t.Helper()
	h, base, _ := newTestHandler(t)
	base.app.ID, base.app.AccountID = uuid.NewString(), uuid.NewString()
	base.app.PublicAuth.Mode = publicAuthModeOpen
	base.app.RequestInvocationsEnabled = true
	base.app.PublicPolicySource = &PublicAppPolicySource{Slug: "target", Revision: "target-old"}
	source := App{PublicPolicySource: &PublicAppPolicySource{Host: base.host, Revision: "source-old", CanSubstitute: true}}
	if claimed {
		source.ID, source.AccountID = uuid.NewString(), base.app.AccountID
	}
	backend := &routeSourcePolicyBackend{fakeBackend: base, source: source, found: claimed}
	h.backend = backend
	entry := &HostEntry{Host: base.host, Route: []EdgeRuleResolved{{ID: "route", AccountID: base.app.AccountID, TargetAppSlug: "target"}}}
	if err := entry.SealPolicy(); err != nil {
		t.Fatal(err)
	}
	h.WithEdgeRules(hostPolicyRouteMatcher{snapshotTestMatcher{entry: entry}}, func(context.Context, string) (App, bool) {
		return base.app, true
	}, nil)
	deployment := uuid.NewString()
	h.WithPublicRoutingPolicy(func(_ context.Context, app App, inputs PublicRoutingInputs) (PublicRoutingSnapshot, error) {
		claim := app.PublicRouteSource
		if claim == nil || claim.Source == nil || claim.Found != claimed || claim.Source.Revision != "source-old" || app.PublicPolicySource.Revision != "target-old" {
			return PublicRoutingSnapshot{}, errors.New("substitution lost its source or target baseline")
		}
		snapshot := publicSnapshotFixture(app, inputs)
		snapshot.Weights = []DeploymentWeightsRow{{ID: deployment, TrafficPercent: 100}}
		return snapshot, nil
	})
	return h, backend, deployment
}

func TestPublicRouteSourceHandlerPinsClaimAndTargetThroughWake(t *testing.T) {
	for _, claimed := range []bool{false, true} {
		t.Run(fmt.Sprint(claimed), func(t *testing.T) {
			h, backend, deployment := publicRouteSourceHandlerFixture(t, claimed)
			var before string
			var approved App
			var approvedContext context.Context
			pin := h.publicRoutingPolicy
			h.WithPublicRoutingPolicy(func(ctx context.Context, app App, inputs PublicRoutingInputs) (PublicRoutingSnapshot, error) {
				before = TrafficPolicyRevision(ctx)
				approved, approvedContext = app, ctx
				return pin(ctx, app, inputs)
			})
			backend.onAdmit = func() {
				backend.source.PublicPolicySource.Revision = "source-new"
				backend.app.PublicPolicySource.Revision = "target-new"
			}
			forwarded := false
			h.WithForwarding(func(target Target) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					forwarded = true
					if target.DeploymentID != deployment || TrafficPolicyRevision(r.Context()) == "" {
						t.Fatal("admitted route or policy proof changed during wake")
					}
					w.WriteHeader(http.StatusNoContent)
				})
			})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil))
			if rec.Code != http.StatusNoContent || !forwarded || before == "" || backend.admits != 1 {
				t.Fatalf("substitution lost a pinned projection: status=%d forward=%v wake=%d body=%s", rec.Code, forwarded, backend.admits, rec.Body)
			}
			// Re-encoding the admitted projection must preserve its fingerprint
			// after both mutable resolver carriers change during the wake.
			_, after, err := freezeTrafficApp(approvedContext, approved)
			if err != nil || after != before || approved.PublicRouteSource.Source.Revision != "source-old" || approved.PublicPolicySource.Revision != "target-old" {
				t.Fatalf("admitted projection retained mutable source data: %s / %s / %v", before, after, err)
			}
		})
	}
}

func TestPublicRouteSourceHandlerRefusesMissingClaimBeforeTargetWork(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	target := backend.app
	target.PublicPolicySource = &PublicAppPolicySource{Slug: "target", Revision: "target-old"}
	entry := &HostEntry{Host: backend.host, Route: []EdgeRuleResolved{{ID: "route", AccountID: backend.app.AccountID, TargetAppSlug: "target"}}}
	if err := entry.SealPolicy(); err != nil {
		t.Fatal(err)
	}
	h.WithEdgeRules(hostPolicyRouteMatcher{snapshotTestMatcher{entry: entry}}, func(context.Context, string) (App, bool) {
		return target, true
	}, nil)
	forwarded := false
	h.WithForwarding(func(Target) http.Handler {
		return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { forwarded = true })
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil))
	if rec.Code != http.StatusServiceUnavailable || backend.admits != 0 || forwarded {
		t.Fatalf("missing claim authorized a substitution: status=%d wake=%d forward=%v", rec.Code, backend.admits, forwarded)
	}
}

func TestPublicRouteSourceHandlerPreservesReservedAndExactHosts(t *testing.T) {
	for _, found := range []bool{false, true} {
		t.Run(fmt.Sprint(found), func(t *testing.T) {
			h, backend, _ := publicRouteSourceHandlerFixture(t, found)
			backend.source = backend.app
			backend.source.ID = uuid.NewString()
			backend.source.PublicPolicySource = &PublicAppPolicySource{Host: backend.host, Revision: "reserved", CanSubstitute: false}
			backend.source.PinnedDeploymentID = uuid.NewString()
			h.publicRoutingPolicy = nil // Ordinary fixture admission, without a substitute target.
			targetLoads := 0
			h.resolveTargetApp = func(context.Context, string) (App, bool) {
				targetLoads++
				return backend.app, true
			}
			forwarded := false
			h.WithForwarding(func(target Target) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					forwarded = true
					if target.AppID != backend.source.ID || target.DeploymentID != backend.source.PinnedDeploymentID {
						t.Fatalf("exact host changed its app or deployment: %+v", target)
					}
					w.WriteHeader(http.StatusNoContent)
				})
			})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil))
			wantStatus, wantWakes := http.StatusNotFound, int32(0)
			if found {
				wantStatus, wantWakes = http.StatusNoContent, 1
			}
			if rec.Code != wantStatus || backend.admits != wantWakes || forwarded != found || targetLoads != 0 {
				t.Fatalf("reserved host substituted: status=%d wake=%d forward=%v targets=%d body=%s", rec.Code, backend.admits, forwarded, targetLoads, rec.Body)
			}
		})
	}
}

func TestPublicRouteSourceSecurityFenceRefusesBeforeWake(t *testing.T) {
	h, backend, _ := publicRouteSourceHandlerFixture(t, true)
	store := &gatewaySecurityStore{}
	store.set(trafficrevocation.Scope{Kind: "app", ID: backend.source.ID}, 1, true)
	registry := trafficrevocation.New(store)
	defer registry.Close()
	h.WithTrafficRevocations(registry)
	forwarded := false
	h.WithForwarding(func(Target) http.Handler {
		return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { forwarded = true })
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil))
	if rec.Code != http.StatusForbidden || backend.admits != 0 || forwarded || !strings.Contains(rec.Body.String(), api.CodeTrafficRevoked) {
		t.Fatalf("revoked source authorized wake: status=%d wake=%d forward=%v body=%s", rec.Code, backend.admits, forwarded, rec.Body)
	}
	if exchanges, scopes := registry.Tracked(); exchanges != 0 || scopes != 0 {
		t.Fatalf("source refusal retained registrations: %d/%d", exchanges, scopes)
	}
}

func TestPublicRouteSourceSecurityFenceCancelsHTTPExchangeUntilCleanup(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(fmt.Sprint(h2), func(t *testing.T) {
			h, backend, _ := publicRouteSourceHandlerFixture(t, true)
			store := &gatewaySecurityStore{}
			registry := trafficrevocation.New(store)
			defer registry.Close()
			h.WithTrafficRevocations(registry)
			started := make(chan context.Context, 1)
			canceled := make(chan error, 1)
			cleanup := make(chan struct{})
			h.WithForwarding(func(Target) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					started <- r.Context()
					<-r.Context().Done()
					canceled <- trafficRevocationCause(r.Context())
					<-cleanup
					handleForwardRequestCancellation(w, r, true)
				})
			})
			server := httptest.NewUnstartedServer(h)
			server.EnableHTTP2 = h2
			if h2 {
				server.StartTLS()
			} else {
				server.Start()
			}
			defer server.Close()
			defer func() {
				select {
				case <-cleanup:
				default:
					close(cleanup)
				}
			}()
			server.Client().Timeout = 2 * time.Second
			result := make(chan error, 1)
			go func() {
				request, _ := http.NewRequest(http.MethodGet, server.URL, nil)
				request.Host = backend.host
				response, err := server.Client().Do(request)
				if err == nil {
					defer response.Body.Close()
					body, readErr := io.ReadAll(response.Body)
					if readErr != nil {
						err = readErr
					} else if response.StatusCode != http.StatusForbidden || !strings.Contains(string(body), api.CodeTrafficRevoked) {
						err = fmt.Errorf("source revocation returned %d: %s", response.StatusCode, body)
					}
				}
				result <- err
			}()
			select {
			case ctx := <-started:
				if len(trafficSecuritySnapshot(ctx)) != 4 {
					t.Fatal("response lifetime did not retain account, source, target and deployment")
				}
			case <-time.After(time.Second):
				t.Fatal("route did not start forwarding")
			}
			store.set(trafficrevocation.Scope{Kind: "app", ID: backend.source.ID}, 1, true)
			if err := registry.Refresh(t.Context()); err != nil {
				t.Fatal(err)
			}
			select {
			case cause := <-canceled:
				if !errors.Is(cause, trafficrevocation.ErrRevoked) {
					t.Fatalf("source did not cancel target exchange: %v", cause)
				}
			case <-time.After(time.Second):
				t.Fatal("target exchange survived source revocation")
			}
			if exchanges, scopes := registry.Tracked(); exchanges == 0 || scopes != 4 {
				t.Fatalf("source cancellation released forwarding ownership early: %d/%d", exchanges, scopes)
			}
			close(cleanup)
			select {
			case err := <-result:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("forward cleanup retained request")
			}
			if exchanges, scopes := registry.Tracked(); exchanges != 0 || scopes != 0 {
				t.Fatalf("completed source exchange leaked registrations: %d/%d", exchanges, scopes)
			}
		})
	}
}
