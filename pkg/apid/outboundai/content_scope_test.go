package outboundai

// adr: 20261027132936 — amendment 1: replays and evaluations carry captured
// answers only for readers with the content route's scopes, and evaluations
// withhold the samples of apps the reader may no longer see.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestReplayWithholdsAnswersWithoutContentScope(t *testing.T) {
	service, store, acct, integrationID := newTestService(t)
	if w := serve(service.PutPolicy, acct, http.MethodPut, "/v1/outbound/integrations/x/ai", integrationID,
		`{"ai_provider":"openai","request_log_enabled":true,"request_log_content":true}`); w.Code != http.StatusNoContent {
		t.Fatalf("policy = %d %s", w.Code, w.Body.String())
	}
	app, err := store.CreateApp(context.Background(), state.App{AccountID: acct.ID, Slug: "ai-replayed", Type: state.AppTypeApp, Runtime: "node22"})
	if err != nil {
		t.Fatal(err)
	}
	at, logID := time.Now().UTC().Truncate(time.Second), uuid.NewString()
	store.SeedOutboundAIRequestLog(integrationID, state.OutboundAIRequestLogEntry{ID: logID, CreatedAt: at, Caller: app.ID,
		Model: "gpt-5", Status: 200, Outcome: "known", Attempts: 1})
	store.SeedOutboundAIRequestContent(integrationID, state.OutboundAIRequestContent{LogID: logID, CreatedAt: at,
		RequestPath: "/v1/chat/completions", RequestBody: []byte(`{"model":"gpt-5","messages":[]}`)})
	body, err := json.Marshal(api.CreateOutboundAIReplayRequest{Targets: []api.OutboundAIReplayTarget{{IntegrationID: integrationID}}})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/outbound/integrations/x/ai-requests/x/replays", strings.NewReader(string(body)))
	r.SetPathValue("integration", integrationID)
	r.SetPathValue("request", logID)
	w := httptest.NewRecorder()
	service.CreateReplay(w, r, acct)
	var created api.OutboundAIReplay
	if w.Code != http.StatusAccepted || json.Unmarshal(w.Body.Bytes(), &created) != nil {
		t.Fatalf("create replay = %d %s", w.Code, w.Body.String())
	}
	store.SeedOutboundAIReplayResults(created.ID, "the captured answer", state.OutboundAIReplayResult{IntegrationID: integrationID,
		Model: "gpt-5", Outcome: "known", OutputText: "the replayed answer", ResponseBody: []byte(`{"choices":["the replayed body"]}`),
		ResponseContentType: "application/json"})
	read := func(content bool) (api.OutboundAIReplay, string) {
		t.Helper()
		service.ContentReadable = func(*http.Request) bool { return content }
		r := httptest.NewRequest(http.MethodGet, "/v1/outbound/ai-replays/"+created.ID, nil)
		r.SetPathValue("replay", created.ID)
		w := httptest.NewRecorder()
		service.GetReplay(w, r, acct)
		var replay api.OutboundAIReplay
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &replay) != nil || len(replay.Results) != 1 {
			t.Fatalf("get replay = %d %s", w.Code, w.Body.String())
		}
		return replay, w.Body.String()
	}
	withheld, raw := read(false)
	if !withheld.ContentWithheld || withheld.Source.OutputText != nil || withheld.Results[0].OutputText != "" || withheld.Results[0].Response.Body != "" {
		t.Fatalf("a reader without the content scope got answers: %s", raw)
	}
	for _, answer := range []string{"the captured answer", "the replayed answer", "the replayed body"} {
		if strings.Contains(raw, answer) {
			t.Fatalf("a reader without the content scope read %q", answer)
		}
	}
	if withheld.Results[0].Model == nil || *withheld.Results[0].Model != "gpt-5" {
		t.Fatalf("a withheld replay lost its numbers: %s", raw)
	}
	full, raw := read(true)
	if full.ContentWithheld || full.Source.OutputText == nil || *full.Source.OutputText != "the captured answer" ||
		full.Results[0].OutputText != "the replayed answer" || !strings.Contains(full.Results[0].Response.Body, "the replayed body") {
		t.Fatalf("a reader with the content scope = %s", raw)
	}
}

func TestEvaluationWithholdsAnswersAndHiddenApps(t *testing.T) {
	service, store, acct, integrationID := newTestService(t)
	ctx := context.Background()
	owner, err := store.CreateAccount(ctx, "owner-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "eval-co", Name: "Eval Co", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	for member, role := range map[string]state.OrgRole{owner.ID: state.OrgRoleOwner, acct.ID: state.OrgRoleDeveloper} {
		if err := store.AddOrgMember(ctx, org.ID, member, role, nil); err != nil {
			t.Fatal(err)
		}
	}
	company, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, OrgID: org.ID, Slug: "eval-co-api", Type: state.AppTypeApp, Runtime: "node22"})
	if err != nil {
		t.Fatal(err)
	}
	personal, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "eval-mine", Type: state.AppTypeApp, Runtime: "node22"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Second)
	companyCall, personalCall, expiredCall := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for call, app := range map[string]string{companyCall: company.ID, personalCall: personal.ID} {
		store.SeedOutboundAIRequestLog(integrationID, state.OutboundAIRequestLogEntry{ID: call, CreatedAt: at, Caller: app,
			Model: "gpt-5", Status: 200, Outcome: "known", Attempts: 1})
	}
	evaluationID := uuid.NewString()
	items := make([]state.OutboundAIEvaluationItem, 0, 3)
	for _, call := range []string{companyCall, personalCall, expiredCall} {
		items = append(items, state.OutboundAIEvaluationItem{LogID: call, Verdict: api.OutboundAIEvaluationItemWin,
			BaselineOutputText: "baseline " + call, CandidateOutputText: "candidate " + call})
	}
	store.SeedOutboundAIEvaluation(state.OutboundAIEvaluation{ID: evaluationID, AccountID: acct.ID, RouteID: uuid.NewString(), Position: 2,
		JudgeIntegrationID: integrationID, JudgeModel: "gpt-5", SampleSize: 3, MaxLossRate: 20, State: api.OutboundAIEvaluationDone,
		Verdict: api.OutboundAIEvaluationPass, Samples: 3, Wins: 3, CreatedAt: at, Items: items})
	read := func(content bool) (api.OutboundAIEvaluation, string) {
		t.Helper()
		service.ContentReadable = func(*http.Request) bool { return content }
		w := serveEvaluation(service.GetEvaluation, acct, http.MethodGet, "", evaluationID, "")
		var evaluation api.OutboundAIEvaluation
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &evaluation) != nil {
			t.Fatalf("get evaluation = %d %s", w.Code, w.Body.String())
		}
		return evaluation, w.Body.String()
	}
	samples := func(evaluation api.OutboundAIEvaluation) map[string]bool {
		out := map[string]bool{}
		for _, item := range evaluation.Items {
			out[item.LogID] = true
		}
		return out
	}
	withheld, raw := read(false)
	if !withheld.ContentWithheld || len(withheld.Items) != 3 || strings.Contains(raw, "baseline ") || strings.Contains(raw, "candidate ") {
		t.Fatalf("a reader without the content scope = %s", raw)
	}
	full, raw := read(true)
	if full.ContentWithheld || len(full.Items) != 3 || !strings.Contains(raw, "baseline "+companyCall) {
		t.Fatalf("a reader with the content scope = %s", raw)
	}
	if err := store.RemoveOrgMember(ctx, org.ID, acct.ID); err != nil {
		t.Fatal(err)
	}
	removed, raw := read(true)
	if got := samples(removed); len(got) != 2 || got[companyCall] || !got[personalCall] || !got[expiredCall] {
		t.Fatalf("a removed creator's samples = %v", got)
	}
	if strings.Contains(raw, companyCall) {
		t.Fatalf("a removed creator read the company app's sample: %s", raw)
	}
}
