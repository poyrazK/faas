package state_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestPgIssueAttributionSettles pins the end of attribution retries: an
// occurrence that names no evidence is checked once, and one that names a
// request is retried until api.IssueAttributionRetryWindow has passed. A
// settled occurrence leaves the retry scan (its check time is infinite).
func TestPgIssueAttributionSettles(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	deployment := seedPgDeployment(t, s, ctx, app)
	store := state.IssueStore(s)
	now := time.Now().UTC().Truncate(time.Microsecond)
	hash := []byte(strings.Repeat("s", 32))
	limits := api.PlanPro.IssueLimits()
	if _, err := store.CreateIssueToken(ctx, account.ID, app.ID, api.CreateIssueIngestTokenRequest{
		DeploymentID: deployment.ID,
		Environment:  "application",
		Name:         "attribution-settle",
		ExpiresAt:    now.Add(time.Hour),
	}, hash, limits); err != nil {
		t.Fatalf("CreateIssueToken: %v", err)
	}
	credential, err := store.FindIssueToken(ctx, hash, now)
	if err != nil {
		t.Fatalf("FindIssueToken: %v", err)
	}
	record := func(requestID string) string {
		t.Helper()
		event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "SettleError", Message: "no customer evidence", SourceKind: "exception", RequestID: requestID}
		if _, err := store.RecordIssue(ctx, state.RecordIssueParams{
			Credential:      credential,
			Event:           event,
			Fingerprint:     strings.Repeat("d", 64),
			Title:           "SettleError: no customer evidence",
			PayloadHash:     strings.Repeat("e", 64),
			GroupingVersion: 1,
			Limits:          limits,
			Now:             now,
		}); err != nil {
			t.Fatalf("RecordIssue: %v", err)
		}
		return event.EventID
	}
	noEvidence := record("")
	awaitingRequest := record(uuid.NewString())
	settled := func(eventID string) bool {
		t.Helper()
		var infinite bool
		if err := pool.QueryRow(ctx, `select attribution_checked_at = 'infinity' from issue_events where app_id = $1 and event_id = $2`, app.ID, eventID).Scan(&infinite); err != nil {
			t.Fatalf("read attribution_checked_at: %v", err)
		}
		return infinite
	}

	if err := s.MaintainIssues(ctx, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("MaintainIssues inside the window: %v", err)
	}
	if !settled(noEvidence) {
		t.Fatal("an occurrence that names no evidence was left in the retry scan")
	}
	if settled(awaitingRequest) {
		t.Fatal("an occurrence waiting on request evidence settled inside the retry window")
	}

	if err := s.MaintainIssues(ctx, now.Add(api.IssueAttributionRetryWindow+2*time.Minute)); err != nil {
		t.Fatalf("MaintainIssues past the window: %v", err)
	}
	if !settled(awaitingRequest) {
		t.Fatal("an occurrence past the retry window stayed in the retry scan")
	}
}

func TestPgIssueAttributionBacklogGetsOneFurtherRetry(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	deployment := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC().Truncate(time.Microsecond)
	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error", RequestID: uuid.NewString()}
	in := state.RecordIssueParams{Credential: state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: deployment.ID, Environment: "application"}, Event: event, Fingerprint: strings.Repeat("a", 64), Title: "Error", PayloadHash: strings.Repeat("b", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now}
	if _, err := s.RecordIssue(ctx, in); err != nil {
		t.Fatal(err)
	}
	check := func(want bool) {
		t.Helper()
		var settled bool
		if err := pool.QueryRow(ctx, `SELECT attribution_checked_at='infinity' FROM issue_events WHERE app_id=$1 AND event_id=$2`, app.ID, event.EventID).Scan(&settled); err != nil || settled != want {
			t.Fatalf("settled=%v want %v err=%v", settled, want, err)
		}
	}
	first := now.Add(api.IssueAttributionRetryWindow + 2*time.Minute)
	if err := s.MaintainIssues(ctx, first); err != nil {
		t.Fatal(err)
	}
	check(false)
	if err := s.MaintainIssues(ctx, first.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	check(true)
}

func TestPgIssueCompoundAlertOnLateAttribution(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	deployment := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC().Truncate(time.Microsecond)
	zero := int64(0)
	rule := api.IssueCompoundAlertRule{Key: "late", Enabled: true, MinimumEvents: 1, MinimumCustomers: 1, WindowSeconds: 600, CooldownSeconds: 60}
	if _, err := s.SetIssueCompoundAlertPolicy(ctx, app.ID, account.ID, api.UpdateIssueCompoundAlertPolicyRequest{ExpectedRevision: &zero, Rules: []api.IssueCompoundAlertRule{rule}}); err != nil {
		t.Fatal(err)
	}
	requestID, consumerID, auditID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	in := state.RecordIssueParams{Credential: state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: deployment.ID, Environment: "application"}, Event: api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "LateError", SourceKind: "exception", RequestID: requestID}, Fingerprint: strings.Repeat("e", 64), Title: "LateError", PayloadHash: strings.Repeat("f", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now}
	result, err := s.RecordIssue(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO api_consumer_usage_events(event_id,account_id,app_id,consumer_key,window_start,request_count,error_count,billable_units) VALUES($1,$2,$3,$4,date_trunc('minute',now()),1,1,0)`, auditID, account.ID, app.ID, consumerID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO request_audit_events(event_id,account_id,app_id,consumer_key,route_template,method,http_status,latency_ms,deployment_id,occurred_at,request_id) VALUES($1,$2,$3,$4,'/','GET',500,10,$5,$6,$7)`, auditID, account.ID, app.ID, consumerID, deployment.ID, now, requestID); err != nil {
		t.Fatal(err)
	}
	for _, when := range []time.Time{now.Add(2 * time.Minute), now.Add(3 * time.Minute)} {
		if err = s.MaintainIssues(ctx, when); err != nil {
			t.Fatal(err)
		}
	}
	var alerts int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM issue_activity WHERE issue_id=$1 AND action='compound_threshold_reached'`, result.IssueID).Scan(&alerts); err != nil || alerts != 1 {
		t.Fatalf("late attribution alerts = %d, %v; want one", alerts, err)
	}
}

func TestPgIssueAttributionCompletesPartialIdentity(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	deployment := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC().Truncate(time.Microsecond)
	consumer, tenant, auditID, requestID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	trace := strings.Repeat("a", 32)
	if _, err := pool.Exec(ctx, `INSERT INTO platform_tenants(id,account_id,external_ref,name) VALUES($1,$2,$3,$3)`, tenant, account.ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO api_consumer_usage_events(event_id,account_id,app_id,consumer_key,window_start,request_count,error_count,billable_units) VALUES($1,$2,$3,$4,date_trunc('minute',now()),1,1,0)`, auditID, account.ID, app.ID, consumer); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO request_audit_events(event_id,account_id,app_id,consumer_key,route_template,method,http_status,latency_ms,deployment_id,occurred_at,request_id,trace_id) VALUES($1,$2,$3,$4,'/','GET',500,1,$5,$6,$7,$8)`, auditID, account.ID, app.ID, consumer, deployment.ID, now, requestID, trace); err != nil {
		t.Fatal(err)
	}
	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error", SourceKind: "exception", RequestID: requestID, TraceID: trace}
	in := state.RecordIssueParams{Credential: state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: deployment.ID, Environment: "application"}, Event: event, Fingerprint: strings.Repeat("a", 64), Title: "Error", PayloadHash: strings.Repeat("b", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now}
	if _, err := s.RecordIssue(ctx, in); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO request_telemetry(account_id,app_id,deployment_id,route,method,status,latency_ms,count,received_at,trace_id,consumer_id,platform_tenant_id) VALUES($1,$2,$3,'/','GET',500,1,1,$4,$5,$6,$7)`, account.ID, app.ID, deployment.ID, now, trace, consumer, tenant); err != nil {
		t.Fatal(err)
	}
	if err := s.MaintainIssues(ctx, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var gotConsumer, gotTenant *string
	if err := pool.QueryRow(ctx, `SELECT verified_consumer_id::text,verified_platform_tenant_id::text FROM issue_events WHERE app_id=$1 AND event_id=$2`, app.ID, event.EventID).Scan(&gotConsumer, &gotTenant); err != nil || gotConsumer == nil || gotTenant == nil || *gotConsumer != consumer || *gotTenant != tenant {
		t.Fatalf("partial identity not completed: consumer=%v tenant=%v err=%v", gotConsumer, gotTenant, err)
	}
	in.Event.EventID = uuid.NewString()
	in.Event.OccurredAt = now.Add(10 * time.Minute)
	in.Event.TraceID = ""
	in.Now = in.Event.OccurredAt
	if _, err := s.RecordIssue(ctx, in); err != nil {
		t.Fatal(err)
	}
	gotConsumer, gotTenant = nil, nil
	if err := pool.QueryRow(ctx, `SELECT verified_consumer_id::text,verified_platform_tenant_id::text FROM issue_events WHERE app_id=$1 AND event_id=$2`, app.ID, in.Event.EventID).Scan(&gotConsumer, &gotTenant); err != nil || gotConsumer != nil || gotTenant != nil {
		t.Fatalf("stale request ID matched: consumer=%v tenant=%v err=%v", gotConsumer, gotTenant, err)
	}
	anonymousAudit, anonymousRequest := uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO api_consumer_usage_events(event_id,account_id,app_id,consumer_key,window_start,request_count,error_count,billable_units) VALUES($1,$2,$3,'__anonymous__',date_trunc('minute',now()),1,1,0)`, anonymousAudit, account.ID, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO request_audit_events(event_id,account_id,app_id,consumer_key,route_template,method,http_status,latency_ms,deployment_id,occurred_at,request_id,trace_id) VALUES($1,$2,$3,'__anonymous__','/','GET',500,1,$4,$5,$6,$7)`, anonymousAudit, account.ID, app.ID, deployment.ID, now, anonymousRequest, trace); err != nil {
		t.Fatal(err)
	}
	in.Event.EventID, in.Event.RequestID, in.Event.TraceID, in.Event.OccurredAt, in.Now = uuid.NewString(), anonymousRequest, trace, now, now
	if _, err := s.RecordIssue(ctx, in); err != nil {
		t.Fatal(err)
	}
	gotConsumer, gotTenant = nil, nil
	if err := pool.QueryRow(ctx, `SELECT verified_consumer_id::text,verified_platform_tenant_id::text FROM issue_events WHERE app_id=$1 AND event_id=$2`, app.ID, in.Event.EventID).Scan(&gotConsumer, &gotTenant); err != nil || gotConsumer != nil || gotTenant != nil {
		t.Fatalf("conflicting anonymous audit was promoted: consumer=%v tenant=%v err=%v", gotConsumer, gotTenant, err)
	}
	conflictAudit, conflictRequest, otherConsumer := uuid.NewString(), uuid.NewString(), uuid.NewString()
	conflictTrace := strings.Repeat("b", 32)
	if _, err := pool.Exec(ctx, `INSERT INTO api_consumer_usage_events(event_id,account_id,app_id,consumer_key,window_start,request_count,error_count,billable_units) VALUES($1,$2,$3,$4,date_trunc('minute',now()),1,1,0)`, conflictAudit, account.ID, app.ID, consumer); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO request_audit_events(event_id,account_id,app_id,consumer_key,route_template,method,http_status,latency_ms,deployment_id,occurred_at,request_id,trace_id,platform_tenant_id) VALUES($1,$2,$3,$4,'/','GET',500,1,$5,$6,$7,$8,$9)`, conflictAudit, account.ID, app.ID, consumer, deployment.ID, now, conflictRequest, conflictTrace, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO request_telemetry(account_id,app_id,deployment_id,route,method,status,latency_ms,count,received_at,trace_id,consumer_id,platform_tenant_id) VALUES($1,$2,$3,'/','GET',500,1,1,$4,$5,$6,$7)`, account.ID, app.ID, deployment.ID, now, conflictTrace, otherConsumer, tenant); err != nil {
		t.Fatal(err)
	}
	in.Event.EventID, in.Event.RequestID, in.Event.TraceID = uuid.NewString(), conflictRequest, conflictTrace
	if _, err := s.RecordIssue(ctx, in); err != nil {
		t.Fatalf("conflicting evidence discarded occurrence: %v", err)
	}
	gotConsumer, gotTenant = nil, nil
	if err := pool.QueryRow(ctx, `SELECT verified_consumer_id::text,verified_platform_tenant_id::text FROM issue_events WHERE app_id=$1 AND event_id=$2`, app.ID, in.Event.EventID).Scan(&gotConsumer, &gotTenant); err != nil || gotConsumer != nil || gotTenant != nil {
		t.Fatalf("conflicting evidence was attributed: consumer=%v tenant=%v err=%v", gotConsumer, gotTenant, err)
	}
	lateAudit, lateRequest, lateTrace := uuid.NewString(), uuid.NewString(), strings.Repeat("c", 32)
	if _, err := pool.Exec(ctx, `INSERT INTO api_consumer_usage_events(event_id,account_id,app_id,consumer_key,window_start,request_count,error_count,billable_units) VALUES($1,$2,$3,$4,date_trunc('minute',now()),1,1,0)`, lateAudit, account.ID, app.ID, consumer); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO request_audit_events(event_id,account_id,app_id,consumer_key,route_template,method,http_status,latency_ms,deployment_id,occurred_at,request_id,trace_id) VALUES($1,$2,$3,$4,'/','GET',500,1,$5,$6,$7,$8)`, lateAudit, account.ID, app.ID, consumer, deployment.ID, now, lateRequest, lateTrace); err != nil {
		t.Fatal(err)
	}
	in.Event.EventID, in.Event.RequestID, in.Event.TraceID = uuid.NewString(), lateRequest, lateTrace
	if _, err := s.RecordIssue(ctx, in); err != nil {
		t.Fatal(err)
	}
	gotConsumer, gotTenant = nil, nil
	if err := pool.QueryRow(ctx, `SELECT verified_consumer_id::text,verified_platform_tenant_id::text FROM issue_events WHERE app_id=$1 AND event_id=$2`, app.ID, in.Event.EventID).Scan(&gotConsumer, &gotTenant); err != nil || gotConsumer == nil || *gotConsumer != consumer || gotTenant != nil {
		t.Fatalf("initial partial identity: consumer=%v tenant=%v err=%v", gotConsumer, gotTenant, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO request_telemetry(account_id,app_id,deployment_id,route,method,status,latency_ms,count,received_at,trace_id,consumer_id,platform_tenant_id) VALUES($1,$2,$3,'/','GET',500,1,1,$4,$5,$6,$7)`, account.ID, app.ID, deployment.ID, now, lateTrace, otherConsumer, tenant); err != nil {
		t.Fatal(err)
	}
	if err := s.MaintainIssues(ctx, now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	gotConsumer, gotTenant = nil, nil
	if err := pool.QueryRow(ctx, `SELECT verified_consumer_id::text,verified_platform_tenant_id::text FROM issue_events WHERE app_id=$1 AND event_id=$2`, app.ID, in.Event.EventID).Scan(&gotConsumer, &gotTenant); err != nil || gotConsumer != nil || gotTenant != nil {
		t.Fatalf("late conflict retained identity: consumer=%v tenant=%v err=%v", gotConsumer, gotTenant, err)
	}
	ambiguousID, ambiguousRequest := uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO api_consumer_usage_events(event_id,account_id,app_id,consumer_key,window_start,request_count,error_count,billable_units) VALUES($1,$2,$3,$4,date_trunc('minute',now()),1,1,0)`, ambiguousID, account.ID, app.ID, consumer); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO request_audit_events(event_id,account_id,app_id,consumer_key,route_template,method,http_status,latency_ms,deployment_id,occurred_at,request_id) VALUES($1,$2,$3,$4,'/','GET',500,1,$5,$6,$7)`, ambiguousID, account.ID, app.ID, consumer, deployment.ID, now, ambiguousRequest); err != nil {
		t.Fatal(err)
	}
	in.Event.EventID, in.Event.RequestID, in.Event.TraceID = uuid.NewString(), ambiguousRequest, ""
	if _, err := s.RecordIssue(ctx, in); err != nil {
		t.Fatal(err)
	}
	var before *string
	if err := pool.QueryRow(ctx, `SELECT verified_consumer_id::text FROM issue_events WHERE app_id=$1 AND event_id=$2`, app.ID, in.Event.EventID).Scan(&before); err != nil || before == nil {
		t.Fatalf("initial attribution: %v %v", before, err)
	}
	ambiguousID = uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO api_consumer_usage_events(event_id,account_id,app_id,consumer_key,window_start,request_count,error_count,billable_units) VALUES($1,$2,$3,$4,date_trunc('minute',now()),1,1,0)`, ambiguousID, account.ID, app.ID, otherConsumer); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO request_audit_events(event_id,account_id,app_id,consumer_key,route_template,method,http_status,latency_ms,deployment_id,occurred_at,request_id) VALUES($1,$2,$3,$4,'/','GET',500,1,$5,$6,$7)`, ambiguousID, account.ID, app.ID, otherConsumer, deployment.ID, now, ambiguousRequest); err != nil {
		t.Fatal(err)
	}
	if err := s.MaintainIssues(ctx, now.Add(6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	gotConsumer, gotTenant = nil, nil
	if err := pool.QueryRow(ctx, `SELECT verified_consumer_id::text,verified_platform_tenant_id::text FROM issue_events WHERE app_id=$1 AND event_id=$2`, app.ID, in.Event.EventID).Scan(&gotConsumer, &gotTenant); err != nil || gotConsumer != nil || gotTenant != nil {
		t.Fatalf("ambiguous request retained identity: consumer=%v tenant=%v err=%v", gotConsumer, gotTenant, err)
	}
}

func TestPgIssueLateAmbiguousTraceRevokesPartialAndCompleteIdentity(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	deployment := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC().Truncate(time.Microsecond)
	tenant := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO platform_tenants(id,account_id,external_ref,name) VALUES($1,$2,$3,$3)`, tenant, account.ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	for i, complete := range []bool{false, true} {
		trace, first, second := strings.Repeat(string(rune('a'+i)), 32), uuid.NewString(), uuid.NewString()
		insert := func(consumer string) {
			t.Helper()
			var tenantID any
			if complete {
				tenantID = tenant
			}
			if _, err := pool.Exec(ctx, `INSERT INTO request_telemetry(account_id,app_id,deployment_id,route,method,status,latency_ms,count,received_at,trace_id,consumer_id,platform_tenant_id) VALUES($1,$2,$3,'/','GET',500,1,1,$4,$5,$6,$7)`, account.ID, app.ID, deployment.ID, now, trace, consumer, tenantID); err != nil {
				t.Fatal(err)
			}
		}
		insert(first)
		event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "TraceError", SourceKind: "exception", TraceID: trace}
		in := state.RecordIssueParams{Credential: state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: deployment.ID, Environment: "application"}, Event: event, Fingerprint: strings.Repeat(string(rune('a'+i)), 64), Title: "TraceError", PayloadHash: strings.Repeat("b", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now}
		if _, err := s.RecordIssue(ctx, in); err != nil {
			t.Fatal(err)
		}
		insert(second)
		checkAt := now.Add(2 * time.Minute)
		if complete {
			if err := s.MaintainIssues(ctx, checkAt); err != nil {
				t.Fatal(err)
			}
			var before *string
			if err := pool.QueryRow(ctx, `SELECT verified_consumer_id::text FROM issue_events WHERE app_id=$1 AND event_id=$2`, app.ID, event.EventID).Scan(&before); err != nil || before == nil || *before != first {
				t.Fatalf("complete identity checked before its cadence: %v %v", before, err)
			}
			checkAt = now.Add(31 * time.Minute)
		}
		if err := s.MaintainIssues(ctx, checkAt); err != nil {
			t.Fatal(err)
		}
		var consumer, gotTenant *string
		if err := pool.QueryRow(ctx, `SELECT verified_consumer_id::text,verified_platform_tenant_id::text FROM issue_events WHERE app_id=$1 AND event_id=$2`, app.ID, event.EventID).Scan(&consumer, &gotTenant); err != nil || consumer != nil || gotTenant != nil {
			t.Fatalf("complete=%v: conflicting trace retained consumer=%v tenant=%v err=%v", complete, consumer, gotTenant, err)
		}
	}
}
