// adr: 531
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

type refusingTrafficDomainPublicationStore struct {
	*state.MemStore
	refusal error
	calls   int
}

func (s *refusingTrafficDomainPublicationStore) CreateCustomDomainIfUnderQuota(ctx context.Context, domain, app, token string, perApp, perAccount int) (state.CustomDomain, error) {
	s.calls++
	if s.refusal != nil {
		return state.CustomDomain{}, fmt.Errorf("private.foreign.example: %w", s.refusal)
	}
	return s.MemStore.CreateCustomDomainIfUnderQuota(ctx, domain, app, token, perApp, perAccount)
}

func (s *refusingTrafficDomainPublicationStore) CreateCustomDomainIfUnderQuotaWithActivity(ctx context.Context, domain, app, token string, perApp, perAccount int, entry state.OrgActivity) (state.CustomDomain, int64, error) {
	s.calls++
	if s.refusal != nil {
		return state.CustomDomain{}, 0, fmt.Errorf("private.foreign.example: %w", s.refusal)
	}
	return s.MemStore.CreateCustomDomainIfUnderQuotaWithActivity(ctx, domain, app, token, perApp, perAccount, entry)
}

func TestTrafficDomainPublicationHTTPRefusalPrivacyAndRepair(t *testing.T) {
	for _, fixture := range []struct {
		name, code string
		status     int
		refusal    error
	}{
		{"aggregate", api.CodeTrafficPolicyTooLarge, http.StatusUnprocessableEntity, &state.TrafficPolicyAggregateError{Scope: "private_foreign_scope", Host: "private.foreign.example", Unit: "bytes", Limit: api.TrafficPolicyMaxHostBytes, Observed: 2*api.TrafficPolicyMaxHostBytes + 987}},
		{"analysis", api.CodeTrafficPolicyTooComplex, http.StatusUnprocessableEntity, &state.TrafficPolicyAnalysisError{Scope: "private_foreign_scope", Unit: "states", Limit: api.TrafficPolicyMaxAnalysisStates, Observed: 2*api.TrafficPolicyMaxAnalysisStates + 987}},
		{"store-outage", "", http.StatusServiceUnavailable, errors.New("private_foreign_scope")},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			e, notifier := newTestServerWithCapturingNotifier(t, api.PlanPro)
			mustSeedDeployment(t, e, "publication-app")
			refusing := &refusingTrafficDomainPublicationStore{MemStore: e.store, refusal: fixture.refusal}
			e.s.store = refusing
			notifier.mu.Lock()
			notifications := len(notifier.emitted)
			notifier.mu.Unlock()
			eventsBefore, err := e.store.ListEvents(t.Context(), e.acct.ID, 0)
			if err != nil {
				t.Fatal(err)
			}
			const domain = "publication.example.test"
			request := map[string]any{"app_id": "publication-app", "domain": domain}
			response := e.do(t, http.MethodPost, "/v1/domains", request, nil)
			if response.Code != fixture.status {
				t.Fatalf("refusal status=%d body=%s", response.Code, response.Body.String())
			}
			bodyText := response.Body.String()
			if strings.Contains(bodyText, "private.foreign") || strings.Contains(bodyText, "private_foreign") || strings.Contains(bodyText, "987") {
				t.Fatal("publication refusal disclosed foreign policy evidence")
			}
			if fixture.code != "" {
				var body map[string]any
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body["code"] != fixture.code || body["docs_url"] == nil {
					t.Fatalf("stable publication problem missing: body=%+v err=%v", body, err)
				}
				if body["observed"].(float64) != body["limit"].(float64)+1 || body["scope"] != nil || !strings.Contains(body["detail"].(string), "claim remains unchanged") {
					t.Fatalf("publication returned foreign evidence: %+v", body)
				}
			}
			if refusing.calls != 1 {
				t.Fatalf("publication calls=%d", refusing.calls)
			}
			if _, err := e.store.DomainByName(t.Context(), domain); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("refused publication saved claim: %v", err)
			}
			eventsAfter, err := e.store.ListEvents(t.Context(), e.acct.ID, 0)
			notifier.mu.Lock()
			changed := len(notifier.emitted) != notifications
			notifier.mu.Unlock()
			if err != nil || len(eventsBefore) != len(eventsAfter) || changed {
				t.Fatal("refused publication emitted audit or notification")
			}
			refusing.refusal = nil
			response = e.do(t, http.MethodPost, "/v1/domains", request, nil)
			if response.Code != http.StatusAccepted {
				t.Fatalf("repair/retry status=%d body=%s", response.Code, response.Body.String())
			}
			if claim, err := e.store.DomainByName(t.Context(), domain); err != nil || claim.Verified() {
				t.Fatalf("successful retry did not save pending claim: domain=%q err=%v", claim.Domain, err)
			}
		})
	}
}
