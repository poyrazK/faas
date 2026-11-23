package apidserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type anomalyPreviewTransport func(*http.Request) (*http.Response, error)

func (f anomalyPreviewTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type anomalyPreviewPromotionStore struct {
	MemStore *state.MemStore
	list     func(context.Context, string, string, string, time.Time, int) ([]state.DeploymentPromotion, error)
}

func TestAlertObserveOnlyEnvironmentRoute(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := t.Context()
	project, err := e.store.CreateProject(ctx, state.Project{AccountID: e.acct.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "orders", Type: state.AppTypeApp}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	e.s.anomalyAlertsEnabled = true
	const collection = "/v1/apps/orders/alerts"
	req := api.CreateAlertRuleRequest{
		Name: "Observe staging", Metric: "error_rate_pct", Comparison: "above_baseline",
		Threshold: 3, WindowSpec: "15m", ObserveOnly: true, EnvironmentScope: "foreign",
		RecoveryConfirmationChecks: new(2),
	}
	if rec := e.do(t, http.MethodPost, collection, req, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("create foreign scope returned %d: %s", rec.Code, rec.Body.String())
	}
	req.EnvironmentScope, req.ObserveOnly = "staging", false
	if rec := e.do(t, http.MethodPost, collection, req, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("create scoped Notify returned %d: %s", rec.Code, rec.Body.String())
	}
	req.ObserveOnly = true
	rec := e.do(t, http.MethodPost, collection, req, nil)
	var rule api.AlertRuleResponse
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &rule) != nil || !rule.ObserveOnly ||
		rule.EnvironmentScope != "staging" || rule.RecoveryConfirmationChecks != 2 {
		t.Fatalf("create scoped observation: %d %s", rec.Code, rec.Body.String())
	}
	path := collection + "/" + rule.ID
	snapshot, err := e.store.AlertRuleByID(ctx, rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	at := snapshot.UpdatedAt.Add(-time.Second)
	evidence := api.AlertAnomalyEvaluation{
		Reason: api.AlertAnomalyObservedBreach, BaselineDays: new(6),
		Observed: new(5.0), Baseline: new(1.0), TriggerValue: new(3.0),
	}
	if accepted, err := e.store.RecordAlertObservation(ctx, snapshot, at, evidence); err != nil || !accepted {
		t.Fatalf("record observation: accepted=%v err=%v", accepted, err)
	}
	rec = e.do(t, http.MethodGet, path, nil, nil)
	rule = api.AlertRuleResponse{}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &rule) != nil || rule.EnvironmentScope != "staging" ||
		rule.AnomalyEvaluation == nil || rule.AnomalyEvaluation.BaselineDays == nil || *rule.AnomalyEvaluation.BaselineDays != 6 ||
		len(rule.ObservedIncidents) != 1 || rule.ObservedIncidents[0].EnvironmentScope != "staging" {
		t.Fatalf("get environment history: %d %s", rec.Code, rec.Body.String())
	}
	opening := rule.ObservedIncidents[0].OpeningEvidence
	if opening == nil || opening.Metric != "error_rate_pct" || opening.Window != "15m" ||
		opening.Observed != 5 || opening.Baseline != 1 || opening.TriggerValue != 3 {
		t.Fatalf("get observed incident opening evidence: %d %s", rec.Code, rec.Body.String())
	}
	coverage := rule.ObservationCoverage
	if coverage == nil || coverage.Attempted != 1 || coverage.Completed != 1 || !coverage.LastCheckedAt.Equal(at.Truncate(time.Microsecond)) {
		t.Fatalf("get observed rule coverage: %d %s", rec.Code, rec.Body.String())
	}
	for _, patch := range []api.UpdateAlertRuleRequest{
		{ObserveOnly: new(false)}, {EnvironmentScope: new("foreign")}, {RecoveryConfirmationChecks: new(3)}, {RecoveryConfirmationChecks: new(0)},
	} {
		if rec := e.do(t, http.MethodPatch, path, patch, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid scope edit returned %d: %s", rec.Code, rec.Body.String())
		}
	}
	for _, scope := range []string{"production", ""} {
		rec := e.do(t, http.MethodPatch, path, api.UpdateAlertRuleRequest{EnvironmentScope: &scope}, nil)
		edited := api.AlertRuleResponse{}
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &edited) != nil || edited.EnvironmentScope != scope {
			t.Fatalf("edit scope %q: %d %s", scope, rec.Code, rec.Body.String())
		}
	}
	channel, err := e.store.CreateNotificationChannel(ctx, state.NotificationChannel{
		AccountID: e.acct.ID, Name: "Operations", Kind: "slack",
	}, 10)
	if err != nil {
		t.Fatal(err)
	}
	rec = e.do(t, http.MethodPatch, path, api.UpdateAlertRuleRequest{EnvironmentScope: new("staging")}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("save staging scope: %d %s", rec.Code, rec.Body.String())
	}
	patch := api.UpdateAlertRuleRequest{ObserveOnly: new(false), ChannelIDs: &[]string{channel.ID}}
	patch.Action = new("demote")
	if rec := e.do(t, http.MethodPatch, path, patch, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("scoped Notify accepted a deployment action: %d %s", rec.Code, rec.Body.String())
	}
	patch.Action = nil
	rec = e.do(t, http.MethodPatch, path, patch, nil)
	rule = api.AlertRuleResponse{}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &rule) != nil || rule.ObserveOnly ||
		rule.EnvironmentScope != "staging" || rule.RecoveryConfirmationChecks != 2 || len(rule.ChannelIDs) != 1 || rule.ChannelIDs[0] != channel.ID {
		t.Fatalf("enable Notify and retain scope: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, http.MethodPatch, path, api.UpdateAlertRuleRequest{RecoveryConfirmationChecks: new(1)}, nil)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &rule) != nil || rule.RecoveryConfirmationChecks != 1 {
		t.Fatalf("change recovery policy: %d %s", rec.Code, rec.Body.String())
	}
}

func (s anomalyPreviewPromotionStore) ListDeploymentPromotions(ctx context.Context, accountID, appID, scope string, before time.Time, limit int) ([]state.DeploymentPromotion, error) {
	return s.list(ctx, accountID, appID, scope, before, limit)
}

func TestAlertAnomalyP95LatencyPreviewRoute(t *testing.T) {
	e := setup(t, api.PlanPro)
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "orders", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	e.s.anomalyAlertsEnabled = true
	replayStart := time.Now().UTC().Truncate(time.Minute).Add(-3 * time.Hour)
	promotedAt := replayStart.Add(10 * time.Minute)
	promotionReads := 0
	e.s.store = statetest.NewMem(anomalyPreviewPromotionStore{
		MemStore: e.store,
		list: func(_ context.Context, accountID, appID, scope string, before time.Time, limit int) ([]state.DeploymentPromotion, error) {
			promotionReads++
			if accountID != e.acct.ID || appID != app.ID || scope != state.DefaultEnvScope ||
				!before.Equal(replayStart.Add(2*time.Hour)) || limit != 101 {
				t.Fatalf("unexpected promotion lookup: %s %s %s %v %d", accountID, appID, scope, before, limit)
			}
			return []state.DeploymentPromotion{
				{DeploymentID: "current", CommitSHA: "abcdef0123456789", PromotedAt: promotedAt},
				{DeploymentID: "prior", PromotedAt: replayStart.Add(-time.Minute)},
				{DeploymentID: "older", PromotedAt: replayStart.Add(-time.Hour)},
			}, nil
		},
	})
	path := "/v1/apps/orders/alerts/anomaly-preview?metric=latency_p95_ms&window_spec=5m&comparison="
	replay := "&incident_replay_start=" + url.QueryEscape(replayStart.Format(time.RFC3339))
	queries := 0
	e.s.promqlClient = promql.NewClient("http://prometheus.invalid", &http.Client{Transport: anomalyPreviewTransport(func(r *http.Request) (*http.Response, error) {
		queries++
		q := r.URL.Query()
		start, err := strconv.ParseInt(q.Get("start"), 10, 64)
		if err != nil || r.URL.Path != "/api/v1/query_range" || q.Get("step") != "60" ||
			q.Get("end") != strconv.FormatInt(start+119*60, 10) {
			t.Fatalf("unexpected range query: %s", r.URL.String())
		}
		query := q.Get("query")
		success := `{app="` + app.ID + `",class="2xx"}[5m]`
		quantile := strings.Contains(query, "histogram_quantile(0.95,") &&
			strings.Contains(query, "gateway_request_duration_seconds_bucket"+success)
		if !quantile && (!strings.Contains(query, "increase(gateway_request_duration_seconds_count"+success+")") ||
			!strings.Contains(query, "vector(-1)")) {
			t.Fatalf("unexpected p95 expression: %s", query)
		}
		values := make([][2]any, 120)
		for i := range values {
			value := "50"
			if quantile {
				value = "100"
			}
			values[i] = [2]any{start + int64(i)*60, value}
		}
		if start == replayStart.Unix() {
			if quantile {
				values[13][1], values[14][1] = "400", "400"
			} else {
				values[14][1] = "49"
			}
		}
		body, err := json.Marshal(map[string]any{
			"status": "success",
			"data":   map[string]any{"resultType": "matrix", "result": []any{map[string]any{"metric": map[string]string{}, "values": values}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})})
	if rec := e.do(t, http.MethodGet, path+"below_baseline&threshold=0.25"+replay, nil, nil); rec.Code != http.StatusBadRequest || queries != 0 || promotionReads != 0 {
		t.Fatalf("invalid p95 comparison returned %d after %d queries and %d promotion reads", rec.Code, queries, promotionReads)
	}
	rec := e.do(t, http.MethodGet, path+"above_baseline&threshold=3"+replay, nil, nil)
	if rec.Code != http.StatusOK || queries != 16 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("p95 replay returned %d after %d queries: %s", rec.Code, queries, rec.Body.String())
	}
	var result api.AlertAnomalyPreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || len(result.Breaches) != 1 ||
		len(result.SkippedWindows) != 1 || result.IncidentReplay == nil || len(result.IncidentReplay.Incidents) != 1 {
		t.Fatalf("p95 response=%+v err=%v, want one incident and one skipped check", result, err)
	}
	breach, low := result.Breaches[0], result.SkippedWindows[0]
	if breach.Observed != 400 || breach.Baseline != 100 || breach.TriggerValue != 300 ||
		breach.EligibleRequests == nil || *breach.EligibleRequests != 50 || low.Reason != api.AlertAnomalyInsufficientTraffic ||
		low.EligibleRequests == nil || *low.EligibleRequests != 49 || low.RequiredRequests != 50 {
		t.Fatalf("breach=%+v skip=%+v, want the same 400 ms reading evaluated at 50 successful requests and skipped at 49", breach, low)
	}
	history := result.IncidentReplay.DeploymentContext
	if promotionReads != 1 || history == nil || history.Scope != state.DefaultEnvScope || history.Truncated ||
		len(history.Promotions) != 2 || history.Promotions[0].DeploymentID != "current" ||
		history.Promotions[0].CommitSHA != "abcdef0123456789" ||
		result.IncidentReplay.Incidents[0].OpenedAt.Sub(history.Promotions[0].PromotedAt) != 3*time.Minute ||
		history.Promotions[1].DeploymentID != "prior" {
		t.Fatalf("promotion reads=%d, history=%+v, incident=%+v", promotionReads, history, result.IncidentReplay.Incidents[0])
	}
}

func TestAlertAnomalyPreviewRoute(t *testing.T) {
	e := setup(t, api.PlanPro)
	acct, store, srv := e.acct, e.store, e.s
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "orders", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	const path = "/v1/apps/orders/alerts/anomaly-preview?comparison=below_baseline&window_spec=15m&threshold="
	if rec := e.do(t, http.MethodGet, path+"0.25", nil, nil); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "anomaly_alerts_unavailable") {
		t.Fatalf("unreleased preview returned %d %s", rec.Code, rec.Body.String())
	}
	srv.anomalyAlertsEnabled = true
	queries := 0
	now := time.Now().UTC()
	replayStart := now.Truncate(time.Minute).Add(-3*time.Hour - 17*time.Minute)
	srv.promqlClient = promql.NewClient("http://prometheus.invalid", &http.Client{Transport: anomalyPreviewTransport(func(r *http.Request) (*http.Response, error) {
		q := r.URL.Query()
		if r.URL.Path != "/api/v1/query_range" || !strings.Contains(q.Get("query"), `app="`+app.ID+`"`) {
			t.Fatalf("unexpected request to telemetry: %s", r.URL.String())
		}
		queries++
		start, err := strconv.ParseInt(q.Get("start"), 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		step, n := int64(3600), api.AnomalyPreviewChecks+api.AnomalyBaselineDays*24
		switch q.Get("step") {
		case "3600":
		case "60":
			step, n = 60, 120
		default:
			t.Fatalf("unexpected telemetry step %s", q.Get("step"))
		}
		if q.Get("end") != strconv.FormatInt(start+int64(n-1)*step, 10) {
			t.Fatalf("unexpected telemetry range end: %s", q.Get("end"))
		}
		values := make([][2]any, n)
		for i := range values {
			values[i] = [2]any{start + int64(i)*step, "100"}
		}
		if step == 3600 {
			values[n-1][1] = "0"
			values[n-2][1] = "40"
		} else if start == replayStart.Unix() {
			for i := 13; i < 21; i++ {
				values[i][1] = "0"
			}
			values[18][1] = "-1"
		} else if start == replayStart.Add(-21*24*time.Hour).Unix() {
			values[19][1] = "-1"
		}
		data := map[string]any{
			"resultType": "matrix", "result": []any{map[string]any{"metric": map[string]string{}, "values": values}},
		}
		body, err := json.Marshal(map[string]any{"status": "success", "data": data})
		if err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})})
	if rec := e.do(t, http.MethodGet, path+"NaN", nil, nil); rec.Code != http.StatusBadRequest || queries != 0 {
		t.Fatalf("invalid multiplier returned %d and ran %d telemetry queries", rec.Code, queries)
	}
	for _, candidate := range []string{"", "NaN", "0.95"} {
		if rec := e.do(t, http.MethodGet, path+"0.25&candidate_threshold="+candidate, nil, nil); rec.Code != http.StatusBadRequest ||
			!strings.Contains(rec.Body.String(), "candidate_threshold") || queries != 0 {
			t.Fatalf("invalid candidate %q returned %d and ran %d telemetry queries: %s", candidate, rec.Code, queries, rec.Body.String())
		}
	}
	if rec := e.do(t, http.MethodGet, strings.Replace(path+"0.25", "/orders/", "/other-app/", 1), nil, nil); rec.Code != http.StatusNotFound || queries != 0 {
		t.Fatalf("unknown app returned %d and ran %d telemetry queries", rec.Code, queries)
	}
	rec := e.do(t, http.MethodGet, path+"0.25", nil, nil)
	if rec.Code != http.StatusOK || queries != 1 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("preview returned %d after %d telemetry queries: %s", rec.Code, queries, rec.Body.String())
	}
	var result api.AlertAnomalyPreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || result.Evaluated != 168 || result.Skipped != 0 ||
		result.SkippedWindows == nil || len(result.SkippedWindows) != 0 || len(result.Breaches) != 1 ||
		result.Breaches[0].Observed != 0 || result.Breaches[0].TriggerValue != 25 || result.Candidate != nil {
		t.Fatalf("preview=%+v err=%v, want one observed zero breach", result, err)
	}
	rec = e.do(t, http.MethodGet, path+"0.25&candidate_threshold=0.5", nil, nil)
	if rec.Code != http.StatusOK || queries != 2 {
		t.Fatalf("comparison returned %d after %d telemetry queries: %s", rec.Code, queries, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || result.Candidate == nil || result.Candidate.Threshold != 0.5 ||
		len(result.Candidate.Breaches) != 2 || result.Candidate.Breaches[0].Observed != 40 || result.Candidate.Breaches[0].TriggerValue != 50 {
		t.Fatalf("candidate=%+v err=%v, want two breaches at 50 requests", result.Candidate, err)
	}
	replayQuery := "&incident_replay_start=" + url.QueryEscape(replayStart.Format(time.RFC3339))
	for _, invalid := range []string{
		"&cooldown_minutes=5",
		"&recovery_confirmation_checks=2",
		replayQuery + "&cooldown_minutes=4",
		replayQuery + "&recovery_confirmation_checks=3",
		"&incident_replay_start=invalid",
		"&incident_replay_start=" + url.QueryEscape(replayStart.Add(time.Second).Format(time.RFC3339)),
		"&incident_replay_start=" + url.QueryEscape(now.Truncate(time.Minute).Format(time.RFC3339)),
		"&incident_replay_start=" + url.QueryEscape(replayStart.Add(-8*24*time.Hour).Format(time.RFC3339)),
	} {
		if rec := e.do(t, http.MethodGet, path+"0.25"+invalid, nil, nil); rec.Code != http.StatusBadRequest || queries != 2 {
			t.Fatalf("invalid replay %q returned %d and ran %d telemetry queries: %s", invalid, rec.Code, queries, rec.Body.String())
		}
	}
	rec = e.do(t, http.MethodGet, path+"0.25"+replayQuery+"&candidate_threshold=0.5&cooldown_minutes=5", nil, nil)
	if rec.Code != http.StatusOK || queries != 10 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("incident replay returned %d after %d queries: %s", rec.Code, queries, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || result.IncidentReplay == nil ||
		result.IncidentReplay.CooldownMinutes != 5 || result.StepSeconds != 60 || result.Skipped != 1 || result.Evaluated != 119 ||
		len(result.IncidentReplay.Incidents) != 1 || result.IncidentReplay.Incidents[0].EligibleNotifications != 2 ||
		len(result.IncidentReplay.CandidateIncidents) != 1 || result.IncidentReplay.RecoveryConfirmation != nil {
		t.Fatalf("incident response=%+v err=%v, want one incident per multiplier", result.IncidentReplay, err)
	}
	for _, invalid := range []string{
		path + "0.25&candidate_baseline=unknown",
		path + "0.25&candidate_baseline=same_weekday&candidate_threshold=0.5",
		strings.Replace(path, "window_spec=15m", "window_spec=1h", 1) + "0.25&candidate_baseline=same_weekday",
	} {
		if rec := e.do(t, http.MethodGet, invalid, nil, nil); rec.Code != http.StatusBadRequest || queries != 10 {
			t.Fatalf("invalid baseline request returned %d after %d queries: %s", rec.Code, queries, rec.Body.String())
		}
	}
	rec = e.do(t, http.MethodGet, path+"0.25"+replayQuery+"&candidate_baseline=same_weekday&cooldown_minutes=5&recovery_confirmation_checks=2", nil, nil)
	if rec.Code != http.StatusOK || queries != 20 {
		t.Fatalf("same-weekday replay returned %d after %d queries: %s", rec.Code, queries, rec.Body.String())
	}
	var compared api.AlertAnomalyPreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &compared); err != nil || compared.Candidate == nil || compared.Candidate.Threshold != 0.25 ||
		compared.Candidate.WeekdayBaseline == nil || compared.Candidate.WeekdayBaseline.Evaluated != 119 ||
		len(compared.Candidate.WeekdayBaseline.FallbackAt) != 1 || !compared.Candidate.WeekdayBaseline.FallbackAt[0].Equal(replayStart.Add(19*time.Minute)) {
		t.Fatalf("same-weekday comparison=%+v err=%v, want the fixed multiplier and explicit fallback time", compared.Candidate, err)
	}
	if compared.IncidentReplay == nil || compared.IncidentReplay.RecoveryConfirmation == nil {
		t.Fatal("weekday replay omitted recovery confirmation")
	}
	confirmation := compared.IncidentReplay.RecoveryConfirmation
	if confirmation.RequiredHealthyChecks != 2 || len(confirmation.Incidents) != 1 || len(confirmation.CandidateIncidents) != 1 {
		t.Fatalf("recovery confirmation=%+v, want the same incident under both baselines", confirmation)
	}
	for _, incident := range []api.AlertObservedIncident{confirmation.Incidents[0], confirmation.CandidateIncidents[0]} {
		if incident.EndedAt == nil || !incident.EndedAt.Equal(replayStart.Add(22*time.Minute)) || incident.EligibleNotifications != 2 {
			t.Fatalf("confirmed incident=%+v, want recovery on the second healthy check", incident)
		}
	}
	if rules, err := store.ListAlertRulesForAccount(t.Context(), acct.ID); err != nil || len(rules) != 0 {
		t.Fatalf("preview changed alert rules: %v %v", rules, err)
	}
}

func TestAlertAnomalyErrorRatePreviewRoute(t *testing.T) {
	e := setup(t, api.PlanPro)
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "orders", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	e.s.anomalyAlertsEnabled = true
	replayStart := time.Now().UTC().Truncate(time.Minute).Add(-3 * time.Hour)
	path := "/v1/apps/orders/alerts/anomaly-preview?metric=error_rate_pct&window_spec=5m&comparison="
	replay := "&incident_replay_start=" + url.QueryEscape(replayStart.Format(time.RFC3339))
	queries := 0
	zeroBaseline := false
	e.s.promqlClient = promql.NewClient("http://prometheus.invalid", &http.Client{Transport: anomalyPreviewTransport(func(r *http.Request) (*http.Response, error) {
		queries++
		q := r.URL.Query()
		start, err := strconv.ParseInt(q.Get("start"), 10, 64)
		if err != nil || r.URL.Path != "/api/v1/query_range" || q.Get("step") != "60" ||
			q.Get("end") != strconv.FormatInt(start+119*60, 10) {
			t.Fatalf("unexpected range query: %s", r.URL.String())
		}
		query := q.Get("query")
		rate := query == appmetrics.ErrorRatePctQuery(app.ID, "5m")
		failures := query == appmetrics.FailedRequestsQuery("increase", app.ID, "5m")+" or vector(0)"
		if !rate && !failures && (!strings.Contains(query, appmetrics.EligibleRequestsQuery("increase", app.ID, "5m")) || !strings.Contains(query, "vector(-1)")) {
			t.Fatalf("unexpected metric expression: %s", query)
		}
		values := make([][2]any, 120)
		for i := range values {
			value := "100"
			if failures || rate && zeroBaseline {
				value = "0"
			} else if rate {
				value = "1"
			}
			values[i] = [2]any{start + int64(i)*60, value}
		}
		if start == replayStart.Unix() {
			if zeroBaseline {
				switch {
				case failures:
					values[13][1], values[14][1], values[15][1] = "10", "4", "5"
				case rate:
					values[13][1], values[14][1], values[15][1] = "10", "8", "2.5"
				default:
					values[14][1], values[15][1] = "50", "200"
				}
			} else if rate {
				values[13][1], values[14][1] = "10", "20"
			} else {
				values[14][1], values[15][1] = "10", "-1"
			}
		}
		body, err := json.Marshal(map[string]any{
			"status": "success",
			"data":   map[string]any{"resultType": "matrix", "result": []any{map[string]any{"metric": map[string]string{}, "values": values}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})})
	for _, invalid := range []string{
		path + "below_baseline&threshold=0.25" + replay,
		path + "above_baseline&threshold=3&candidate_baseline=same_weekday" + replay,
		strings.Replace(path, "error_rate_pct", "latency_p99_ms", 1) + "above_baseline&threshold=3" + replay,
		strings.Replace(path, "error_rate_pct", "request_count", 1) + "above_baseline&threshold=3&candidate_baseline=zero_baseline_fallback" + replay,
		path + "above_baseline&threshold=3&candidate_baseline=zero_baseline_fallback&candidate_threshold=4" + replay,
	} {
		if rec := e.do(t, http.MethodGet, invalid, nil, nil); rec.Code != http.StatusBadRequest || queries != 0 {
			t.Fatalf("invalid metric request returned %d after %d telemetry queries: %s", rec.Code, queries, rec.Body.String())
		}
	}
	rec := e.do(t, http.MethodGet, path+"above_baseline&threshold=3"+replay, nil, nil)
	if rec.Code != http.StatusOK || queries != 16 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("replay returned %d after %d telemetry queries: %s", rec.Code, queries, rec.Body.String())
	}
	var result api.AlertAnomalyPreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || result.Evaluated != 118 || result.Skipped != 2 ||
		len(result.Breaches) != 1 || len(result.SkippedWindows) != 2 || result.IncidentReplay == nil ||
		len(result.IncidentReplay.Incidents) != 1 {
		t.Fatalf("error-rate response=%+v err=%v, want one incident and two skipped checks", result, err)
	}
	breach, low, missing := result.Breaches[0], result.SkippedWindows[0], result.SkippedWindows[1]
	if breach.Observed != 10 || breach.Baseline != 1 || breach.TriggerValue != 3 ||
		breach.EligibleRequests == nil || *breach.EligibleRequests != 100 ||
		low.Reason != api.AlertAnomalyInsufficientTraffic || low.EligibleRequests == nil || *low.EligibleRequests != 10 ||
		low.RequiredRequests != 50 || missing.Reason != api.AlertAnomalyTelemetryMissing || missing.EligibleRequests != nil {
		t.Fatalf("breach=%+v skips=%+v, want rated breach on 100 requests and distinct low-traffic and telemetry skips", breach, result.SkippedWindows)
	}
	zeroBaseline = true
	rec = e.do(t, http.MethodGet, path+"above_baseline&threshold=3&candidate_baseline=zero_baseline_fallback"+replay, nil, nil)
	if rec.Code != http.StatusOK || queries != 33 {
		t.Fatalf("fallback replay returned %d after %d telemetry queries: %s", rec.Code, queries, rec.Body.String())
	}
	var fallbackResult api.AlertAnomalyPreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &fallbackResult); err != nil || len(fallbackResult.Breaches) != 0 ||
		fallbackResult.Candidate == nil || fallbackResult.Candidate.ZeroBaselineFallback == nil ||
		len(fallbackResult.Candidate.Breaches) != 1 || fallbackResult.IncidentReplay == nil ||
		len(fallbackResult.IncidentReplay.Incidents) != 0 || len(fallbackResult.IncidentReplay.CandidateIncidents) != 1 {
		t.Fatalf("fallback response=%+v err=%v, want one incident for the fallback candidate", fallbackResult, err)
	}
	opened := fallbackResult.IncidentReplay.CandidateIncidents[0].OpenedAt
	fallbackBreach := fallbackResult.Candidate.Breaches[0]
	if !fallbackBreach.ZeroBaselineFallback || fallbackBreach.FailedRequests == nil || *fallbackBreach.FailedRequests != 10 ||
		fallbackBreach.TriggerValue != 5 || !opened.Equal(fallbackBreach.EvaluatedAt) || !opened.Equal(replayStart.Add(13*time.Minute)) {
		t.Fatalf("fallback breach=%+v opened=%v, want 10 failures to qualify at the 5%% absolute trigger", fallbackBreach, opened)
	}
}

func TestAlertAnomalyEnvironmentPreviewRoute(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := t.Context()
	project, err := e.store.CreateProject(ctx, state.Project{AccountID: e.acct.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "orders", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	e.s.anomalyAlertsEnabled = true
	replayStart := time.Now().UTC().Truncate(time.Minute).Add(-3 * time.Hour)
	e.s.store = statetest.NewMem(anomalyPreviewPromotionStore{
		MemStore: e.store,
		list: func(_ context.Context, accountID, appID, scope string, _ time.Time, _ int) ([]state.DeploymentPromotion, error) {
			if accountID != e.acct.ID || appID != app.ID {
				t.Fatalf("unexpected deployment history owner: %s %s", accountID, appID)
			}
			return []state.DeploymentPromotion{{DeploymentID: scope + "-release", PromotedAt: replayStart.Add(10 * time.Minute)}}, nil
		},
	})
	scope := ""
	warming := false
	reads := 0
	e.s.promqlClient = promql.NewClient("http://prometheus.invalid", &http.Client{Transport: anomalyPreviewTransport(func(r *http.Request) (*http.Response, error) {
		reads++
		params := r.URL.Query()
		query := params.Get("query")
		label := `app="` + app.ID + `"`
		metricPrefix := "gateway_request_duration_seconds_count"
		if scope != "" {
			label += `,scope="` + scope + `"`
			metricPrefix = "gateway_environment_request_duration_seconds_count"
		}
		if !strings.Contains(query, label) || !strings.Contains(query, metricPrefix) ||
			(scope != "" && !strings.Contains(query, "gateway_environment_capacity_refusals_total")) {
			t.Fatalf("scope=%q, unexpected query %s", scope, query)
		}
		start, _ := strconv.ParseInt(params.Get("start"), 10, 64)
		end, _ := strconv.ParseInt(params.Get("end"), 10, 64)
		step, _ := strconv.ParseInt(params.Get("step"), 10, 64)
		rate := strings.Contains(query, "rate(")
		values := make([][2]any, 0, (end-start)/step+1)
		for at := start; at <= end; at += step {
			value := "100"
			if rate {
				value = "1"
				if scope == "staging" && at == replayStart.Add(13*time.Minute).Unix() {
					value = "10"
				}
			} else if warming && start < replayStart.Unix() {
				value = "-1"
			}
			values = append(values, [2]any{at, value})
		}
		body, err := json.Marshal(map[string]any{
			"status": "success",
			"data": map[string]any{"resultType": "matrix", "result": []any{
				map[string]any{"metric": map[string]string{}, "values": values},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})})
	path := "/v1/apps/orders/alerts/anomaly-preview?metric=error_rate_pct&window_spec=5m&comparison=above_baseline&threshold=3"
	replay := path + "&incident_replay_start=" + url.QueryEscape(replayStart.Format(time.RFC3339))
	for _, invalid := range []string{path + "&environment_scope=staging", replay + "&environment_scope=foreign", replay + "&environment_scope="} {
		if rec := e.do(t, http.MethodGet, invalid, nil, nil); rec.Code != http.StatusBadRequest || reads != 0 {
			t.Fatalf("invalid scope returned %d after %d reads", rec.Code, reads)
		}
	}
	run := func(url string) api.AlertAnomalyPreviewResponse {
		t.Helper()
		rec := e.do(t, http.MethodGet, url, nil, nil)
		var response api.AlertAnomalyPreviewResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); rec.Code != http.StatusOK || err != nil {
			t.Fatalf("status=%d, err=%v, body=%s", rec.Code, err, rec.Body.String())
		}
		return response
	}
	overview := run(path)
	if strings.Join(overview.EnvironmentScopes, ",") != "default,production,staging" {
		t.Fatalf("unexpected environments: %v", overview.EnvironmentScopes)
	}
	for _, tc := range []struct {
		scope     string
		incidents int
	}{{"staging", 1}, {"production", 0}} {
		scope = tc.scope
		got := run(replay + "&environment_scope=" + scope)
		review := got.IncidentReplay
		if got.EnvironmentScope != scope || got.Evaluated != 120 || review == nil || len(review.Incidents) != tc.incidents ||
			review.DeploymentContext == nil || review.DeploymentContext.Scope != scope ||
			len(review.DeploymentContext.Promotions) != 1 || review.DeploymentContext.Promotions[0].DeploymentID != scope+"-release" {
			t.Fatalf("scope %s: got %+v, replay %+v", scope, got, review)
		}
	}
	scope, warming = "staging", true
	got := run(replay + "&environment_scope=staging")
	if got.Evaluated != 0 || len(got.SkippedWindows) != 120 || got.SkippedWindows[13].Reason != api.AlertAnomalyInsufficientHistory {
		t.Fatalf("staging without historical data: evaluated=%d skipped=%d", got.Evaluated, len(got.SkippedWindows))
	}
}
