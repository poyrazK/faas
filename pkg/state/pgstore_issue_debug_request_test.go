package state_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgIssueDebuggerLinksOnlyUniqueJournalTrace(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	deployment := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC().Truncate(time.Microsecond)
	trace, requestID := strings.Repeat("a", 32), "public-"+uuid.NewString()
	var telemetryID string
	if err := pool.QueryRow(ctx, `INSERT INTO request_telemetry(account_id,app_id,deployment_id,route,method,status,latency_ms,count,received_at,trace_id) VALUES($1,$2,$3,'/','GET',500,1,1,$4,$5) RETURNING id::text`, account.ID, app.ID, deployment.ID, now, trace).Scan(&telemetryID); err != nil {
		t.Fatal(err)
	}
	insertJournal := func(id, trace string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO request_id_journal(id,account_id,app_id,request_id,trace_id,received_at,expires_at) VALUES($1,$2,$3,$4,NULLIF($5,''),$6,$7)`, uuid.NewString(), account.ID, app.ID, id, trace, now, now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	insertJournal(requestID, trace)
	check := func(id, eventTrace, want string) {
		t.Helper()
		event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error", RequestID: id, TraceID: eventTrace}
		response, err := s.RecordIssue(ctx, state.RecordIssueParams{Credential: state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: deployment.ID, Environment: "application"}, Event: event, Fingerprint: strings.Repeat("a", 64), Title: "Error", PayloadHash: strings.Repeat("b", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now})
		if err != nil {
			t.Fatal(err)
		}
		detail, err := s.GetIssueDetail(ctx, app.ID, response.IssueID, now.Add(-time.Minute), now.Add(time.Minute), state.IssueDetailCursors{})
		if err != nil {
			t.Fatal(err)
		}
		for _, occurrence := range detail.Events {
			if occurrence.EventID == event.EventID {
				if occurrence.DebugRequestID != want {
					t.Fatalf("request %q trace %q: debug ID %q, want %q", id, eventTrace, occurrence.DebugRequestID, want)
				}
				return
			}
		}
		t.Fatal("issue occurrence missing")
	}
	check(requestID, "", telemetryID)
	check("", trace, telemetryID)
	// A public ID equal to an unrelated telemetry row UUID is not a link.
	check(telemetryID, "", "")
	insertJournal(requestID, trace)
	check(requestID, "", "")
	if _, err := pool.Exec(ctx, `INSERT INTO request_telemetry(account_id,app_id,deployment_id,route,method,status,latency_ms,count,received_at,trace_id) VALUES($1,$2,$3,'/','GET',500,1,1,$4,$5)`, account.ID, app.ID, deployment.ID, now, trace); err != nil {
		t.Fatal(err)
	}
	check("", trace, "")
}
