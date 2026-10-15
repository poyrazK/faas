package apidserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func gapEnv(t *testing.T) apidserver.TestEnv {
	return apidserver.NewObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
}

func TestObsRegressionCoverageZero(t *testing.T) {
	e := gapEnv(t)
	if err := (*e.Store()).InsertAuditLog(context.Background(), state.AuditLog{ID: uuid.New(), Kind: "operator.action.force_park", ReceivedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	p := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := int64(0)
		query := r.URL.Query().Get("query")
		if strings.Contains(query, "timestamp(") || strings.Contains(query, "last_evaluation_timestamp_seconds") {
			value = time.Now().Unix()
		}
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1728000000,"%d"]}]}}`, value)
	}))
	defer p.Close()
	*(*e.S()).PromqlClient() = promql.NewClient(p.URL, p.Client())
	rec := e.Do(t, "GET", "/v1/admin/obs/health", nil, nil)
	var got api.ObsHealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || got.AuditLogCoverageRatio5m != 0 || !got.PrometheusAvailable {
		t.Fatalf("unexpected result: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestObsRegressionPrometheusOutage(t *testing.T) {
	e := gapEnv(t)
	p := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "offline", 503) }))
	defer p.Close()
	*(*e.S()).PromqlClient() = promql.NewClient(p.URL, p.Client())
	rec := e.Do(t, "GET", "/v1/admin/obs/incidents?type=platform_alert", nil, nil)
	var got api.ObsIncidentListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || len(got.Items) != 0 || len(got.UnavailableSources) != 2 || got.UnavailableSources[0] != "prometheus" || got.UnavailableSources[1] != "platform_alert_history" {
		t.Fatalf("unexpected result: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestObsRegressionNodeCursor(t *testing.T) {
	e := gapEnv(t)
	for i := 0; i < 3; i++ {
		_, err := (*e.Store()).UpsertComputeNodeFromOperator(context.Background(), state.ComputeNode{ID: uuid.NewString(), Name: fmt.Sprintf("gap-node-%d", i), Lifecycle: state.NodeLifecycleActive, LastHeartbeatAt: time.Now().UTC()})
		if err != nil {
			t.Fatal(err)
		}
	}
	var first, second api.ObsNodeListResponse
	rec := e.Do(t, "GET", "/v1/admin/obs/nodes?limit=1", nil, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("first page: %s", rec.Body.String())
	}
	rec = e.Do(t, "GET", "/v1/admin/obs/nodes?limit=1&cursor="+first.NextCursor, nil, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].ID == first.Items[0].ID {
		t.Fatalf("second page: %s", rec.Body.String())
	}
}

func TestObsRegressionIncidentCap(t *testing.T) {
	e := gapEnv(t)
	ctx := context.Background()
	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 201; i++ {
		app, err := (*e.Store()).CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: fmt.Sprintf("gap-app-%d", i), Type: state.AppTypeApp})
		if err != nil {
			t.Fatal(err)
		}
		status := state.DeployPending
		if i == 0 {
			status = state.DeployFailed
		}
		_, err = (*e.Store()).CreateDeployment(ctx, state.Deployment{ID: uuid.NewString(), AppID: app.ID, Status: status, CreatedAt: base.Add(time.Duration(i) * time.Second)})
		if err != nil {
			t.Fatal(err)
		}
	}
	var all, filtered api.ObsIncidentListResponse
	rec := e.Do(t, "GET", "/v1/admin/obs/incidents?type=deployment&limit=200", nil, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &all); err != nil {
		t.Fatal(err)
	}
	if len(all.Items) != 200 || all.NextCursor == "" {
		t.Fatalf("cap response: %s", rec.Body.String())
	}
	rec = e.Do(t, "GET", "/v1/admin/obs/incidents?type=deployment&limit=200&cursor="+all.NextCursor, nil, nil)
	var tail api.ObsIncidentListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &tail); err != nil {
		t.Fatal(err)
	}
	if len(tail.Items) != 1 || tail.NextCursor != "" {
		t.Fatalf("tail response: %s", rec.Body.String())
	}
	rec = e.Do(t, "GET", "/v1/admin/obs/incidents?type=deployment&severity=error", nil, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &filtered); err != nil {
		t.Fatal(err)
	}
	if len(filtered.Items) != 1 || filtered.Items[0].Severity != "error" {
		t.Fatalf("filtered response: %s", rec.Body.String())
	}
}

type obsFailedApps struct{ Store state.Store }

func (f *obsFailedApps) OperatorTenantApps(context.Context, string) ([]api.ObsTenantApp, error) {
	return nil, errors.New("database unavailable")
}

func TestObsRegressionTenantFailureIsUnavailable(t *testing.T) {
	e := gapEnv(t)
	*(*e.S()).Store() = statetest.NewStore(&obsFailedApps{Store: *(*e.S()).Store()})
	rec := e.Do(t, "GET", "/v1/admin/obs/tenants/"+(*e.Acct()).ID, nil, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestObsRegressionWakeNodeBuckets(t *testing.T) {
	e := gapEnv(t)
	node, err := (*e.Store()).UpsertComputeNodeFromOperator(context.Background(), state.ComputeNode{ID: uuid.NewString(), Name: "wake-node", Lifecycle: state.NodeLifecycleActive})
	if err != nil {
		t.Fatal(err)
	}
	p := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("query"), "by (node_id, le)") {
			fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"node_id":%q,"le":"0.1"},"value":[1728000000,"2"]},{"metric":{"node_id":%q,"le":"0.5"},"value":[1728000000,"10"]},{"metric":{"node_id":%q,"le":"+Inf"},"value":[1728000000,"10"]}]}}`, node.ID, node.ID, node.ID)
		} else {
			fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1728000000,"500"]}]}}`)
		}
	}))
	defer p.Close()
	*(*e.S()).PromqlClient() = promql.NewClient(p.URL, p.Client())
	rec := e.Do(t, "GET", "/v1/admin/obs/nodes/wake-latency", nil, nil)
	var response api.ObsNodeWakeLatencyResponse
	if err = json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || !response.PrometheusAvailable || response.Window != "5m" || len(response.Quantiles) != 1 || response.Quantiles[0].NodeName != "wake-node" || response.Quantiles[0].SampleCount != 10 {
		t.Fatalf("response: %s", rec.Body.String())
	}
}

func TestObsRegressionAlertsPreserveIdentityAndSeverity(t *testing.T) {
	e := gapEnv(t)
	p := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/alerts" {
			fmt.Fprint(w, `{"status":"success","data":{"alerts":[]}}`)
			return
		}
		query := r.URL.Query().Get("query")
		if !strings.Contains(query, `alertname!="FaasWatchdog"`) || !strings.Contains(query, `severity!="info"`) {
			t.Errorf("query includes heartbeat or info alerts: %s", query)
		}
		fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"alertname":"FaasAPIUnavailable","component":"apid","severity":"page","alertstate":"firing"},"value":[1728000000,"1727999900"]},{"metric":{"alertname":"FaasAuditWarn","component":"schedd","severity":"warn","alertstate":"firing"},"value":[1728000000,"1727999900"]}]}}`)
	}))
	defer p.Close()
	*(*e.S()).PromqlClient() = promql.NewClient(p.URL, p.Client())
	var response api.ObsIncidentListResponse
	rec := e.Do(t, "GET", "/v1/admin/obs/incidents?type=platform_alert&limit=1", nil, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 || response.NextCursor == "" || len(response.UnavailableSources) != 1 || response.UnavailableSources[0] != "platform_alert_history" || response.Items[0].Status != "unknown" {
		t.Fatalf("first page: %s", rec.Body.String())
	}
	first := response.Items[0]
	rec = e.Do(t, "GET", "/v1/admin/obs/incidents?type=platform_alert&limit=1&cursor="+response.NextCursor, nil, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 || response.Items[0].ID == first.ID || response.Items[0].Severity == first.Severity {
		t.Fatalf("second page: %s", rec.Body.String())
	}
}

func TestObsRegressionOverviewAndTenant(t *testing.T) {
	e := gapEnv(t)
	ctx := context.Background()
	app, err := (*e.Store()).CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: "gap-summary-app", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	_, err = (*e.Store()).CreateDeployment(ctx, state.Deployment{ID: uuid.NewString(), AppID: app.ID, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	accountID := (*e.Acct()).ID
	for _, kind := range []string{"auth.rate_limited", "deployment.failed"} {
		if err = (*e.Store()).AppendEvent(ctx, "apid", kind, &accountID, []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	rec := e.Do(t, "GET", "/v1/admin/obs/overview", nil, nil)
	var overview api.ObsOverviewResponse
	if err = json.Unmarshal(rec.Body.Bytes(), &overview); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || len(overview.RecentFailures1h) != 1 || overview.RecentFailures1h[0].Count != 1 || len(overview.TopRateLimitedAccounts24h) != 1 || overview.TopRateLimitedAccounts24h[0].Hits != 1 {
		t.Fatalf("unexpected overview: %s", rec.Body.String())
	}
	rec = e.Do(t, "GET", "/v1/admin/obs/tenants", nil, nil)
	var tenants api.ObsTenantListResponse
	if err = json.Unmarshal(rec.Body.Bytes(), &tenants); err != nil {
		t.Fatal(err)
	}
	for _, tenant := range tenants.Items {
		if tenant.AccountID == accountID {
			if tenant.AppsCount != 1 || tenant.DeploymentsLiveCount != 1 {
				t.Fatalf("unexpected tenant counts: %+v", tenant)
			}
			return
		}
	}
	t.Fatal("tenant not found")
}

func TestObsRegressionFailedJob(t *testing.T) {
	e := gapEnv(t)
	ctx := context.Background()
	job, err := (*e.Store()).JobCreate(ctx, (*e.Acct()).ID, "gap-job", "app", "", nil, 128, 60, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := (*e.Store()).JobRunCreate(ctx, job.ID, (*e.Acct()).ID, "manual", nil, nil, nil, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = (*e.Store()).JobTaskMarkTerminal(ctx, run.ID, 0, "failed", 1, "app", "test failure", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	run, err = (*e.Store()).JobRunRecompute(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.AggregateStatus != "failed" {
		t.Fatalf("run status=%q", run.AggregateStatus)
	}
	rec := e.Do(t, "GET", "/v1/admin/obs/incidents?type=job_run", nil, nil)
	var got api.ObsIncidentListResponse
	if err = json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || len(got.Items) != 1 || got.Items[0].ID != "job_run/"+run.ID || got.Items[0].Severity != "error" {
		t.Fatalf("response: %s", rec.Body.String())
	}
}
