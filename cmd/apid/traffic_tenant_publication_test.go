// adr: 531
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type refusingTrafficTenantStore struct {
	*state.MemStore
	refusal error
	calls   int
}

func (s *refusingTrafficTenantStore) MarkTenantHostnameVerifiedIfChallenge(ctx context.Context, host, token string) (bool, error) {
	s.calls++
	if s.refusal != nil {
		return false, fmt.Errorf("private tenant publication: %w", s.refusal)
	}
	return s.MemStore.MarkTenantHostnameVerifiedIfChallenge(ctx, host, token)
}

func (s *refusingTrafficTenantStore) SetPlatformTenantStatus(ctx context.Context, account, tenant, status string) (state.PlatformTenant, error) {
	s.calls++
	if s.refusal != nil {
		return state.PlatformTenant{}, fmt.Errorf("private tenant publication: %w", s.refusal)
	}
	return s.MemStore.SetPlatformTenantStatus(ctx, account, tenant, status)
}

func (s *refusingTrafficTenantStore) LinkPlatformTenantSurface(ctx context.Context, account, tenant, surface string) (state.TenantSurface, error) {
	s.calls++
	if s.refusal != nil {
		return state.TenantSurface{}, fmt.Errorf("private tenant publication: %w", s.refusal)
	}
	return s.MemStore.LinkPlatformTenantSurface(ctx, account, tenant, surface)
}

func trafficTenantAPIFixture(t *testing.T, e testEnv) (state.TenantSurface, state.TenantHostname) {
	t.Helper()
	appID := mustSeedApp(t, e, "traffic-tenant-api")
	limits := api.MustLimitsFor(e.acct.Plan)
	surface, err := e.store.CreateTenantSurfaceIfUnderQuota(t.Context(), state.CreateTenantSurfaceParams{AccountID: e.acct.ID, AppID: appID, Name: "traffic-tenant-api"}, limits)
	if err != nil {
		t.Fatal(err)
	}
	host, err := e.store.CreateTenantHostnameIfUnderQuota(t.Context(), state.CreateTenantHostnameParams{SurfaceID: surface.ID, Hostname: "tenant-api.example.test", ChallengeToken: "original-private-token"}, limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpdateTenantSurfaceStatus(t.Context(), surface.ID, state.SurfaceStatusActive); err != nil {
		t.Fatal(err)
	}
	return surface, host
}

func TestTrafficTenantDNSPublicationRefusalStaleAndRepair(t *testing.T) {
	for _, mode := range []string{"aggregate", "analysis", "replacement", "missing-seam"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "true")
			e, _ := newTestServerWithCapturingNotifier(t, api.PlanPro)
			surface, host := trafficTenantAPIFixture(t, e)
			refusing := &refusingTrafficTenantStore{MemStore: e.store}
			switch mode {
			case "aggregate":
				refusing.refusal = &state.TrafficPolicyAggregateError{Scope: "host_rule_projection", Host: "private.host.example", Unit: "bytes", Limit: api.TrafficPolicyMaxHostBytes, Observed: api.TrafficPolicyMaxHostBytes + 1}
			case "analysis":
				refusing.refusal = &state.TrafficPolicyAnalysisError{Scope: "states", Unit: "states", Limit: api.TrafficPolicyMaxAnalysisStates, Observed: api.TrafficPolicyMaxAnalysisStates + 1}
			}
			e.s.store = refusing
			if mode == "missing-seam" {
				e.s.store = struct{ state.Store }{e.store}
			}
			previousLookup := txtLookupFunc
			t.Cleanup(func() { txtLookupFunc = previousLookup })
			txtLookupFunc = func(ctx context.Context, _ string) ([]string, error) {
				if mode == "replacement" {
					if err := e.store.DeleteTenantHostname(ctx, host.Hostname); err != nil {
						t.Fatal(err)
					}
					var err error
					host, err = e.store.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{SurfaceID: surface.ID, Hostname: host.Hostname, ChallengeToken: "replacement-private-token"}, api.MustLimitsFor(e.acct.Plan))
					if err != nil {
						t.Fatal(err)
					}
				}
				return []string{"original-private-token"}, nil
			}
			eventsBefore, err := e.store.ListEvents(t.Context(), "", 0)
			if err != nil {
				t.Fatal(err)
			}
			e.s.runVerifyOnce(t.Context(), slog.Default())
			after, err := e.store.GetTenantHostnameByName(t.Context(), host.Hostname)
			eventsAfter, auditErr := e.store.ListEvents(t.Context(), "", 0)
			if err != nil || auditErr != nil || !reflect.DeepEqual(host, after) || len(eventsBefore) != len(eventsAfter) {
				t.Fatalf("refused DNS publication changed claim or emitted audit: hostErr=%v auditErr=%v", err, auditErr)
			}
			refusing.refusal = nil
			e.s.store = refusing
			txtLookupFunc = func(context.Context, string) ([]string, error) { return []string{host.ChallengeToken}, nil }
			e.s.runVerifyOnce(t.Context(), slog.Default())
			after, err = e.store.GetTenantHostnameByName(t.Context(), host.Hostname)
			eventsAfter, auditErr = e.store.ListEvents(t.Context(), "", 0)
			if err != nil || auditErr != nil || !after.Verified() || len(eventsAfter) != len(eventsBefore)+1 || eventsAfter[0].Kind != "tenant_hostname.verified" {
				t.Fatalf("repaired DNS publication: verified=%v events=%d err=%v/%v", after.Verified(), len(eventsAfter), err, auditErr)
			}
		})
	}
}

func TestTrafficTenantHTTPPublicationRefusalAndRepair(t *testing.T) {
	for _, mode := range []string{"reactivate", "link"} {
		for _, kind := range []string{"aggregate", "analysis"} {
			t.Run(mode+"/"+kind, func(t *testing.T) {
				e, _ := newTestServerWithCapturingNotifier(t, api.PlanPro)
				surface, _ := trafficTenantAPIFixture(t, e)
				tenant, _, err := e.store.CreatePlatformTenant(t.Context(), e.acct.ID, "traffic-api-customer", "Traffic API customer", e.acct.Plan.ConsumerKeysPerAccount())
				if err != nil {
					t.Fatal(err)
				}
				method, path, request := http.MethodPost, "/v1/account/platform-tenants/"+tenant.ID+"/surfaces", any(api.LinkPlatformTenantSurfaceRequest{SurfaceID: surface.ID})
				if mode == "reactivate" {
					if _, err := e.store.SetPlatformTenantStatus(t.Context(), e.acct.ID, tenant.ID, state.PlatformTenantSuspended); err != nil {
						t.Fatal(err)
					}
					method, path, request = http.MethodPatch, "/v1/account/platform-tenants/"+tenant.ID, api.SetPlatformTenantStatusRequest{Status: state.PlatformTenantActive}
				}
				code := api.CodeTrafficPolicyTooLarge
				var refusal error = &state.TrafficPolicyAggregateError{Scope: "host_rule_projection", Host: "private.tenant.example", Unit: "bytes", Limit: api.TrafficPolicyMaxHostBytes, Observed: api.TrafficPolicyMaxHostBytes + 1}
				if kind == "analysis" {
					code = api.CodeTrafficPolicyTooComplex
					refusal = &state.TrafficPolicyAnalysisError{Scope: "states", Unit: "states", Limit: api.TrafficPolicyMaxAnalysisStates, Observed: api.TrafficPolicyMaxAnalysisStates + 1}
				}
				refusing := &refusingTrafficTenantStore{MemStore: e.store, refusal: refusal}
				e.s.store = refusing
				eventsBefore, err := e.store.ListEvents(t.Context(), "", 0)
				if err != nil {
					t.Fatal(err)
				}
				response := e.do(t, method, path, request, nil)
				var body map[string]any
				if response.Code != http.StatusUnprocessableEntity || json.Unmarshal(response.Body.Bytes(), &body) != nil || body["code"] != code || body["docs_url"] == nil || strings.Contains(response.Body.String(), "private.") {
					t.Fatalf("publication problem: status=%d body=%s", response.Code, response.Body.String())
				}
				eventsAfter, err := e.store.ListEvents(t.Context(), "", 0)
				if err != nil || len(eventsBefore) != len(eventsAfter) || refusing.calls != 1 {
					t.Fatalf("refused API publication emitted audit: calls=%d err=%v", refusing.calls, err)
				}
				refusing.refusal = nil
				if response := e.do(t, method, path, request, nil); response.Code != http.StatusOK {
					t.Fatalf("publication retry: status=%d body=%s", response.Code, response.Body.String())
				}
			})
		}
	}
}

func (s *refusingTrafficTenantStore) DeleteTenantHostnameForSurface(ctx context.Context, host, surface string) error {
	s.calls++
	if s.refusal != nil {
		return fmt.Errorf("private transition witness: %w", s.refusal)
	}
	return s.MemStore.DeleteTenantHostnameForSurface(ctx, host, surface)
}

func (s *refusingTrafficTenantStore) DeleteTenantSurfaceWithHostnames(ctx context.Context, surface, account string) error {
	s.calls++
	if s.refusal != nil {
		return fmt.Errorf("private transition witness: %w", s.refusal)
	}
	return s.MemStore.DeleteTenantSurfaceWithHostnames(ctx, surface, account)
}

func TestTrafficTenantAPIRemovalRefusalPrivacyAndRepair(t *testing.T) {
	for _, mode := range []string{"hostname", "cascade", "missing-seam"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "true")
			e, _ := newTestServerWithCapturingNotifier(t, api.PlanPro)
			surface, host := trafficTenantAPIFixture(t, e)
			refusal := &state.TrafficPolicyAggregateError{Scope: "private_owner_scope", Host: "private.foreign.example", Unit: "bytes", Limit: api.TrafficPolicyMaxHostBytes, Observed: api.TrafficPolicyMaxHostBytes + 8123}
			refusing := &refusingTrafficTenantStore{MemStore: e.store, refusal: refusal}
			e.s.store = refusing
			path := "/v1/apps/traffic-tenant-api/tenant-surfaces/" + surface.ID
			if mode == "hostname" {
				path += "/hostnames/" + host.Hostname
			}
			if mode == "missing-seam" {
				e.s.store = struct{ state.Store }{e.store}
			}
			before, err := e.store.ListEvents(t.Context(), "", 0)
			if err != nil {
				t.Fatal(err)
			}
			response := e.do(t, http.MethodDelete, path, nil, nil)
			if mode == "missing-seam" {
				if response.Code != http.StatusInternalServerError && response.Code != http.StatusServiceUnavailable {
					t.Fatalf("missing atomic seam: %d", response.Code)
				}
			} else {
				var body map[string]any
				if response.Code != http.StatusUnprocessableEntity || json.Unmarshal(response.Body.Bytes(), &body) != nil || body["code"] != api.CodeTrafficPolicyTooLarge || body["observed"] != float64(refusal.Limit+1) || body["docs_url"] == nil || strings.Contains(response.Body.String(), "private.") || strings.Contains(response.Body.String(), "private_owner_scope") {
					t.Fatalf("private binding refusal: status=%d body=%s", response.Code, response.Body.String())
				}
			}
			after, err := e.store.ListEvents(t.Context(), "", 0)
			if err != nil || len(after) != len(before) {
				t.Fatal("refusal emitted audit")
			}
			if rows, err := e.store.ListTenantHostnamesForSurface(t.Context(), surface.ID); err != nil || len(rows) != 1 || rows[0].ID != host.ID {
				t.Fatal("refusal removed a hostname")
			}
			e.s.store = refusing
			refusing.refusal = nil
			if response := e.do(t, http.MethodDelete, path, nil, nil); response.Code != http.StatusNoContent {
				t.Fatalf("repair: %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestTrafficTenantBindingProblemRedactsForeignCounts(t *testing.T) {
	for _, refusal := range []error{
		&state.TrafficPolicyProjectionError{Scope: "private_scope", Limit: 100, Observed: 999},
		&state.TrafficPolicyAggregateError{Scope: "private_scope", Host: "private.example", Unit: "rules", Limit: 100, Observed: 999},
		&state.TrafficPolicyAnalysisError{Scope: "private_scope", Unit: "states", Limit: 100, Observed: 999},
	} {
		problem := tenantBindingWriteProblem(fmt.Errorf("private owner: %w", refusal), api.ErrInternal("fallback"))
		encoded, err := json.Marshal(problem)
		if err != nil || strings.Contains(string(encoded), "private") || strings.Contains(string(encoded), "999") {
			t.Fatalf("binding problem disclosed another owner: %s", encoded)
		}
		var body map[string]any
		if json.Unmarshal(encoded, &body) != nil || body["observed"] != float64(101) || body["docs_url"] == nil {
			t.Fatalf("binding problem omitted lower bound: %s", encoded)
		}
	}
}
