package state_test

// adr: 20261114180740 — managed prompts: the first version is live, an
// identical push stores nothing, targets must be the account's OpenAI-format
// routes or translatable managed AI integrations, live/rollback/canary keep
// the pointers consistent, and the plan's prompt limit holds; in Postgres
// and in memory.

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type promptTestStore interface {
	state.OutboundAIPromptStore
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreateOutboundIntegration(context.Context, state.OutboundIntegrationOffer) (state.OutboundIntegrationOffer, error)
	SetOutboundAIPolicy(context.Context, string, string, state.OutboundAIPolicy) error
	CreateOutboundAIRoute(context.Context, state.OutboundAIRoute) (state.OutboundAIRoute, error)
}

func promptIntegration(t *testing.T, store promptTestStore, accountID, provider string) string {
	t.Helper()
	ctx := context.Background()
	policy := api.OutboundRequestPolicy{RatePerSecond: 10, Burst: 10, MaxInFlight: 10, RequestTimeoutMS: 30_000}
	offer, err := store.CreateOutboundIntegration(ctx, state.OutboundIntegrationOffer{
		ID: uuid.NewString(), AccountID: accountID, Name: "ai-" + uuid.NewString()[:8], Origin: "https://api.openai.com",
		AllowedMethods: []string{http.MethodPost}, AllowedPathPrefixes: []string{"/v1"}, Enabled: true,
		CredentialSource: "customer_sealed", OwnerKind: "customer", RequestPolicy: policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetOutboundAIPolicy(ctx, accountID, offer.ID, state.OutboundAIPolicy{Provider: provider}); err != nil {
		t.Fatal(err)
	}
	return offer.ID
}

func promptPush(accountID, name, text string, target api.OutboundAIPromptTarget) state.OutboundAIPromptPush {
	return state.OutboundAIPromptPush{ID: uuid.NewString(), AccountID: accountID, Name: name, MaxPrompts: 2,
		Version: state.OutboundAIPromptVersion{Messages: []api.OutboundAIPromptMessage{
			{Role: api.OutboundAIPromptRoleSystem, Content: text}, {Role: api.OutboundAIPromptRoleUser, Content: "{{question}}"}},
			Target: target, Defaults: map[string]string{}}}
}

func checkOutboundAIPrompts(t *testing.T, store promptTestStore) {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "prompts-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	openai := promptIntegration(t, store, acct.ID, api.OutboundAIProviderOpenAI)
	claude := promptIntegration(t, store, acct.ID, api.OutboundAIProviderAnthropic)
	nativeRoute, err := store.CreateOutboundAIRoute(ctx, state.OutboundAIRoute{ID: uuid.NewString(), AccountID: acct.ID, Name: "native",
		Format: api.OutboundAIRouteFormatNative, Targets: []state.OutboundAIRouteTarget{{IntegrationID: openai}}})
	if err != nil {
		t.Fatal(err)
	}
	openaiRoute, err := store.CreateOutboundAIRoute(ctx, state.OutboundAIRoute{ID: uuid.NewString(), AccountID: acct.ID, Name: "openai",
		Format: api.OutboundAIRouteFormatOpenAI, Targets: []state.OutboundAIRouteTarget{{IntegrationID: claude, Model: "claude-sonnet-4-5"}}})
	if err != nil {
		t.Fatal(err)
	}
	// Targets the account cannot use are refused.
	for name, target := range map[string]api.OutboundAIPromptTarget{
		"native route":           {RouteID: nativeRoute.ID},
		"unknown route":          {RouteID: uuid.NewString()},
		"claude without a model": {IntegrationID: claude},
		"unknown integration":    {IntegrationID: uuid.NewString(), Model: "gpt-4o"},
	} {
		if _, _, _, err := store.PushOutboundAIPromptVersion(ctx, promptPush(acct.ID, "support", "Be kind.", target)); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("%s: err = %v, want invalid", name, err)
		}
	}
	// The first version is live; an identical push stores nothing.
	prompt, v1, created, err := store.PushOutboundAIPromptVersion(ctx, promptPush(acct.ID, "support", "Be kind.", api.OutboundAIPromptTarget{IntegrationID: openai, Model: "gpt-4o"}))
	if err != nil || !created || v1.Version != 1 || prompt.LiveVersion == nil || *prompt.LiveVersion != 1 {
		t.Fatalf("first push = %+v %+v %v %v", prompt, v1, created, err)
	}
	if _, same, created, err := store.PushOutboundAIPromptVersion(ctx, promptPush(acct.ID, "support", "Be kind.", api.OutboundAIPromptTarget{IntegrationID: openai, Model: "gpt-4o"})); err != nil || created || same.Version != 1 {
		t.Fatalf("identical push = %+v %v %v", same, created, err)
	}
	// A later version is not live unless asked.
	description := "Support replies"
	push := promptPush(acct.ID, "support", "Be brief.", api.OutboundAIPromptTarget{RouteID: openaiRoute.ID})
	push.Description = &description
	prompt, v2, created, err := store.PushOutboundAIPromptVersion(ctx, push)
	if err != nil || !created || v2.Version != 2 || *prompt.LiveVersion != 1 || prompt.LatestVersion != 2 || prompt.Description != description {
		t.Fatalf("second push = %+v %+v %v %v", prompt, v2, created, err)
	}
	got, err := store.GetOutboundAIPromptVersion(ctx, acct.ID, "support", 2)
	if err != nil || got.Target.RouteID != openaiRoute.ID || len(got.Messages) != 2 || got.Messages[0].Content != "Be brief." {
		t.Fatalf("version 2 = %+v %v", got, err)
	}
	versions, err := store.ListOutboundAIPromptVersions(ctx, acct.ID, "support")
	if err != nil || len(versions) != 2 || versions[0].Version != 2 {
		t.Fatalf("versions = %+v %v", versions, err)
	}
	// A canary needs an existing version that is not live.
	canary := state.OutboundAIPromptCanary{Version: 1, Percent: 10, MaxErrorRateIncrease: 5, MinCalls: 100, WindowMinutes: 60}
	if _, err := store.SetOutboundAIPromptCanary(ctx, acct.ID, "support", canary); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("canary on live version: %v", err)
	}
	canary.Version = 9
	if _, err := store.SetOutboundAIPromptCanary(ctx, acct.ID, "support", canary); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("canary on missing version: %v", err)
	}
	canary.Version = 2
	if stored, err := store.SetOutboundAIPromptCanary(ctx, acct.ID, "support", canary); err != nil || stored.State != api.OutboundAIRouteCanaryActive || stored.Version != 2 {
		t.Fatalf("canary = %+v %v", stored, err)
	}
	// Promoting makes the canary's version live and keeps the old one.
	prompt, err = store.PromoteOutboundAIPromptCanary(ctx, acct.ID, "support")
	if err != nil || *prompt.LiveVersion != 2 || prompt.PreviousLiveVersion == nil || *prompt.PreviousLiveVersion != 1 || prompt.Canary != nil {
		t.Fatalf("promoted = %+v %v", prompt, err)
	}
	if _, err := store.PromoteOutboundAIPromptCanary(ctx, acct.ID, "support"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("second promote: %v", err)
	}
	// Rolling back swaps live and previous.
	prompt, err = store.RollBackOutboundAIPromptLive(ctx, acct.ID, "support")
	if err != nil || *prompt.LiveVersion != 1 || *prompt.PreviousLiveVersion != 2 {
		t.Fatalf("rolled back = %+v %v", prompt, err)
	}
	if _, err := store.SetOutboundAIPromptLive(ctx, acct.ID, "support", 7); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("live missing version: %v", err)
	}
	// Evaluations compare a version with the live one.
	evaluation, err := store.CreateOutboundAIPromptEvaluation(ctx, state.OutboundAIPromptEvaluation{ID: uuid.NewString(), AccountID: acct.ID,
		PromptID: prompt.ID, CandidateVersion: 2, Cases: []map[string]string{{"question": "hi"}}, JudgeIntegrationID: openai,
		JudgeModel: "gpt-4o", MaxLossRate: 20})
	if err != nil || evaluation.BaselineVersion != 1 || evaluation.State != api.OutboundAIEvaluationPending {
		t.Fatalf("evaluation = %+v %v", evaluation, err)
	}
	if _, err := store.CreateOutboundAIPromptEvaluation(ctx, state.OutboundAIPromptEvaluation{ID: uuid.NewString(), AccountID: acct.ID,
		PromptID: prompt.ID, CandidateVersion: 1, Cases: []map[string]string{{}}, JudgeIntegrationID: openai, JudgeModel: "gpt-4o"}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("evaluating the live version: %v", err)
	}
	read, err := store.GetOutboundAIPromptEvaluation(ctx, acct.ID, evaluation.ID)
	if err != nil || read.PromptName != "support" || len(read.Cases) != 1 || read.Cases[0]["question"] != "hi" {
		t.Fatalf("read evaluation = %+v %v", read, err)
	}
	listed, err := store.ListOutboundAIPromptEvaluations(ctx, acct.ID, "support")
	if err != nil || len(listed) != 1 {
		t.Fatalf("evaluations = %+v %v", listed, err)
	}
	stats, err := store.OutboundAIPromptStats(ctx, acct.ID, "support")
	if err != nil || stats.Prompt.ID != prompt.ID || len(stats.Versions) != 0 {
		t.Fatalf("stats = %+v %v", stats, err)
	}
	// The plan limit counts prompts, not versions.
	if _, _, _, err := store.PushOutboundAIPromptVersion(ctx, promptPush(acct.ID, "second", "Hi.", api.OutboundAIPromptTarget{IntegrationID: openai, Model: "gpt-4o"})); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := store.PushOutboundAIPromptVersion(ctx, promptPush(acct.ID, "third", "Hi.", api.OutboundAIPromptTarget{IntegrationID: openai, Model: "gpt-4o"})); !errors.Is(err, state.ErrOutboundAIPromptLimit) {
		t.Fatalf("third prompt: %v", err)
	}
	if err := store.DeleteOutboundAIPrompt(ctx, acct.ID, "support"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetOutboundAIPrompt(ctx, acct.ID, "support"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted prompt: %v", err)
	}
	if _, err := store.GetOutboundAIPromptEvaluation(ctx, acct.ID, evaluation.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted prompt's evaluation: %v", err)
	}
}

func TestMemOutboundAIPrompts(t *testing.T) {
	checkOutboundAIPrompts(t, state.NewMemStore())
}

func TestPgOutboundAIPrompts(t *testing.T) {
	s, _, _ := pgWithPool(t)
	checkOutboundAIPrompts(t, s)
}
