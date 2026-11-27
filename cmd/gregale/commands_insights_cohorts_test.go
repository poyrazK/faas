package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// ADR 20261119102741 §5: `gregale insight cohort ...` and the funnel,
// retention and CUPED options of `gregale flag experiment create`.
func TestInsightCohortAndExperimentMetricCommands(t *testing.T) {
	var cohortReq api.InsightCohortRequest
	var experimentReqs []api.FlagExperimentCreateRequest
	refreshed := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		base := "/v1/projects/shop/environments/production"
		cohort := api.InsightCohort{InsightCohortRequest: api.InsightCohortRequest{Key: "power-buyers", Kind: "did", Event: "order_completed", WithinDays: 30, MinCount: 3},
			Members: 1000, Truncated: true, MaxMembers: 1000, RefreshedAt: &refreshed}
		switch r.Method + " " + r.URL.Path {
		case "POST " + base + "/cohorts":
			_ = json.NewDecoder(r.Body).Decode(&cohortReq)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.InsightCohort{InsightCohortRequest: cohortReq, MaxMembers: 1000})
		case "GET " + base + "/cohorts":
			_ = json.NewEncoder(w).Encode(api.InsightCohortList{Cohorts: []api.InsightCohort{cohort}})
		case "DELETE " + base + "/cohorts/power-buyers":
			w.WriteHeader(http.StatusNoContent)
		case "POST " + base + "/experiments":
			var req api.FlagExperimentCreateRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			experimentReqs = append(experimentReqs, req)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.FlagExperiment{Key: req.Key, Flag: req.Flag, RuleID: req.RuleID, Control: req.Control,
				MetricKind: req.MetricKind, Funnel: req.Funnel, Retention: req.Retention, Status: "draft", Cuped: req.Cuped == nil})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	run := func(args ...string) (int, string, string) {
		main.ResetJSONOut(t)
		stdout, stderr, restore := main.SwapIO(t)
		code := main.Run(args)
		restore()
		return code, stdout.String(), stderr()
	}

	code, out, errOut := run("insight", "cohort", "create", "power-buyers", "--project", "shop", "--kind", "did", "--event", "order_completed",
		"--min-count", "3", "--days", "30", "--where", "plan:pro")
	if code != 0 || cohortReq.Kind != "did" || cohortReq.MinCount != 3 || cohortReq.WithinDays != 30 || cohortReq.Where["plan"] != "pro" ||
		!strings.Contains(out, "--attr cohort=power-buyers") {
		t.Fatalf("cohort create = %d %q %q; request %+v", code, out, errOut, cohortReq)
	}
	if code, out, _ = run("insight", "cohort", "list", "--project", "shop"); code != 0 || !strings.Contains(out, "1000 (max)") || !strings.Contains(out, "at least 3 times in 30 days") {
		t.Fatalf("cohort list = %d %q", code, out)
	}
	if code, out, _ = run("insights", "cohorts", "delete", "power-buyers", "--project", "shop"); code != 0 || !strings.Contains(out, "Deleted cohort power-buyers") {
		t.Fatalf("legacy cohort delete = %d %q", code, out)
	}
	if code, _, errOut = run("insight", "cohort", "create", "x", "--project", "shop", "--where", "nocolon"); code == 0 || !strings.Contains(errOut, "KEY:VALUE") {
		t.Fatalf("bad --where = %d %q", code, errOut)
	}

	code, out, errOut = run("flag", "experiment", "create", "onboarding", "--project", "shop", "--flag", "checkout", "--rule", "weighted", "--control", "legacy",
		"--metric-kind", "funnel", "--step", "signup", "--step", "order_completed", "--window", "72h")
	if code != 0 || len(experimentReqs) != 1 || experimentReqs[0].Funnel == nil || len(experimentReqs[0].Funnel.Steps) != 2 || experimentReqs[0].Funnel.Window != "72h" ||
		experimentReqs[0].MetricEvent != "" || !strings.Contains(out, "funnel signup > order_completed within 72h") {
		t.Fatalf("funnel create = %d %q %q; requests %+v", code, out, errOut, experimentReqs)
	}
	code, out, errOut = run("flag", "experiment", "create", "week-one", "--project", "shop", "--flag", "checkout", "--rule", "weighted", "--control", "legacy",
		"--metric-kind", "retention", "--return-event", "order_completed", "--period", "week", "--period-number", "1", "--no-cuped")
	if code != 0 || len(experimentReqs) != 2 || experimentReqs[1].Retention == nil || experimentReqs[1].Retention.PeriodNumber != 1 ||
		experimentReqs[1].Cuped == nil || *experimentReqs[1].Cuped || !strings.Contains(out, "retention order_completed in week 1") {
		t.Fatalf("retention create = %d %q %q; requests %+v", code, out, errOut, experimentReqs)
	}
}
