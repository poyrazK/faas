package pgintegration_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// The shared SQLC admission and row scanners must retain both the captured
// environment and the scheduled-work classification introduced independently.
func TestEnvironmentGitOpsScopedInvocationFailureRulesAndClassification(t *testing.T) {
	stores(t, func(t *testing.T, store gitOpsTestStore) {
		source, _ := seed(t, store)
		app, err := store.CreateApp(t.Context(), state.App{AccountID: source.AccountID, ProjectID: source.ProjectID,
			Slug: "scheduled-api", Type: state.AppTypeApp})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{
			AccountID: source.AccountID, ProjectID: source.ProjectID, Slug: "staging"}); err != nil {
			t.Fatal(err)
		}
		rules := &workpolicy.FailureRules{Version: workpolicy.Version,
			Rules:            []workpolicy.FailureRule{{OutcomeCodes: []string{"invalid_record"}, Action: "fail_partition"}},
			UnmatchedFailure: "retry", UncertainOutcome: "hold"}
		completion, ok := store.(state.ClassifiedInvocationCompletionStore)
		if !ok {
			t.Fatal("store cannot persist classified invocation completion")
		}
		for _, scope := range []string{"production", "staging"} {
			for _, origin := range []state.InvocationSource{state.InvocationCron, state.InvocationDelayedTask} {
				t.Run(fmt.Sprintf("%s/%s", scope, origin), func(t *testing.T) {
					inv, err := store.EnqueueInvocation(t.Context(), state.Invocation{AccountID: source.AccountID,
						AppID: app.ID, Source: origin, DeploymentScope: scope, DueAt: time.Now(), FailureRules: rules})
					if err != nil {
						t.Fatal(err)
					}
					check := func(stage string, got state.Invocation) {
						t.Helper()
						if got.DeploymentScope != scope || !reflect.DeepEqual(got.FailureRules, rules) {
							t.Fatalf("%s lost scope or failure rules: %+v", stage, got)
						}
					}
					check("admission", inv)
					claimed, err := store.ClaimInvocation(t.Context(), inv.ID, "", 30)
					if err != nil {
						t.Fatal(err)
					}
					check("claim", claimed)
					decision := workpolicy.Evaluate(rules, workpolicy.Evidence{Succeeded: true, OutcomeCode: "processed"})
					if err := completion.CompleteInvocationWithWorkClassification(t.Context(), inv.ID,
						json.RawMessage(`{"accepted":true}`), decision, "processed"); err != nil {
						t.Fatal(err)
					}
					completed, err := store.InvocationByID(t.Context(), inv.ID)
					if err != nil {
						t.Fatal(err)
					}
					check("completion", completed)
					if completed.State != state.InvocationCompleted || completed.OutcomeCode != "processed" ||
						completed.WorkDecision == nil || *completed.WorkDecision != decision {
						t.Fatalf("completion lost classification: %+v", completed)
					}
				})
			}
		}
	})
}
