// adr: 567 — report claims select only report sources and mode changes revoke authority.
package pgintegration_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentGitOpsModeClaimsDoNotStarveEnforcement(t *testing.T) {
	stores(t, func(t *testing.T, store gitOpsTestStore) {
		claims := store.(state.EnvironmentGitOpsModeClaimStore)
		enforce, desired := seedMode(t, store, "enforce")
		var err error
		enforce, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(enforce, desired, strings.Repeat("a", 40)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: enforce.AccountID, ProjectID: enforce.ProjectID, Slug: "staging"}); err != nil {
			t.Fatal(err)
		}
		spec := enforce.Spec
		spec.Mode = "report"
		report, err := store.CreateEnvironmentGitSource(t.Context(), enforce.AccountID, enforce.ProjectID, "staging", spec)
		if err != nil {
			t.Fatal(err)
		}
		definition := desired.Definition
		definition.Environment = "staging"
		staging, err := environmentsync.Compile(definition)
		if err != nil {
			t.Fatal(err)
		}
		report, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(report, staging, strings.Repeat("b", 40)))
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC().Add(time.Second)
		for _, invalid := range []string{"", "all", "Report"} {
			if _, err := claims.ClaimEnvironmentGitOpsMode(t.Context(), invalid, "invalid", now, time.Minute); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("invalid selector %q: %v", invalid, err)
			}
		}
		observed, err := claims.ClaimEnvironmentGitOpsMode(t.Context(), "report", "report-worker", now, time.Minute)
		if err != nil || observed.Source.ID != report.ID || observed.Source.Spec.Mode != "report" {
			t.Fatalf("older enforce source intercepted report claim: %+v %v", observed, err)
		}
		if _, err := claims.ClaimEnvironmentGitOpsMode(t.Context(), "report", "idle-report-worker", now, time.Minute); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("report claimed an enforce source: %v", err)
		}
		executor, err := claims.ClaimEnvironmentGitOpsMode(t.Context(), "enforce", "enforce-worker", now, time.Minute)
		if err != nil || executor.Source.ID != enforce.ID || executor.AttemptCount != 1 {
			t.Fatalf("report consumed enforce lease or attempt: %+v %v", executor, err)
		}
		mode := "enforce"
		report, err = store.(state.EnvironmentGitOpsControlStore).UpdateEnvironmentGitSource(t.Context(), report.AccountID, report.ID,
			state.EnvironmentGitSourceUpdate{ExpectedGeneration: report.Generation, Mode: mode})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.RenewEnvironmentGitOps(t.Context(), observed, now, time.Minute); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("report lease survived mode change: %v", err)
		}
		if err := store.FinishEnvironmentGitOps(t.Context(), observed, "drifted", json.RawMessage(`{}`), json.RawMessage(`[]`), "", now, now.Add(time.Minute)); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("old report published after mode change: %v", err)
		}
		if _, err := claims.ClaimEnvironmentGitOpsMode(t.Context(), "report", "report-after-change", now, time.Minute); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("changed source remained report eligible: %v", err)
		}
		fresh, err := claims.ClaimEnvironmentGitOpsMode(t.Context(), "enforce", "new-enforce-worker", now, time.Minute)
		if err != nil || fresh.Source.ID != report.ID || fresh.Source.Generation != report.Generation {
			t.Fatalf("new mode waited for superseded report expiry: %+v %v", fresh, err)
		}
	})
}
