//go:build !no_pg

package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func contextMoveApp(t *testing.T, f overviewFixture, a state.App, node string) {
	t.Helper()
	if f.mem != nil {
		a.NodeID = node
		(*f.mem.Apps())[a.ID] = a
	} else if _, err := f.pool.Exec(f.ctx, "UPDATE apps SET node_id=$2 WHERE id=$1", a.ID, node); err != nil {
		t.Fatal(err)
	}
}

func contextEventTime(t *testing.T, f overviewFixture, kind string, at time.Time) {
	t.Helper()
	if f.mem != nil {
		for i, e := range *f.mem.Events() {
			if e.Kind == kind {
				e.At = at
				(*f.mem.Events())[i] = e
			}
		}
	} else if _, err := f.pool.Exec(f.ctx, "UPDATE events SET at=$2 WHERE kind=$1", kind, at); err != nil {
		t.Fatal(err)
	}
}

func TestOperatorContextNodeRelationshipsAndTimeline(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newOverviewFixture(t, backend)
			now := time.Now().UTC().Truncate(time.Microsecond).Add(time.Second)
			a := f.account(t, "context-a", now.Add(-time.Hour), nil, state.AccountActive)
			b := f.account(t, "context-b", now.Add(-time.Hour), nil, state.AccountActive)
			local, err := f.store.ComputeNodeByName(f.ctx, state.DefaultLocalNodeName)
			if err != nil {
				t.Fatal(err)
			}
			remote, err := f.store.CreateComputeNode(f.ctx, state.ComputeNode{Name: "context-remote", TargetURL: "tcp://127.0.0.1:50051", Active: true, VPCPUs: 4, VCPUBudget: 4, MemMB: 4096, MaxConcurrency: 10, AdmissionCeilingMB: 2048})
			if err != nil {
				t.Fatal(err)
			}
			appA, depA := f.workload(t, "context-a", a, true)
			contextMoveApp(t, f, appA, local.ID)
			appB, depB := f.workload(t, "context-b", b, false)
			warm, depWarm := f.workload(t, "context-warm", a, true)
			terminal, depTerminal := f.workload(t, "context-terminal", b, true)
			other, depOther := f.workload(t, "context-other", a, true)
			deleted, depDeleted := f.workload(t, "context-deleted", b, true)
			for _, app := range []state.App{appB, warm, terminal, other, deleted} {
				contextMoveApp(t, f, app, remote.ID)
			}
			for _, v := range []struct {
				app   state.App
				dep   state.Deployment
				state string
			}{{appB, depB, "running"}, {warm, depWarm, "warm"}, {terminal, depTerminal, "failed"}, {deleted, depDeleted, "running"}} {
				if _, err := f.store.CreateInstance(f.ctx, v.app.ID, v.dep.ID, v.state, 128, local.ID, ""); err != nil {
					t.Fatal(err)
				}
			}
			// Model an existing instance surviving app deletion. Runtime admission
			// correctly refuses new instances after the app has been deleted.
			f.deleteApp(t, deleted)
			if err := f.store.UpdateDeploymentStatus(f.ctx, depB.ID, state.DeployFailed, "private-error"); err != nil {
				t.Fatal(err)
			}
			trace := "11111111111111111111111111111111"
			for _, v := range []struct{ kind, subject, payload string }{
				{"app.context", appA.ID, `{"secret":"private-payload"}`},
				{"operator.action.context", appA.ID, `{"deployment_id":"` + depB.ID + `"}`},
				{"deployment.unrelated", depOther.ID, `{}`}, {"account.unrelated", a.ID, `{}`},
				{"event.old", depA.ID, `{}`}, {"event.future", depA.ID, `{}`},
				{"event.floor", depA.ID, `{}`}, {"event.ceiling", depA.ID, `{}`},
			} {
				if err := f.store.AppendEventWithTrace(f.ctx, "private-actor", v.kind, &v.subject, []byte(v.payload), &trace); err != nil {
					t.Fatal(err)
				}
			}
			contextEventTime(t, f, "event.old", now.Add(-api.ObsIncidentContextWindow-time.Microsecond))
			contextEventTime(t, f, "event.future", now.Add(time.Microsecond))
			contextEventTime(t, f, "event.floor", now.Add(-api.ObsIncidentContextWindow))
			contextEventTime(t, f, "event.ceiling", now)
			if _, err := f.store.AppendDeploymentAudit(f.ctx, state.DeploymentAudit{DeploymentID: uuid.MustParse(depA.ID), Kind: state.DeployHealthProbeFailed, Actor: "private-actor", At: now}); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.InsertOperatorIntent(f.ctx, state.OperatorIntentKindForceColdBoot, appA.ID, &a.ID, a.ID, "private-reason", []byte(`{"private":"payload"}`), &trace); err != nil {
				t.Fatal(err)
			}
			filter := state.OperatorIncidentContextFilter{Kind: "compute_node", ID: state.ContextCanonicalID(local.ID), Now: now, Until: now, Limit: 100, HistoryLimit: 100}
			out, err := f.store.OperatorIncidentContext(f.ctx, filter)
			if err != nil {
				t.Fatal(err)
			}
			if out.Scope.State != "known" || out.Counts == nil || out.Counts.Apps != 3 || out.Counts.Tenants != 2 || out.Counts.Nodes != 2 || out.Counts.Deployments != 3 || out.Counts.FailedResources != 1 {
				t.Fatalf("wrong exposure: %+v", out)
			}
			seen := map[string]bool{}
			for _, e := range out.Timeline {
				if seen[e.Entry.Source+e.Entry.ID] {
					t.Fatal("duplicate timeline entry")
				}
				seen[e.Entry.Source+e.Entry.ID] = true
				seen[e.Entry.Kind] = true
				if e.Entry.Kind == "operator.action.context" && (e.ResourceID != state.ContextCanonicalID(appA.ID) || e.Match != "subject") {
					t.Fatalf("direct subject not preferred: %+v", e)
				}
			}
			for _, kind := range []string{"app.context", "operator.action.context", "event.floor", "event.ceiling", "force_cold_boot", string(state.DeployHealthProbeFailed)} {
				if !seen[kind] {
					t.Fatalf("missing %s: %+v", kind, out.Timeline)
				}
			}
			for _, kind := range []string{"account.unrelated", "deployment.unrelated", "event.old", "event.future"} {
				if seen[kind] {
					t.Fatalf("false correlation: %s", kind)
				}
			}
			encoded, _ := json.Marshal(out)
			if strings.Contains(string(encoded), "private-") {
				t.Fatalf("raw evidence leaked: %s", encoded)
			}
			// Counts remain exact even on the last or empty resource page.
			filter.Limit, filter.HistoryLimit = 1, 2
			keys := map[string]bool{}
			before := ""
			for {
				filter.Before = before
				if before != "" {
					filter.ExpectedIncidentID = out.IncidentID
				}
				page, err := f.store.OperatorIncidentContext(f.ctx, filter)
				if err != nil {
					t.Fatal(err)
				}
				if *page.Counts != *out.Counts || len(page.Resources) > 1 || len(page.Timeline) > 2 {
					t.Fatalf("page lost totals or bounds: %+v", page)
				}
				if len(page.Resources) == 0 {
					break
				}
				r := page.Resources[0]
				key := r.Kind + "/" + r.ID
				if keys[key] {
					t.Fatal("duplicate resource")
				}
				keys[key] = true
				before = key
				if page.NextCursor == "" {
					break
				}
			}
			if len(keys) != len(out.Resources) {
				t.Fatalf("pagination lost resources: %d != %d", len(keys), len(out.Resources))
			}
			filter.Before = "tenant/ffffffff-ffff-ffff-ffff-ffffffffffff"
			empty, err := f.store.OperatorIncidentContext(f.ctx, filter)
			if err != nil || len(empty.Resources) != 0 || empty.Counts.Apps != 3 {
				t.Fatalf("empty tail=%+v err=%v", empty, err)
			}
			// App scope cannot expand through its shared owner node.
			filter = state.OperatorIncidentContextFilter{Kind: "deployment", ID: state.ContextCanonicalID(depA.ID), Now: now, Until: now, Limit: 100, HistoryLimit: 1}
			one, err := f.store.OperatorIncidentContext(f.ctx, filter)
			if err != nil || one.Counts.Apps != 1 || one.Counts.Tenants != 1 || !one.TimelineTruncated {
				t.Fatalf("app scope=%+v err=%v", one, err)
			}
			if _, err := f.store.OperatorIncidentContext(f.ctx, state.OperatorIncidentContextFilter{Kind: "deployment", ID: state.ContextCanonicalID(depDeleted.ID), Now: now, Until: now, Limit: 1, HistoryLimit: 1}); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("deleted incident=%v", err)
			}
		})
	}
}

func seedContextAlert(t *testing.T, f overviewFixture, at time.Time, index int, app, account, node string) state.OperatorAlertEpisode {
	t.Helper()
	rule, series := fmt.Sprintf("%064x", index+1), fmt.Sprintf("%064x", index+100)
	e := state.OperatorAlertEpisode{RuleKey: rule, SeriesID: series, Name: "FaasContextAlert", Severity: "critical", AppID: state.ContextCanonicalID(app), AccountID: state.ContextCanonicalID(account), NodeID: state.ContextCanonicalID(node), StartedAt: at.Add(-time.Minute)}
	e.ID = state.OperatorAlertEpisodeID(rule, series, e.StartedAt)
	err := f.store.RecordOperatorAlertSnapshot(f.ctx, state.OperatorAlertSnapshot{ObservedAt: at, Rules: []state.OperatorAlertRuleObservation{{Key: rule, EvaluatedAt: at, EpisodeIDs: []string{e.ID}}}, Episodes: []state.OperatorAlertEpisode{e}})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestOperatorContextAlertAttributionAndValidation(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newOverviewFixture(t, backend)
			now := time.Now().UTC().Truncate(time.Microsecond)
			a := f.account(t, "context-alert-a", now.Add(-time.Hour), nil, state.AccountActive)
			b := f.account(t, "context-alert-b", now.Add(-time.Hour), nil, state.AccountActive)
			app, _ := f.workload(t, "context-alert-app", a, true)
			_, _ = f.workload(t, "context-alert-other", b, true)
			node, err := f.store.ComputeNodeByName(f.ctx, state.DefaultLocalNodeName)
			if err != nil {
				t.Fatal(err)
			}
			contextMoveApp(t, f, app, node.ID)
			for i, v := range []struct {
				app, account, node, reason string
				apps                       int64
			}{
				{app.ID, a.ID, node.ID, "", 1}, {app.ID, b.ID, node.ID, "account_mismatch", 0},
				{uuid.NewString(), a.ID, node.ID, "referenced_app_missing", 0}, {"", a.ID, "", "no_workload_reference", 0},
				{"", "", "", "no_workload_reference", 0}, {"", "", uuid.NewString(), "referenced_node_missing", 0},
				{"", a.ID, node.ID, "", 1},
			} {
				e := seedContextAlert(t, f, now.Add(time.Duration(i)*time.Microsecond), i, v.app, v.account, v.node)
				filter := state.OperatorIncidentContextFilter{Kind: "platform_alert", ID: e.SeriesID, Now: now.Add(time.Second), Until: now.Add(time.Second), Limit: 100, HistoryLimit: 10}
				out, err := f.store.OperatorIncidentContext(f.ctx, filter)
				if err != nil {
					t.Fatal(err)
				}
				if out.IncidentID != "platform_alert/"+e.ID || out.Scope.Reason != v.reason {
					t.Fatalf("attribution=%+v want=%+v", out, v)
				}
				if v.reason != "" {
					if out.Counts != nil || len(out.Resources) != 0 || out.Scope.State != "unknown" {
						t.Fatalf("unknown became zero/known: %+v", out)
					}
				} else if out.Counts == nil || out.Counts.Apps != v.apps {
					t.Fatalf("scope expanded: %+v", out)
				}
				filter.ExpectedIncidentID = "platform_alert/" + strings.Repeat("f", 64)
				if _, err := f.store.OperatorIncidentContext(f.ctx, filter); !errors.Is(err, state.ErrInvalidArgument) {
					t.Fatalf("cross-episode cursor=%v", err)
				}
			}
			base := state.OperatorIncidentContextFilter{Kind: "compute_node", ID: state.ContextCanonicalID(node.ID), Now: now, Until: now, Limit: 1, HistoryLimit: 1}
			for _, field := range []string{"kind", "id", "limit", "history", "expired", "future", "cursor"} {
				bad := base
				switch field {
				case "kind":
					bad.Kind = "app"
				case "id":
					bad.ID = "not-uuid"
				case "limit":
					bad.Limit = 101
				case "history":
					bad.HistoryLimit = 0
				case "expired":
					bad.Until = now.Add(-api.ObsIncidentContextCursorTTL - time.Microsecond)
				case "future":
					bad.Until = now.Add(time.Hour)
				case "cursor":
					bad.Before = "app/no-uuid"
				}
				if _, err := f.store.OperatorIncidentContext(f.ctx, bad); !errors.Is(err, state.ErrInvalidArgument) {
					t.Fatalf("%s accepted: %v", field, err)
				}
			}
			cancelled, cancel := context.WithCancel(f.ctx)
			cancel()
			if _, err := f.store.OperatorIncidentContext(cancelled, base); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled read=%v", err)
			}
		})
	}
}

func TestOperatorContextJobIdentity(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newOverviewFixture(t, backend)
			now := time.Now().UTC()
			a := f.account(t, "context-jobs", now.Add(-time.Hour), nil, state.AccountActive)
			_, _ = f.workload(t, "unrelated-account-app", a, true)
			var roots []state.JobRun
			for _, name := range []string{"context-job", "unrelated-job"} {
				j, err := f.store.JobCreate(f.ctx, a.ID, name, "batch", "example.invalid/private:latest", []string{"/bin/true"}, 128, 60, 1, 0, nil)
				if err != nil {
					t.Fatal(err)
				}
				for n := 0; n < 2; n++ {
					run, _, err := f.store.JobRunCreate(f.ctx, j.ID, a.ID, "manual", nil, nil, nil, nil, 1)
					if err != nil {
						t.Fatal(err)
					}
					roots = append(roots, run)
				}
			}
			now = time.Now().UTC().Add(time.Second)
			node, err := f.store.ComputeNodeByName(f.ctx, state.DefaultLocalNodeName)
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range []struct {
				run    state.JobRun
				status string
			}{{roots[0], "running"}, {roots[2], "failed"}} {
				if _, err := f.store.CreateJobInstance(f.ctx, uuid.NewString(), v.run.JobID, v.run.ID, 0, v.status, 128, node.ID, ""); err != nil {
					t.Fatal(err)
				}
			}
			out, err := f.store.OperatorIncidentContext(f.ctx, state.OperatorIncidentContextFilter{Kind: "job_run", ID: state.ContextCanonicalID(roots[0].ID), Now: now, Until: now, Limit: 100, HistoryLimit: 100})
			if err != nil || out.Counts == nil || out.Counts.Apps != 0 || out.Counts.Jobs != 1 || out.Counts.JobRuns != 2 || out.Counts.Nodes != 1 || out.Counts.Tenants != 1 || out.Scope.JobID != state.ContextCanonicalID(roots[0].JobID) {
				t.Fatalf("job attribution=%+v err=%v", out, err)
			}
			nodeContext, err := f.store.OperatorIncidentContext(f.ctx, state.OperatorIncidentContextFilter{Kind: "compute_node", ID: state.ContextCanonicalID(node.ID), Now: now, Until: now, Limit: 100, HistoryLimit: 100})
			if err != nil || nodeContext.Counts == nil || nodeContext.Counts.Apps != 0 || nodeContext.Counts.Jobs != 1 || nodeContext.Counts.JobRuns != 2 || nodeContext.Counts.Tenants != 1 {
				t.Fatalf("job node exposure=%+v err=%v", nodeContext, err)
			}
		})
	}
}
