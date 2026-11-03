package state_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgIssueRejectsConflictingRequestAndTraceEvidence(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	dep := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC().Truncate(time.Microsecond)
	consumerA, consumerB, tenantA, tenantB := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	requestA, journalOnly, auditOnly, untracedAudit := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	traceA, traceB := strings.Repeat("a", 32), strings.Repeat("b", 32)
	for _, tenant := range []string{tenantA, tenantB} {
		if _, err := pool.Exec(ctx, `INSERT INTO platform_tenants(id,account_id,external_ref,name) VALUES($1,$2,$3,$3)`, tenant, account.ID, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
	}
	audit := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO api_consumer_usage_events(event_id,account_id,app_id,consumer_key,window_start,request_count,error_count,billable_units) VALUES($1,$2,$3,$4,date_trunc('minute',now()),1,1,0)`, audit, account.ID, app.ID, consumerA); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO request_audit_events(event_id,account_id,app_id,consumer_key,platform_tenant_id,route_template,method,http_status,latency_ms,deployment_id,occurred_at,request_id,trace_id) VALUES($1,$2,$3,$4,$5,'/','GET',500,1,$6,$7,$8,$9)`, audit, account.ID, app.ID, consumerA, tenantA, dep.ID, now, requestA, traceA); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ request, trace string }{{auditOnly, traceA}, {untracedAudit, ""}} {
		id := uuid.NewString()
		if _, err := pool.Exec(ctx, `INSERT INTO api_consumer_usage_events(event_id,account_id,app_id,consumer_key,window_start,request_count,error_count,billable_units) VALUES($1,$2,$3,$4,date_trunc('minute',now()),1,1,0)`, id, account.ID, app.ID, consumerA); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO request_audit_events(event_id,account_id,app_id,consumer_key,platform_tenant_id,route_template,method,http_status,latency_ms,deployment_id,occurred_at,request_id,trace_id) VALUES($1,$2,$3,$4,$5,'/','GET',500,1,$6,$7,$8,$9)`, id, account.ID, app.ID, consumerA, tenantA, dep.ID, now, item.request, item.trace); err != nil {
			t.Fatal(err)
		}
	}
	for _, request := range []string{requestA, journalOnly} {
		if _, err := pool.Exec(ctx, `INSERT INTO request_id_journal(id,account_id,app_id,request_id,trace_id,received_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, uuid.NewString(), account.ID, app.ID, request, traceA, now, now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	insertTelemetry := func(trace, consumer, tenant string) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO request_telemetry(account_id,app_id,deployment_id,route,method,status,latency_ms,count,received_at,trace_id,consumer_id,platform_tenant_id) VALUES($1,$2,$3,'/','GET',500,1,1,$4,$5,$6,$7) RETURNING id::text`, account.ID, app.ID, dep.ID, now, trace, consumer, tenant).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	telemetryA := insertTelemetry(traceA, consumerA, tenantA)
	insertTelemetry(traceB, consumerB, tenantB)
	check := func(request, trace, wantConsumer, wantTenant, wantDebug string) {
		t.Helper()
		event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error", SourceKind: "exception", RequestID: request, TraceID: trace}
		response, err := s.RecordIssue(ctx, state.RecordIssueParams{Credential: state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: event, Fingerprint: strings.Repeat("a", 64), Title: "Error", PayloadHash: strings.Repeat("b", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now})
		if err != nil {
			t.Fatal(err)
		}
		detail, err := s.GetIssueDetail(ctx, app.ID, response.IssueID, now.Add(-time.Minute), now.Add(time.Minute), state.IssueDetailCursors{})
		if err != nil {
			t.Fatal(err)
		}
		for _, got := range detail.Events {
			if got.EventID == event.EventID {
				if got.VerifiedConsumerID != wantConsumer || got.VerifiedPlatformTenantID != wantTenant || got.DebugRequestID != wantDebug {
					t.Fatalf("correlation %s/%s: %+v", request, trace, got)
				}
				return
			}
		}
		t.Fatal("issue event missing")
	}
	check(requestA, traceB, "", "", "")
	check(journalOnly, traceB, "", "", "")
	check(auditOnly, traceB, "", "", "")
	check(untracedAudit, traceB, "", "", "")
	check(requestA, traceA, consumerA, tenantA, telemetryA)
}
