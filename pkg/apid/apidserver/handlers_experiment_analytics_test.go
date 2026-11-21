package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/platformflags"
)

// ADR 20261119102741 §1, §2, §5: funnel and retention metrics need product
// analytics, take no metric_event, are stored with their definition and
// CUPED setting, and results report CUPED.
func TestFlagExperimentFunnelAndRetentionMetrics(t *testing.T) {
	f := newFlagExperimentTestEnv(t)
	funnel := &api.FlagExperimentFunnel{Window: "72h", Steps: []api.InsightStepRequest{{Event: "signup"}, {Event: "order_completed", Where: map[string]string{"plan": "pro"}}}}
	req := api.FlagExperimentCreateRequest{Key: "onboarding", Flag: "checkout", RuleID: "weighted", Control: "legacy",
		MetricKind: flags.ExperimentMetricFunnel, Funnel: funnel, Start: true}
	if code, body := f.do(t, http.MethodPost, f.base, req); code != http.StatusForbidden || !strings.Contains(body, "product analytics") {
		t.Fatalf("funnel without analytics = %d %s; want 403", code, body)
	}
	(*f.e.S()).EnablePlatformFlagForAll(platformflags.ProductAnalytics)

	for _, tc := range []struct {
		name string
		req  api.FlagExperimentCreateRequest
		want string
	}{
		{"metric event", api.FlagExperimentCreateRequest{Key: "k1", Flag: "checkout", RuleID: "weighted", Control: "legacy", MetricEvent: "signup",
			MetricKind: flags.ExperimentMetricFunnel, Funnel: funnel}, "metric_event"},
		{"one step", api.FlagExperimentCreateRequest{Key: "k2", Flag: "checkout", RuleID: "weighted", Control: "legacy",
			MetricKind: flags.ExperimentMetricFunnel, Funnel: &api.FlagExperimentFunnel{Window: "1h", Steps: []api.InsightStepRequest{{Event: "a"}}}}, "step"},
		{"retention period", api.FlagExperimentCreateRequest{Key: "k3", Flag: "checkout", RuleID: "weighted", Control: "legacy",
			MetricKind: flags.ExperimentMetricRetention, Retention: &api.FlagExperimentRetention{Return: api.InsightStepRequest{Event: "a"}, Period: "day", PeriodNumber: 13}}, "period"},
		{"definition for conversion", api.FlagExperimentCreateRequest{Key: "k4", Flag: "checkout", RuleID: "weighted", Control: "legacy", MetricEvent: "signup", Funnel: funnel}, "funnel"},
	} {
		if code, body := f.do(t, http.MethodPost, f.base, tc.req); code != http.StatusUnprocessableEntity || !strings.Contains(body, tc.want) {
			t.Errorf("%s: %d %s; want 422 naming %q", tc.name, code, body, tc.want)
		}
	}

	x := f.create(t, req)
	if x.MetricEvent != "order_completed" || !x.Cuped || len(x.MetricDefinition.Steps) != 2 || x.MetricDefinition.Steps[1].Where["plan"] != "pro" {
		t.Fatalf("funnel experiment = %+v; want the last step as its event and CUPED on", x)
	}
	off := false
	ret := f.create(t, api.FlagExperimentCreateRequest{Key: "week-one", Flag: "checkout", RuleID: "weighted", Control: "legacy",
		MetricKind: flags.ExperimentMetricRetention, Cuped: &off,
		Retention: &api.FlagExperimentRetention{Return: api.InsightStepRequest{Event: "order_completed"}, Period: "week", PeriodNumber: 1}})
	if ret.Cuped || ret.MetricEvent != "order_completed" || ret.MetricDefinition.PeriodNumber != 1 {
		t.Fatalf("retention experiment = %+v", ret)
	}

	code, body := f.do(t, http.MethodGet, f.base+"/onboarding", nil)
	var got api.FlagExperiment
	if err := json.Unmarshal([]byte(body), &got); code != http.StatusOK || err != nil || got.Funnel == nil || got.Funnel.Window != "72h" || !got.Cuped {
		t.Fatalf("get = %d %s", code, body)
	}
	apidserver.RunFlagExperimentPass(*f.e.S(), time.Now())
	code, body = f.do(t, http.MethodGet, f.base+"/onboarding/results", nil)
	var res api.FlagExperimentResults
	if err := json.Unmarshal([]byte(body), &res); code != http.StatusOK || err != nil || res.Cuped.Applied || res.Cuped.PreDays != api.ExperimentCupedPreDays || res.Cuped.Reason == "" {
		t.Fatalf("results = %d %s; want CUPED not applied with a reason", code, body)
	}
	if _, body = f.do(t, http.MethodGet, f.base+"/week-one/results", nil); !strings.Contains(body, `"reason":"off"`) {
		t.Fatalf("results without CUPED = %s", body)
	}
}

// ADR 20261119102741 §3, §5: cohorts need product analytics, are validated
// and bounded, and the reconciler computes their members.
func TestInsightCohortsLifecycle(t *testing.T) {
	e, token, customer := flagEventsRuntime(t, api.PlanPro)
	base := "/v1/projects/flags-events/environments/production/cohorts"
	create := api.InsightCohortRequest{Key: "signed-up", Kind: "did", Event: "signup", WithinDays: 7}
	if res := e.Do(t, http.MethodPost, base, create, nil); res.Code != http.StatusForbidden {
		t.Fatalf("cohort with analytics off = %d %s; want 403", res.Code, res.Body.String())
	}
	(*e.S()).EnablePlatformFlagForAll(platformflags.ProductAnalytics)
	now := time.Now().UTC()
	ingest := e.Do(t, http.MethodPost, "/v1/runtime/flags/events", api.FlagEventsRequest{Events: []api.FlagEvent{
		{Name: "signup", CustomerID: customer, At: now.Add(-time.Hour)},
	}}, map[string]string{"Authorization": "Bearer " + token})
	if ingest.Code != http.StatusOK {
		t.Fatalf("ingest = %d %s", ingest.Code, ingest.Body.String())
	}

	for _, tc := range []struct {
		name string
		req  api.InsightCohortRequest
		want string
	}{
		{"kind", api.InsightCohortRequest{Key: "a", Kind: "maybe", Event: "signup", WithinDays: 7}, "kind"},
		{"days", api.InsightCohortRequest{Key: "a", Kind: "did", Event: "signup", WithinDays: 400}, "within_days"},
		{"key", api.InsightCohortRequest{Key: "Not A Key", Kind: "did", Event: "signup", WithinDays: 7}, "key"},
		{"funnel steps", api.InsightCohortRequest{Key: "a", Kind: "funnel", Window: "1h", WithinDays: 7, Steps: []api.InsightStepRequest{{Event: "a"}}}, "step"},
	} {
		if res := e.Do(t, http.MethodPost, base, tc.req, nil); res.Code != http.StatusUnprocessableEntity || !strings.Contains(res.Body.String(), tc.want) {
			t.Errorf("%s: %d %s; want 422 naming %q", tc.name, res.Code, res.Body.String(), tc.want)
		}
	}

	res := e.Do(t, http.MethodPost, base, create, nil)
	var c api.InsightCohort
	if err := json.Unmarshal(res.Body.Bytes(), &c); res.Code != http.StatusCreated || err != nil || c.Members != 0 || c.MaxMembers != 10_000 || c.RefreshedAt != nil {
		t.Fatalf("create = %d %s", res.Code, res.Body.String())
	}
	if res := e.Do(t, http.MethodPost, base, create, nil); res.Code != http.StatusConflict {
		t.Fatalf("duplicate = %d %s; want 409", res.Code, res.Body.String())
	}

	apidserver.RunFlagExperimentPass(*e.S(), time.Now())
	res = e.Do(t, http.MethodGet, base+"/signed-up", nil, nil)
	if err := json.Unmarshal(res.Body.Bytes(), &c); res.Code != http.StatusOK || err != nil || c.Members != 1 || c.RefreshedAt == nil || c.RefreshError != "" {
		t.Fatalf("after a pass = %d %s; want one member", res.Code, res.Body.String())
	}
	if res := e.Do(t, http.MethodGet, base, nil, nil); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"key":"signed-up"`) {
		t.Fatalf("list = %d %s", res.Code, res.Body.String())
	}
	if res := e.Do(t, http.MethodPost, base+"/signed-up/refresh", nil, nil); res.Code != http.StatusAccepted {
		t.Fatalf("refresh = %d %s", res.Code, res.Body.String())
	}
	if res := e.Do(t, http.MethodDelete, base+"/signed-up", nil, nil); res.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", res.Code, res.Body.String())
	}
	if res := e.Do(t, http.MethodGet, base+"/signed-up", nil, nil); res.Code != http.StatusNotFound {
		t.Fatalf("get after delete = %d %s", res.Code, res.Body.String())
	}
}
