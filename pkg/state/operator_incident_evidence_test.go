package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOperatorIncidentEvidenceStores(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			ctx := context.Background()
			if backend == "postgres" {
				store, ctx = pgStore(t)
			}
			account, err := store.CreateAccount(ctx, "obs-evidence@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			target, other := uuid.New(), uuid.New()
			targetID, otherID := target.String(), other.String()
			trace := "11111111111111111111111111111111"
			since := time.Now().UTC().Add(-time.Minute)
			for _, subject := range []string{targetID, otherID} {
				if err := store.AppendEventWithTrace(ctx, "system:schedd", "deployment.failed", &subject, []byte(`{"secret":"hidden"}`), &trace); err != nil {
					t.Fatal(err)
				}
			}
			for _, key := range []string{"target_id", "run_id", "deployment_id"} {
				payload := []byte(`{"` + key + `":"` + targetID + `","secret":"hidden"}`)
				if err := store.AppendEventWithTrace(ctx, "system:apid", "operator.action.cancel", &account.ID, payload, &trace); err != nil {
					t.Fatal(err)
				}
			}
			// An account match alone is insufficient, and a direct subject plus
			// a matching JSON reference must appear only once.
			for _, entry := range []struct{ subject, resource string }{{account.ID, otherID}, {targetID, targetID}} {
				payload := []byte(`{"run_id":"` + entry.resource + `"}`)
				if err := store.AppendEventWithTrace(ctx, "system:apid", "operator.action.cancel", &entry.subject, payload, &trace); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := store.AppendDeploymentAudit(ctx, state.DeploymentAudit{DeploymentID: target, Kind: state.DeployHealthProbeFailed, Actor: "system", At: time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}
			if _, err := store.InsertOperatorIntent(ctx, state.OperatorIntentKindForcePark, targetID, &account.ID, account.ID, "incident_test", nil, &trace); err != nil {
				t.Fatal(err)
			}
			rows, err := store.OperatorIncidentEvidence(ctx, targetID, since, 10)
			if err != nil || len(rows) != 7 {
				t.Fatalf("history=%+v err=%v", rows, err)
			}
			seen := map[string]bool{}
			for i, row := range rows {
				seen[row.Source] = true
				if i > 0 && row.At.After(rows[i-1].At) {
					t.Fatal("history not newest first")
				}
				if row.Source == "event" && row.TraceID != trace {
					t.Fatal("event lost trace ID")
				}
			}
			if !seen["event"] || !seen["deployment_audit"] || !seen["operator_intent"] {
				t.Fatalf("missing source: %v", seen)
			}
			rows, err = store.OperatorIncidentEvidence(ctx, targetID, since, 1)
			if err != nil || len(rows) != 1 {
				t.Fatalf("unbounded history=%+v err=%v", rows, err)
			}
			rows, err = store.OperatorIncidentEvidence(ctx, targetID, time.Now().Add(time.Hour), 10)
			if err != nil || len(rows) != 0 {
				t.Fatalf("time window ignored: %+v, %v", rows, err)
			}
		})
	}
}
