package outbound

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// An evaluation of an account with none running is claimed before an older
// one of an account already running one, so one account's queue of
// evaluations cannot hold every other account's behind it.
func TestPostgresAIEvaluationClaimsAreFairAcrossAccounts(t *testing.T) {
	f := newAIPreviewFixture(t, okChatCompletion, state.OutboundAIPolicy{RequestLogEnabled: true})
	ctx := context.Background()
	backend, err := NewPostgresBackend(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	integration := func(account state.Account, name string) state.OutboundIntegrationOffer {
		t.Helper()
		offer, err := f.store.CreateOutboundIntegration(ctx, state.OutboundIntegrationOffer{
			ID: uuid.NewString(), AccountID: account.ID, Name: name, Origin: f.upstream.URL,
			AllowedMethods: []string{http.MethodPost}, AllowedPathPrefixes: []string{"/v1"}, Enabled: true,
			CredentialSource: CredentialSourceCustomerSealed, OwnerKind: IntegrationOwnerCustomer,
			RequestPolicy: api.OutboundRequestPolicy{RatePerSecond: 100, Burst: 20, MaxInFlight: 20, RequestTimeoutMS: 30_000},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.store.SetOutboundAIPolicy(ctx, account.ID, offer.ID, state.OutboundAIPolicy{Provider: api.OutboundAIProviderOpenAI}); err != nil {
			t.Fatal(err)
		}
		return offer
	}
	evaluate := func(account state.Account, baseline, candidate state.OutboundIntegrationOffer) string {
		t.Helper()
		route, err := f.store.CreateOutboundAIRoute(ctx, state.OutboundAIRoute{ID: uuid.NewString(), AccountID: account.ID, Name: "chat-" + uuid.NewString()[:8],
			Targets: []state.OutboundAIRouteTarget{{IntegrationID: baseline.ID}, {IntegrationID: candidate.ID, Model: "gpt-new"}}})
		if err != nil {
			t.Fatal(err)
		}
		created, err := f.store.CreateOutboundAIEvaluation(ctx, state.OutboundAIEvaluation{AccountID: account.ID, RouteID: route.ID,
			Position: 2, JudgeIntegrationID: baseline.ID, JudgeModel: "judge", SampleSize: 5, MaxLossRate: 20})
		if err != nil {
			t.Fatal(err)
		}
		return created.ID
	}
	busyCandidate := integration(f.account, "llm-new")
	first := evaluate(f.account, f.offer, busyCandidate)
	evaluate(f.account, f.offer, busyCandidate)
	other, err := f.store.CreateAccount(ctx, fmt.Sprintf("ai-other-%s@example.com", uuid.NewString()), api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	waiting := evaluate(other, integration(other, "llm"), integration(other, "llm-new"))

	if job, ok, err := backend.ClaimAIEvaluation(ctx); err != nil || !ok || job.ID != first {
		t.Fatalf("first claim = %s %v %v, want the oldest evaluation", job.ID, ok, err)
	}
	if job, ok, err := backend.ClaimAIEvaluation(ctx); err != nil || !ok || job.ID != waiting {
		t.Fatalf("second claim = %s %v %v, want the other account's evaluation before the busy account's second", job.ID, ok, err)
	}
}
