package conformance

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func testAtomicConfigRestore(t *testing.T, fx *Fixture) {
	history := fx.Store.(state.ConfigRevisionStore)
	restore := fx.Store.(state.ConfigRestoreStore)
	ctx, account, app := fx.Ctx, fx.Account.ID, fx.App.ID
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []string{"A", "B"} {
		must(fx.Store.UpsertAppEnvInScope(ctx, account, app, "default", key, "old"))
	}
	ticket, err := history.BeginConfigChange(ctx, state.ConfigChangeStart{AccountID: account, AppID: app, Scope: "default", Now: time.Now()})
	must(err)
	for _, key := range []string{"A", "B"} {
		must(fx.Store.UpsertAppEnvInScope(ctx, account, app, "default", key, "new"))
	}
	before, err := history.FinishConfigChange(ctx, state.ConfigChangeFinish{AccountID: account, AppID: app, Scope: "default", Source: state.ConfigSourceEnvSet, Key: "A", Now: time.Now()})
	must(err)
	variables, secrets, err := history.ConfigState(ctx, account, app, "default")
	must(err)
	r := state.ConfigRestore{Finish: state.ConfigChangeFinish{AccountID: account, AppID: app, Scope: "default", Source: state.ConfigSourceRollback, RestoredRevision: ticket.BaseRevision, Now: time.Now()},
		ExpectedVariables: variables, ExpectedSecrets: secrets,
		Variables: []state.ConfigVariableRestore{{Key: "A", Value: "old"}, {Key: "B", Value: "old"}},
		// Force an error after the variables changed, inside the transaction.
		Secrets: []state.ConfigGuardSecret{{Key: "MISSING"}},
	}
	if _, err := restore.ApplyConfigRestore(ctx, r); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("late failure = %v, want missing secret", err)
	}
	current, _, err := history.ConfigState(ctx, account, app, "default")
	must(err)
	if current["A"] != "new" || current["B"] != "new" {
		t.Fatalf("partial restore escaped: %v", current)
	}
	rows, err := history.ListConfigRevisions(ctx, app, "default", 1)
	must(err)
	if len(rows) != 1 || rows[0].Revision != before.Revision {
		t.Fatalf("failed restore recorded a revision: %+v", rows)
	}
	r.Secrets = nil
	completed, err := restore.ApplyConfigRestore(ctx, r)
	must(err)
	if completed.RestoredRevision != ticket.BaseRevision || completed.Variables["A"] != "old" || completed.Variables["B"] != "old" {
		t.Fatalf("restore = %+v", completed)
	}
	must(fx.Store.UpsertAppEnvInScope(ctx, account, app, "default", "B", "concurrent"))
	if _, err := restore.ApplyConfigRestore(ctx, r); !errors.Is(err, state.ErrConfigRestoreChanged) {
		t.Fatalf("stale restore = %v", err)
	}
	current, _, err = history.ConfigState(ctx, account, app, "default")
	must(err)
	if current["A"] != "old" || current["B"] != "concurrent" {
		t.Fatalf("concurrent edit overwritten: %v", current)
	}
}

func testConfigGuardCursor(t *testing.T, fx *Fixture) {
	history := fx.Store.(state.ConfigRevisionStore)
	for i := 0; i <= api.ConfigGuardBatchSize; i++ {
		scope := fmt.Sprintf("guard-%02d", i)
		_, err := history.BeginConfigChange(fx.Ctx, state.ConfigChangeStart{AccountID: fx.Account.ID, AppID: fx.App.ID, Scope: scope, Guard: true, Now: time.Now().Add(time.Duration(i) * time.Second)})
		if err != nil {
			t.Fatal(err)
		}
	}
	first, err := history.ListWatchingConfigGuards(fx.Ctx, api.ConfigGuardBatchSize)
	if err != nil || len(first) != api.ConfigGuardBatchSize {
		t.Fatalf("first page = %d, %v", len(first), err)
	}
	last := first[len(first)-1]
	ctx := state.WithConfigGuardCursor(fx.Ctx, state.ConfigGuardCursor{ChangedAt: last.LastChangeAt, ID: last.ID})
	next, err := history.ListWatchingConfigGuards(ctx, api.ConfigGuardBatchSize)
	if err != nil || len(next) != 1 || next[0].Scope != fmt.Sprintf("guard-%02d", api.ConfigGuardBatchSize) {
		t.Fatalf("next page = %+v, %v", next, err)
	}
}

func testAtomicGuardRestore(t *testing.T, fx *Fixture) {
	ctx, account, app := fx.Ctx, fx.Account.ID, fx.App.ID
	history := fx.Store.(state.ConfigRevisionStore)
	restore := fx.Store.(state.ConfigRestoreStore)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(fx.Store.UpsertAppEnvInScope(ctx, account, app, "default", "MODE", "old"))
	must(fx.Store.UpsertAppSecretWithClassInScope(ctx, account, app, "default", "TOKEN", "kid-1", "hash-1", "persistent", []byte("sealed-1")))
	ticket, err := history.BeginConfigChange(ctx, state.ConfigChangeStart{AccountID: account, AppID: app, Scope: "default", Guard: true, SecretKey: "TOKEN", Now: time.Now()})
	must(err)
	must(fx.Store.UpsertAppEnvInScope(ctx, account, app, "default", "MODE", "new"))
	must(fx.Store.UpsertAppSecretWithClassInScope(ctx, account, app, "default", "TOKEN", "kid-2", "hash-2", "persistent", []byte("sealed-2")))
	_, err = history.FinishConfigChange(ctx, state.ConfigChangeFinish{AccountID: account, AppID: app, Scope: "default", Source: state.ConfigSourceSecretSet, Key: "TOKEN", GuardID: ticket.GuardID, Now: time.Now()})
	must(err)
	guard, err := history.LatestConfigGuard(ctx, app, "default")
	must(err)
	variables, secrets, err := history.ConfigState(ctx, account, app, "default")
	must(err)
	retained, err := history.ConfigGuardSecrets(ctx, guard.ID)
	must(err)
	r := state.ConfigRestore{Finish: state.ConfigChangeFinish{AccountID: account, AppID: app, Scope: "default", Source: state.ConfigSourceGuardRestore, RestoredRevision: ticket.BaseRevision, Now: time.Now()},
		ExpectedVariables: variables, ExpectedSecrets: secrets, Variables: []state.ConfigVariableRestore{{Key: "MODE", Value: "old"}},
		Guard: &guard, Decision: api.ConfigGuardDecision{Rule: "fail_first_wake_5xx", Summary: "failed"},
		Secrets: append(append([]state.ConfigGuardSecret(nil), retained...), state.ConfigGuardSecret{Key: "MISSING"}),
	}
	if _, err := restore.ApplyConfigRestore(ctx, r); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("late secret error = %v", err)
	}
	currentVars, currentSecrets, err := history.ConfigState(ctx, account, app, "default")
	must(err)
	if currentVars["MODE"] != "new" || currentSecrets["TOKEN"] != secrets["TOKEN"] {
		t.Fatal("failed restore changed variables or secret versions")
	}
	currentGuard, err := history.LatestConfigGuard(ctx, app, "default")
	must(err)
	if currentGuard.Status != state.ConfigGuardWatching {
		t.Fatalf("failed restore closed guard: %s", currentGuard.Status)
	}
	r.Secrets = retained
	revision, err := restore.ApplyConfigRestore(ctx, r)
	must(err)
	currentGuard, err = history.LatestConfigGuard(ctx, app, "default")
	must(err)
	if currentGuard.Status != state.ConfigGuardRestored || revision.Variables["MODE"] != "old" || revision.Secrets["TOKEN"].ValueHash != "hash-1" {
		t.Fatalf("restore did not commit complete state: guard=%s", currentGuard.Status)
	}
	retained, err = history.ConfigGuardSecrets(ctx, guard.ID)
	must(err)
	if len(retained) != 0 {
		t.Fatal("completed restore retained old sealed secrets")
	}
	if _, err := restore.ApplyConfigRestore(ctx, r); !errors.Is(err, state.ErrConfigRestoreChanged) {
		t.Fatalf("duplicate guard restore = %v", err)
	}
}

func testAutomaticRollbackCurrent(t *testing.T, fx *Fixture) {
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	target := fx.Deployment
	must(fx.Store.MarkDeploymentLive(fx.Ctx, target.ID))
	deploy := func(digest string) state.Deployment {
		d, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{AppID: fx.App.ID, Kind: state.DeploymentKindImage, ImageDigest: digest, Status: state.DeployBuilding})
		must(err)
		must(fx.Store.MarkDeploymentLive(fx.Ctx, d.ID))
		return d
	}
	bad := deploy("sha256:bad")
	good := deploy("sha256:good")
	ctx := state.WithExpectedRollbackCurrent(fx.Ctx, bad.ID)
	if _, err := fx.Store.PrepareDeploymentRollback(ctx, fx.App.ID, target.ID); !errors.Is(err, state.ErrCheckedRollbackChanged) {
		t.Fatalf("stale automatic rollback = %v", err)
	}
	live, err := fx.Store.LiveDeploymentForScope(fx.Ctx, fx.App.ID, "default")
	must(err)
	if live.ID != good.ID {
		t.Fatalf("newer release replaced: %s", live.ID)
	}
}
