// adr: 570
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type refusingTrafficDomainRemovalStore struct {
	*state.MemStore
	refusal       error
	calls         int
	authorizedApp string
}

func (s *refusingTrafficDomainRemovalStore) DeleteCustomDomainForApp(ctx context.Context, domain, app string) error {
	s.calls++
	s.authorizedApp = app
	if s.refusal != nil {
		return fmt.Errorf("private.foreign.example: %w", s.refusal)
	}
	return s.MemStore.DeleteCustomDomainForApp(ctx, domain, app)
}

func (s *refusingTrafficDomainRemovalStore) DeleteCustomDomainForAppWithActivity(ctx context.Context, domain, app string, entry state.OrgActivity) (int64, error) {
	s.calls++
	s.authorizedApp = app
	if s.refusal != nil {
		return 0, fmt.Errorf("private.foreign.example: %w", s.refusal)
	}
	return s.MemStore.DeleteCustomDomainForAppWithActivity(ctx, domain, app, entry)
}

func TestTrafficDomainRemovalHTTPRefusalPrivacyAndRepair(t *testing.T) {
	for _, fixture := range []struct {
		name, code string
		status     int
		refusal    error
	}{
		{"aggregate", api.CodeTrafficPolicyTooLarge, http.StatusUnprocessableEntity, &state.TrafficPolicyAggregateError{Scope: "private_foreign_scope", Host: "private.foreign.example", Unit: "bytes", Limit: api.TrafficPolicyMaxHostBytes, Observed: 2*api.TrafficPolicyMaxHostBytes + 987}},
		{"analysis", api.CodeTrafficPolicyTooComplex, http.StatusUnprocessableEntity, &state.TrafficPolicyAnalysisError{Scope: "private_foreign_scope", Unit: "states", Limit: api.TrafficPolicyMaxAnalysisStates, Observed: 2*api.TrafficPolicyMaxAnalysisStates + 987}},
		{"stale-owner", "", http.StatusNotFound, state.ErrNotFound},
		{"store-outage", "", http.StatusServiceUnavailable, errors.New("private_foreign_scope")},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			e, notifier := newTestServerWithCapturingNotifier(t, api.PlanPro)
			deployment := mustSeedDeployment(t, e, "domain-removal-app")
			const domain = "remove.example.test"
			if _, err := e.store.CreateCustomDomain(t.Context(), domain, deployment.AppID, "private-token"); err != nil {
				t.Fatal(err)
			}
			if err := e.store.MarkDomainVerified(t.Context(), domain); err != nil {
				t.Fatal(err)
			}
			if err := e.store.SetDefaultCustomDomain(t.Context(), deployment.AppID, domain); err != nil {
				t.Fatal(err)
			}
			refusing := &refusingTrafficDomainRemovalStore{MemStore: e.store, refusal: fixture.refusal}
			e.s.store = refusing
			notifier.mu.Lock()
			notifications := len(notifier.emitted)
			notifier.mu.Unlock()
			eventsBefore, err := e.store.ListEvents(t.Context(), e.acct.ID, 0)
			if err != nil {
				t.Fatal(err)
			}
			response := e.do(t, http.MethodDelete, "/v1/domains/"+domain, nil, nil)
			if response.Code != fixture.status {
				t.Fatalf("refusal status=%d body=%s", response.Code, response.Body.String())
			}
			bodyText := response.Body.String()
			if strings.Contains(bodyText, "private.foreign") || strings.Contains(bodyText, "private_foreign") || strings.Contains(bodyText, "987") || strings.Contains(bodyText, "private-token") {
				t.Fatal("domain refusal disclosed foreign policy evidence")
			}
			if fixture.code != "" {
				var body map[string]any
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body["code"] != fixture.code || body["docs_url"] == nil {
					t.Fatalf("stable removal problem missing: body=%+v err=%v", body, err)
				}
				if body["observed"].(float64) != body["limit"].(float64)+1 {
					t.Fatalf("removal returned exact foreign counts: %+v", body)
				}
			}
			if refusing.calls != 1 || refusing.authorizedApp != deployment.AppID {
				t.Fatalf("removal did not carry authorization: calls=%d app=%q", refusing.calls, refusing.authorizedApp)
			}
			if selected, err := e.store.DefaultCustomDomain(t.Context(), deployment.AppID); err != nil || selected != domain {
				t.Fatalf("refusal changed default domain: %q/%v", selected, err)
			}
			eventsAfter, err := e.store.ListEvents(t.Context(), e.acct.ID, 0)
			notifier.mu.Lock()
			changed := len(notifier.emitted) != notifications
			notifier.mu.Unlock()
			if err != nil || len(eventsBefore) != len(eventsAfter) || changed {
				t.Fatal("refused removal emitted audit or notification")
			}
			refusing.refusal = nil
			response = e.do(t, http.MethodDelete, "/v1/domains/"+domain, nil, nil)
			if response.Code != http.StatusNoContent {
				t.Fatalf("repair/retry status=%d body=%s", response.Code, response.Body.String())
			}
			if _, err := e.store.DomainByName(t.Context(), domain); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("successful retry retained claim: %v", err)
			}
		})
	}
}

type legacyAuthoritativeDomainRemovalStore struct {
	state.Store
	deleted bool
}

func (s *legacyAuthoritativeDomainRemovalStore) WithPublicHostPolicySnapshot(context.Context, func(state.PublicHostPolicyReader) error) error {
	return errors.New("snapshot should not be read by removal")
}

func (s *legacyAuthoritativeDomainRemovalStore) DeleteCustomDomain(ctx context.Context, domain string) error {
	s.deleted = true
	return s.Store.DeleteCustomDomain(ctx, domain)
}

func TestTrafficDomainRemovalHTTPRequiresOwnerBoundAuthoritativeStore(t *testing.T) {
	e, _ := newTestServerWithCapturingNotifier(t, api.PlanPro)
	deployment := mustSeedDeployment(t, e, "missing-owned-removal")
	const domain = "owner-bound.example.test"
	if _, err := e.store.CreateCustomDomain(t.Context(), domain, deployment.AppID, "private-token"); err != nil {
		t.Fatal(err)
	}
	legacy := &legacyAuthoritativeDomainRemovalStore{Store: e.store}
	e.s.store = legacy
	response := e.do(t, http.MethodDelete, "/v1/domains/"+domain, nil, nil)
	if response.Code != http.StatusServiceUnavailable || legacy.deleted {
		t.Fatalf("authoritative store used an unbound deletion: status=%d deleted=%v", response.Code, legacy.deleted)
	}
	if claim, err := e.store.DomainByName(t.Context(), domain); err != nil || claim.AppID != deployment.AppID {
		t.Fatalf("missing removal seam changed claim: %+v/%v", claim, err)
	}
}
