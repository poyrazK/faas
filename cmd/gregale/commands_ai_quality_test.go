package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// ADR 20261021171712: `gregale ai policy` keeps the online evaluation and
// quality alerts it is not told to change; `gregale ai feedback` rates a
// logged call; `gregale ai quality-alerts` lists drift alerts.
func TestAIQualityCommands(t *testing.T) {
	main.ResetJSONOut(t)
	const integrationID = "11111111-2222-3333-4444-555555555555"
	const judgeID = "99999999-2222-3333-4444-555555555555"
	const callID = "aaaaaaaa-2222-3333-4444-555555555555"
	current := api.OutboundIntegrationOffer{ID: integrationID, AIProvider: api.OutboundAIProviderOpenAI, AIRequestLogEnabled: true,
		AIRequestLogContent: true, AIQualityAlerts: true,
		AIOnlineEvaluation: &api.OutboundAIOnlineEvaluation{SamplePercent: 5, JudgeIntegrationID: judgeID, JudgeModel: "gpt-5-mini"}}
	var policies []api.PutOutboundAIPolicyRequest
	var feedback api.PutOutboundAIFeedbackRequest
	base := "/v1/outbound/integrations/" + integrationID
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/outbound/integrations":
			main.WriteJSONTest(w, api.OutboundIntegrationOfferList{Items: []api.OutboundIntegrationOffer{current}})
		case "PUT " + base + "/ai":
			var policy api.PutOutboundAIPolicyRequest
			if err := json.NewDecoder(r.Body).Decode(&policy); err != nil {
				t.Error(err)
			}
			policies = append(policies, policy)
			w.WriteHeader(http.StatusNoContent)
		case "PUT " + base + "/ai-requests/" + callID + "/feedback":
			if err := json.NewDecoder(r.Body).Decode(&feedback); err != nil {
				t.Error(err)
			}
			w.WriteHeader(http.StatusNoContent)
		case "GET " + base + "/ai-quality-alerts":
			main.WriteJSONTest(w, api.OutboundAIQualityAlertList{Items: []api.OutboundAIQualityAlert{
				{UsageDate: "2026-10-21", Model: "gpt-5", Metric: "truncated", CurrentValue: 1_250, BaselineValue: 200,
					CurrentCount: 400, BaselineCount: 9_000, DetectedAt: time.Date(2026, 10, 21, 9, 30, 0, 0, time.UTC)},
				{UsageDate: "2026-10-21", Model: "gpt-5", Metric: "evaluation_score", CurrentValue: 310, BaselineValue: 420,
					CurrentCount: 30, BaselineCount: 200, DetectedAt: time.Date(2026, 10, 21, 9, 40, 0, 0, time.UTC)},
			}})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	var stdout bytes.Buffer
	oldOut := (*main.OsStdout)
	(*main.OsStdout) = &stdout
	defer func() { (*main.OsStdout) = oldOut }()

	if code := main.CmdAI([]string{"policy", integrationID, "--daily-tokens", "1000"}); code != 0 {
		t.Fatalf("ai policy exit = %d", code)
	}
	if kept := policies[0]; kept.OnlineEvaluation == nil || kept.OnlineEvaluation.SamplePercent != 5 || !kept.QualityAlerts {
		t.Fatalf("an unrelated change dropped the quality settings: %+v", kept)
	}
	if code := main.CmdAI([]string{"policy", integrationID, "--online-eval-percent", "20", "--online-eval-rubric", "Cite a source.",
		"--quality-alerts", "off"}); code != 0 {
		t.Fatalf("ai policy exit = %d", code)
	}
	if changed := policies[1]; changed.OnlineEvaluation.SamplePercent != 20 || changed.OnlineEvaluation.JudgeModel != "gpt-5-mini" ||
		changed.OnlineEvaluation.Rubric != "Cite a source." || changed.QualityAlerts {
		t.Fatalf("policy = %+v %+v", changed, changed.OnlineEvaluation)
	}
	if code := main.CmdAI([]string{"policy", integrationID, "--online-eval-percent", "none"}); code != 0 || policies[2].OnlineEvaluation != nil {
		t.Fatalf("turning evaluations off = %d %+v", code, policies[2].OnlineEvaluation)
	}

	if code := main.CmdAI([]string{"feedback", integrationID, callID, "--bad", "--label", "wrong_answer"}); code != 0 ||
		feedback.Rating != "bad" || feedback.Label != "wrong_answer" {
		t.Fatalf("ai feedback = %d %+v", code, feedback)
	}
	stdout.Reset()
	if code := main.CmdAI([]string{"quality-alerts", integrationID}); code != 0 {
		t.Fatalf("ai quality-alerts exit = %d", code)
	}
	if out := stdout.String(); !strings.Contains(out, "12.50%") || !strings.Contains(out, "2.00%") || !strings.Contains(out, "3.10") ||
		!strings.Contains(out, "4.20") {
		t.Fatalf("quality alerts:\n%s", out)
	}
	for _, args := range [][]string{
		{"policy", integrationID, "--online-eval-percent", "0"},
		{"policy", integrationID, "--online-eval-percent", "none", "--online-eval-model", "x"},
		{"policy", integrationID, "--quality-alerts", "maybe"},
		{"feedback", integrationID, callID},
		{"feedback", integrationID, callID, "--good", "--bad"},
		{"feedback", integrationID, callID, "--good", "--label", "Bad Label"},
		{"quality-alerts", integrationID, "--limit", "101"},
	} {
		if code := main.CmdAI(args); code == 0 {
			t.Errorf("%v exited 0", args)
		}
	}
	if len(policies) != 3 {
		t.Fatalf("invalid flags reached the API: %d policies", len(policies))
	}
}
