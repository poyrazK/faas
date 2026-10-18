package outboundai

// adr: 1431 — apid issues access tokens for an app, showing the secret
// once, and records evaluations of a route target within the daily limit.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// appHost loads one app of the account.
type appHost struct {
	testHost
	app state.App
}

func (h appHost) LoadApp(w http.ResponseWriter, _ *http.Request, acct state.Account, slug string) (state.App, bool) {
	if slug != h.app.Slug || acct.ID != h.app.AccountID {
		http.NotFound(w, nil)
		return state.App{}, false
	}
	return h.app, true
}

func serveApp(handler func(http.ResponseWriter, *http.Request, state.Account), acct state.Account, method, slug, tokenID, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/v1/apps/"+slug+"/ai-access-tokens", strings.NewReader(body))
	r.SetPathValue("slug", slug)
	r.SetPathValue("token", tokenID)
	w := httptest.NewRecorder()
	handler(w, r, acct)
	return w
}

var accessTokenSecret = regexp.MustCompile(`^ggai_[A-Za-z0-9_-]{43}$`)

func TestAccessTokenEndpoints(t *testing.T) {
	_, store, acct, _ := newTestService(t)
	ctx := context.Background()
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "api-" + uuid.NewString()[:6], RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	bound, unbound := aiIntegration(t, store, acct.ID, api.OutboundAIProviderOpenAI), aiIntegration(t, store, acct.ID, api.OutboundAIProviderOpenAI)
	if _, err := store.BindOutboundIntegration(ctx, acct.ID, app.ID, bound); err != nil {
		t.Fatal(err)
	}
	service := &Service{Host: appHost{testHost: testHost{store}, app: app}}
	for _, body := range []string{`{"name":"Bad_Name"}`, `{"name":"x","expires_in_hours":0}`, `{"name":"x","expires_in_hours":2161}`,
		`{"name":"x","integration_ids":["nope"]}`, `{"name":"x","integration_ids":["` + bound + `","` + bound + `"]}`,
		`{"name":"x","integration_ids":["` + unbound + `"]}`} {
		if w := serveApp(service.CreateAccessToken, acct, http.MethodPost, app.Slug, "", body); w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s", body, w.Code, w.Body.String())
		}
	}
	if w := serveApp(service.CreateAccessToken, acct, http.MethodPost, "missing", "", `{"name":"x"}`); w.Code != http.StatusNotFound {
		t.Fatalf("an unknown app = %d", w.Code)
	}
	w := serveApp(service.CreateAccessToken, acct, http.MethodPost, app.Slug, "", `{"name":"laptop","expires_in_hours":24,"integration_ids":["`+bound+`"]}`)
	var created api.CreatedOutboundAIAccessToken
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &created) != nil || !accessTokenSecret.MatchString(created.Token) ||
		created.AccessToken.Prefix != created.Token[:12] || created.AccessToken.AppID != app.ID ||
		len(created.AccessToken.IntegrationIDs) != 1 || w.Header().Get("Cache-Control") != "no-store" ||
		created.AccessToken.ExpiresAt.Sub(time.Now()) < 23*time.Hour || created.AccessToken.ExpiresAt.Sub(time.Now()) > 24*time.Hour {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	if w := serveApp(service.CreateAccessToken, acct, http.MethodPost, app.Slug, "", `{"name":"laptop"}`); w.Code != http.StatusConflict ||
		!strings.Contains(w.Body.String(), "outbound_ai_access_token_exists") {
		t.Fatalf("a duplicate name = %d %s", w.Code, w.Body.String())
	}
	w = serveApp(service.ListAccessTokens, acct, http.MethodGet, app.Slug, "", "")
	var list api.OutboundAIAccessTokenList
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list.Items) != 1 || strings.Contains(w.Body.String(), created.Token) {
		t.Fatalf("list = %d %s", w.Code, w.Body.String())
	}
	for id, want := range map[string]int{"nope": http.StatusBadRequest, uuid.NewString(): http.StatusNotFound, created.AccessToken.ID: http.StatusNoContent} {
		if w := serveApp(service.RevokeAccessToken, acct, http.MethodDelete, app.Slug, id, ""); w.Code != want {
			t.Errorf("revoke %s = %d, want %d", id, w.Code, want)
		}
	}
	w = serveApp(service.ListAccessTokens, acct, http.MethodGet, app.Slug, "", "")
	if json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list.Items) != 1 || list.Items[0].RevokedAt == nil {
		t.Fatalf("list after revoke = %s", w.Body.String())
	}
	for index := range api.OutboundAIAccessTokensPerApp {
		body := `{"name":"t` + string(rune('a'+index)) + `"}`
		if w := serveApp(service.CreateAccessToken, acct, http.MethodPost, app.Slug, "", body); w.Code != http.StatusCreated {
			t.Fatalf("token %d = %d %s", index, w.Code, w.Body.String())
		}
	}
	w = serveApp(service.CreateAccessToken, acct, http.MethodPost, app.Slug, "", `{"name":"one-more"}`)
	var problem api.Problem
	if w.Code != http.StatusConflict || json.Unmarshal(w.Body.Bytes(), &problem) != nil || problem.Code != "outbound_ai_access_token_limit" ||
		problem.Limit == nil || *problem.Limit != api.OutboundAIAccessTokensPerApp {
		t.Fatalf("a token past the limit = %d %s", w.Code, w.Body.String())
	}
}

func serveEvaluation(handler func(http.ResponseWriter, *http.Request, state.Account), acct state.Account, method, routeID, evaluationID, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/v1/outbound/ai-routes/"+routeID+"/evaluations", strings.NewReader(body))
	r.SetPathValue("route", routeID)
	r.SetPathValue("evaluation", evaluationID)
	w := httptest.NewRecorder()
	handler(w, r, acct)
	return w
}

func TestEvaluationEndpoints(t *testing.T) {
	service, store, acct, _ := newTestService(t)
	ctx := context.Background()
	first, second := aiIntegration(t, store, acct.ID, api.OutboundAIProviderOpenAI), aiIntegration(t, store, acct.ID, api.OutboundAIProviderAnthropic)
	route, err := store.CreateOutboundAIRoute(ctx, state.OutboundAIRoute{ID: uuid.NewString(), AccountID: acct.ID, Name: "chat",
		Format: api.OutboundAIRouteFormatOpenAI, Targets: []state.OutboundAIRouteTarget{{IntegrationID: first}, {IntegrationID: second, Model: "claude-new"}}})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateAccount(ctx, "other-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreign := aiIntegration(t, store, other.ID, api.OutboundAIProviderOpenAI)
	judge := `"judge_integration_id":"` + first + `","judge_model":"gpt-5"`
	for _, body := range []string{`{"target":1,` + judge + `}`, `{"target":6,` + judge + `}`, `{"target":2,"judge_model":"gpt-5"}`,
		`{"target":2,"judge_integration_id":"nope","judge_model":"gpt-5"}`, `{"target":2,` + judge + `,"sample_size":0}`,
		`{"target":2,` + judge + `,"sample_size":51}`, `{"target":2,` + judge + `,"max_loss_rate":101}`,
		`{"target":2,` + judge + `,"canary":{"target":3,"percent":10}}`, `{"target":2,` + judge + `,"canary":{"target":2,"percent":60}}`,
		`{"target":3,` + judge + `}`, `{"target":2,"judge_integration_id":"` + foreign + `","judge_model":"gpt-5"}`} {
		if w := serveEvaluation(service.CreateEvaluation, acct, http.MethodPost, route.ID, "", body); w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s", body, w.Code, w.Body.String())
		}
	}
	if w := serveEvaluation(service.CreateEvaluation, other, http.MethodPost, route.ID, "", `{"target":2,"judge_integration_id":"`+foreign+`","judge_model":"m"}`); w.Code != http.StatusNotFound {
		t.Fatalf("another account's route = %d", w.Code)
	}
	w := serveEvaluation(service.CreateEvaluation, acct, http.MethodPost, route.ID, "", `{"target":2,`+judge+`,"canary":{"target":2,"percent":10}}`)
	var created api.OutboundAIEvaluation
	if w.Code != http.StatusAccepted || json.Unmarshal(w.Body.Bytes(), &created) != nil || created.State != api.OutboundAIEvaluationPending ||
		created.SampleSize != api.OutboundAIEvaluationSampleDefault || created.MaxLossRate != api.OutboundAIEvaluationMaxLossRateDefault ||
		created.Canary == nil || *created.Canary.MinCalls != api.OutboundAIRouteCanaryMinCallsDefault || created.Verdict != nil {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	ttfb, status := int64(80), 200
	store.SeedOutboundAIEvaluation(state.OutboundAIEvaluation{ID: created.ID, AccountID: acct.ID, RouteID: route.ID, Position: 2,
		JudgeIntegrationID: first, JudgeModel: "gpt-5", SampleSize: 20, MaxLossRate: 20, State: api.OutboundAIEvaluationDone,
		Verdict: api.OutboundAIEvaluationPass, Samples: 1, Wins: 1, BaselineCostMillicents: 9, BaselineTTFBMSTotal: 160, BaselineTTFBCalls: 2,
		CanaryStarted: true, CreatedAt: created.CreatedAt, Items: []state.OutboundAIEvaluationItem{{LogID: uuid.NewString(),
			Verdict: api.OutboundAIEvaluationItemWin, CandidateStatus: &status, CandidateTTFBMS: &ttfb, CandidateOutputText: "better"}}})
	w = serveEvaluation(service.GetEvaluation, acct, http.MethodGet, "", created.ID, "")
	var read api.OutboundAIEvaluation
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &read) != nil || read.Verdict == nil || *read.Verdict != "pass" ||
		read.Baseline.MeanTTFBMS == nil || *read.Baseline.MeanTTFBMS != 80 || read.Candidate.MeanTTFBMS != nil || len(read.Items) != 1 ||
		*read.Items[0].CandidateStatus != 200 || !read.CanaryStarted {
		t.Fatalf("get = %d %s", w.Code, w.Body.String())
	}
	for id, want := range map[string]int{"nope": http.StatusBadRequest, uuid.NewString(): http.StatusNotFound} {
		if w := serveEvaluation(service.GetEvaluation, acct, http.MethodGet, "", id, ""); w.Code != want {
			t.Errorf("get %s = %d, want %d", id, w.Code, want)
		}
	}
	if w := serveEvaluation(service.GetEvaluation, other, http.MethodGet, "", created.ID, ""); w.Code != http.StatusNotFound {
		t.Fatalf("another account read the evaluation: %d", w.Code)
	}
	for range api.OutboundAIEvaluationsPerDay - 1 {
		if w := serveEvaluation(service.CreateEvaluation, acct, http.MethodPost, route.ID, "", `{"target":2,`+judge+`}`); w.Code != http.StatusAccepted {
			t.Fatalf("create = %d %s", w.Code, w.Body.String())
		}
	}
	w = serveEvaluation(service.CreateEvaluation, acct, http.MethodPost, route.ID, "", `{"target":2,`+judge+`}`)
	if w.Code != http.StatusTooManyRequests || !strings.Contains(w.Body.String(), "outbound_ai_evaluation_limit") {
		t.Fatalf("an evaluation past the daily limit = %d %s", w.Code, w.Body.String())
	}
	w = serveEvaluation(service.ListEvaluations, acct, http.MethodGet, route.ID, "", "")
	var list api.OutboundAIEvaluationList
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list.Items) != api.OutboundAIEvaluationsPerDay ||
		strings.Contains(w.Body.String(), `"items":[{"log_id"`) {
		t.Fatalf("list = %d %d", w.Code, len(list.Items))
	}
}
