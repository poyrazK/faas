//go:build !no_pg

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func issueHandoffHook(t *testing.T, e pgHandlerEnv, app state.App, events []string, enabled bool) state.AppWebhook {
	t.Helper()
	hook, err := e.store.CreateAppWebhook(t.Context(), state.AppWebhook{AccountID: e.acct.ID, AppID: app.ID, TargetURL: "https://example.com/handoff/" + uuid.NewString(), Enabled: enabled, EventFilter: events, SecretSealed: []byte("test-sealed")})
	if err != nil {
		t.Fatal(err)
	}
	return hook
}

func issueHandoffTelemetry(t *testing.T, e pgHandlerEnv, app state.App, dep state.Deployment, event api.IssueEvent, id string, count int, at time.Time) {
	t.Helper()
	_, err := e.pool.Exec(t.Context(), `INSERT INTO request_telemetry(id,account_id,app_id,deployment_id,route,method,status,latency_ms,cold_boot,trace_id,spans_summary,count,received_at)
	 VALUES($1,$2,$3,$4,'/exports','POST',500,73,true,$5,$6,$7,$8)`, id, e.acct.ID, app.ID, dep.ID, event.TraceID, `[{"span_id":"1234567890abcdef","name":"db alice@example.com","duration_nanos":35000000,"attributes":{"secret":"do not export"},"db_statement":"secret SQL"}]`, count, at)
	if err != nil {
		t.Fatal(err)
	}
}

func TestIssueHandoffEndToEndPostgres(t *testing.T) {
	e := setupPGHandler(t, api.PlanHobby)
	app := seedPGApp(t, e, "issue-handoff")
	dep := issueSeedDeployment(t, e, app, 5)
	commit := strings.Repeat("a", 40)
	if _, err := e.pool.Exec(t.Context(), `UPDATE deployments SET commit_sha=$1 WHERE id=$2`, commit, dep.ID); err != nil {
		t.Fatal(err)
	}
	token := issueCreateToken(t, e, app.Slug, dep)
	hook := issueHandoffHook(t, e, app, []string{"issue.handoff"}, true)
	wildcard := issueHandoffHook(t, e, app, nil, true)
	disabled := issueHandoffHook(t, e, app, []string{"issue.handoff"}, false)
	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "ExportError", Message: "failure alice@example.com", StackTrace: "Error: failure\n at export (/app/export.ts:12:3)", TraceID: strings.Repeat("b", 32), RequestID: uuid.NewString(), FingerprintOverride: "handoff-test"}
	issueHandoffTelemetry(t, e, app, dep, event, event.RequestID, 1, event.OccurredAt)
	issueSeedRequestAttribution(t, e, app, dep, event.RequestID)
	first := issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, event), http.StatusAccepted)
	if duplicate := issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, event), http.StatusAccepted); !duplicate.Duplicate {
		t.Fatal("retry not deduplicated")
	}
	// Change source data before recovery. The packet must stay at the first
	// occurrence and its commit-time impact and release identity.
	later := event
	later.EventID = uuid.NewString()
	later.Message = "later failure"
	later.OccurredAt = time.Now().UTC()
	issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, later), http.StatusAccepted)
	if _, err := e.pool.Exec(t.Context(), `UPDATE deployments SET commit_sha=$1 WHERE id=$2`, strings.Repeat("c", 40), dep.ID); err != nil {
		t.Fatal(err)
	}
	newHook := issueHandoffHook(t, e, app, []string{"issue.handoff"}, true)
	relay := e.store.(state.AppWebhookEventOutboxStore)
	if n, err := relay.DrainAppWebhookEventOutbox(t.Context(), 20); err != nil || n != 2 {
		t.Fatalf("outbox = %d %v", n, err)
	}
	deliveries, _, err := e.store.ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 50, "")
	if err != nil || len(deliveries) != 1 || deliveries[0].Event != state.AppWebhookEventIssueHandoff {
		t.Fatalf("handoff deliveries: %+v %v", deliveries, err)
	}
	raw := deliveries[0].Payload
	var p api.IssueHandoff
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if p.SchemaVersion != 1 || p.Type != "issue.handoff" || p.Transition.Action != "created" || p.Issue.ID != first.IssueID || p.Issue.EventCount != 1 || p.Sample == nil || p.Sample.EventID != event.EventID || p.Release == nil || p.Release.CommitSHA != commit || p.Impact.ObservedEvents != 1 || p.Impact.IdentifiedCustomers != 1 {
		t.Fatalf("incorrect snapshot: %+v", p)
	}
	if p.Request == nil || p.Request.ID != event.RequestID || p.Request.LatencyMS != 73 || len(p.Request.Spans) != 1 || p.Evidence.RequestPath == "" || p.Evidence.TracePath == "" || len(p.Gaps) != 0 {
		t.Fatalf("request evidence: %+v", p)
	}
	if strings.Contains(string(raw), "alice@example.com") || strings.Contains(string(raw), "secret SQL") || strings.Contains(string(raw), "do not export") {
		t.Fatalf("private data in packet: %s", raw)
	}
	for _, tc := range []struct {
		hook state.AppWebhook
		want int
	}{{wildcard, 1}, {disabled, 0}, {newHook, 0}} {
		d, _, err := e.store.ListAppWebhookDeliveries(t.Context(), app.ID, tc.hook.ID, 50, "")
		if err != nil || len(d) != tc.want {
			t.Fatalf("recipient snapshot: %+v %v", d, err)
		}
		for _, item := range d {
			if item.Event == state.AppWebhookEventIssueHandoff {
				t.Fatal("wildcard received detailed evidence")
			}
		}
	}
	if n, err := relay.DrainAppWebhookEventOutbox(t.Context(), 20); err != nil || n != 0 {
		t.Fatalf("duplicate recovery: %d %v", n, err)
	}
	d, _, err := e.store.ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 50, "")
	if err != nil || len(d) != 1 || !bytes.Equal(d[0].Payload, raw) {
		t.Fatal("recovery changed the packet")
	}
	base := "/v1/apps/" + app.Slug + "/issues/" + first.IssueID + "/actions"
	issueDecode[api.Issue](t, e.do(t, http.MethodPost, base, api.IssueActionRequest{Action: "resolve", FixedDeploymentID: dep.ID}, nil), http.StatusOK)
	regression := later
	regression.EventID = uuid.NewString()
	regression.OccurredAt = time.Now().UTC()
	if r := issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, regression), http.StatusAccepted); !r.Regressed {
		t.Fatal("expected regression")
	}
	issueDecode[api.Issue](t, e.do(t, http.MethodPost, base, api.IssueActionRequest{Action: "resolve", FixedDeploymentID: dep.ID}, nil), http.StatusOK)
	issueDecode[api.Issue](t, e.do(t, http.MethodPost, base, api.IssueActionRequest{Action: "reopen"}, nil), http.StatusOK)
	if _, err := relay.DrainAppWebhookEventOutbox(t.Context(), 20); err != nil {
		t.Fatal(err)
	}
	d, _, err = e.store.ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 50, "")
	if err != nil || len(d) != 3 {
		t.Fatalf("attention transitions: %+v %v", d, err)
	}
	actions := map[string]bool{}
	for _, item := range d {
		var p api.IssueHandoff
		if err := json.Unmarshal(item.Payload, &p); err != nil {
			t.Fatal(err)
		}
		actions[p.Transition.Action] = true
		if p.Transition.Action != "created" && (p.Sample == nil || p.Sample.EventID != regression.EventID) {
			t.Fatalf("incorrect transition sample: %+v", p)
		}
	}
	if !actions["created"] || !actions["regressed"] || !actions["reopened"] {
		t.Fatalf("actions: %v", actions)
	}
	// Evidence references still use the normal API authorization boundary.
	if w := e.do(t, http.MethodGet, p.Evidence.RequestPath, nil, map[string]string{"Authorization": "Bearer " + token.Token}); w.Code == http.StatusOK {
		t.Fatal("reporting token gained evidence read access")
	}
	// Manual reopen remains useful after detailed evidence expires; lifetime
	// issue counters must not turn into invented retained impact or samples.
	future := time.Now().UTC().AddDate(0, 0, api.PlanHobby.IssueLimits().RetentionDays+1)
	store := e.store.(state.IssueStore)
	if _, err := store.ActOnIssue(t.Context(), app.ID, first.IssueID, e.acct.ID, api.IssueActionRequest{Action: "resolve", FixedDeploymentID: dep.ID}, future); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActOnIssue(t.Context(), app.ID, first.IssueID, e.acct.ID, api.IssueActionRequest{Action: "reopen"}, future); err != nil {
		t.Fatal(err)
	}
	if _, err := relay.DrainAppWebhookEventOutbox(t.Context(), 20); err != nil {
		t.Fatal(err)
	}
	d, _, err = e.store.ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 50, "")
	if err != nil || len(d) != 4 {
		t.Fatalf("expired evidence handoff: %+v %v", d, err)
	}
	var expired bool
	for _, item := range d {
		var snapshot api.IssueHandoff
		if err := json.Unmarshal(item.Payload, &snapshot); err != nil {
			t.Fatal(err)
		}
		if !snapshot.GeneratedAt.Equal(future) {
			continue
		}
		expired = true
		if snapshot.Sample != nil || snapshot.Release != nil || snapshot.Request != nil || snapshot.Impact.ObservedEvents != 0 || !slices.Contains(snapshot.Gaps, "occurrence_unavailable_or_expired") {
			t.Fatalf("expired evidence exported: %+v", snapshot)
		}
	}
	if !expired {
		t.Fatal("missing expiry handoff")
	}
}

func TestIssueHandoffCorrelationGapsPostgres(t *testing.T) {
	e := setupPGHandler(t, api.PlanHobby)
	app := seedPGApp(t, e, "handoff-gaps")
	otherApp := seedPGApp(t, e, "handoff-other")
	dep := issueSeedDeployment(t, e, app, 6)
	otherDep := issueSeedDeployment(t, e, otherApp, 7)
	token := issueCreateToken(t, e, app.Slug, dep)
	hook := issueHandoffHook(t, e, app, []string{"issue.handoff"}, true)
	for _, tc := range []struct {
		name, gap string
		count     int
	}{{"ambiguous", "request_correlation_ambiguous", 1}, {"aggregate", "request_unavailable_or_expired", 3}, {"foreign", "request_unavailable_or_expired", 1}, {"expired", "request_unavailable_or_expired", 1}, {"conflict", "request_correlation_conflict", 1}, {"missing", "request_correlation_missing", 1}} {
		t.Run(tc.name, func(t *testing.T) {
			event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "Error", Message: tc.name, FingerprintOverride: tc.name, TraceID: strings.ReplaceAll(uuid.NewString(), "-", "")}
			a, d, at := app, dep, event.OccurredAt
			if tc.name == "foreign" {
				a, d = otherApp, otherDep
			}
			if tc.name == "expired" {
				at = at.AddDate(0, 0, -api.PlanHobby.DebugTelemetryRetentionDays()-1)
				event.OccurredAt = at
			}
			if tc.name == "missing" {
				event.TraceID = ""
			} else {
				id := uuid.NewString()
				issueHandoffTelemetry(t, e, a, d, event, id, tc.count, at)
				if tc.name == "ambiguous" {
					issueHandoffTelemetry(t, e, a, d, event, uuid.NewString(), 1, at)
				}
				if tc.name == "conflict" {
					event.RequestID = id
					event.TraceID = strings.Repeat("e", 32)
				}
			}
			issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, event), http.StatusAccepted)
			if _, err := e.store.(state.AppWebhookEventOutboxStore).DrainAppWebhookEventOutbox(t.Context(), 20); err != nil {
				t.Fatal(err)
			}
			items, _, err := e.store.ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 50, "")
			if err != nil {
				t.Fatal(err)
			}
			var found bool
			for _, item := range items {
				var p api.IssueHandoff
				if err := json.Unmarshal(item.Payload, &p); err != nil {
					t.Fatal(err)
				}
				if p.Sample == nil || p.Sample.EventID != event.EventID {
					continue
				}
				found = true
				if p.Request != nil || p.Evidence.RequestPath != "" || p.Evidence.TracePath != "" || !slices.Contains(p.Gaps, tc.gap) {
					t.Fatalf("unsafe correlation: %+v", p)
				}
			}
			if !found {
				t.Fatal("no handoff for occurrence")
			}
		})
	}
	// Late verified attribution must capture the occurrence that crossed the
	// threshold, even when a newer occurrence is already in the group.
	store := e.store.(state.IssueStore)
	if _, err := store.SetIssueImpactAlertPolicy(t.Context(), app.ID, e.acct.ID, 1, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	trigger := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "Error", Message: "impact", RequestID: uuid.NewString(), FingerprintOverride: "impact-handoff"}
	issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, trigger), http.StatusAccepted)
	newer := trigger
	newer.EventID = uuid.NewString()
	newer.RequestID = uuid.NewString()
	newer.OccurredAt = time.Now().UTC()
	issueDecode[api.IssueEventResponse](t, issueSend(t, e, app.Slug, token, newer), http.StatusAccepted)
	issueSeedRequestAttribution(t, e, app, dep, trigger.RequestID)
	maintainer := e.store.(interface {
		MaintainIssues(context.Context, time.Time) error
	})
	if err := maintainer.MaintainIssues(t.Context(), time.Now().UTC().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.(state.AppWebhookEventOutboxStore).DrainAppWebhookEventOutbox(t.Context(), 20); err != nil {
		t.Fatal(err)
	}
	items, _, err := e.store.ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 50, "")
	if err != nil {
		t.Fatal(err)
	}
	var crossings int
	for _, item := range items {
		var packet api.IssueHandoff
		if err := json.Unmarshal(item.Payload, &packet); err != nil {
			t.Fatal(err)
		}
		if packet.Transition.Action != "impact_threshold_reached" {
			continue
		}
		crossings++
		if packet.Sample == nil || packet.Sample.EventID != trigger.EventID || packet.Impact.IdentifiedCustomers != 1 || packet.Impact.ObservedEvents != 2 {
			t.Fatalf("late impact selected wrong evidence: %+v", packet)
		}
	}
	if crossings != 1 {
		t.Fatalf("late impact crossings=%d", crossings)
	}
}
