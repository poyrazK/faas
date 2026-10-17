package conformance

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// testConfigRevisionsAndGuards pins ADR-1451's bookkeeping on both stores:
// a baseline before the first write, complete revisions after each write,
// one watching guard per scope that later writes join, the sealed rows a
// guard retains (first capture wins, erased on close), the judging window's
// compare-and-set, supersession, abandonment, and revision validation.
func testConfigRevisionsAndGuards(t *testing.T, fx *Fixture) {
	t.Helper()
	store, ok := fx.Store.(state.ConfigRevisionStore)
	if !ok {
		t.Fatalf("%T does not implement state.ConfigRevisionStore", fx.Store)
	}
	acct, app, scope := fx.Account.ID, fx.App.ID, "default"
	clock := time.Now().UTC().Truncate(time.Second)
	tick := func() time.Time { clock = clock.Add(time.Second); return clock }
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(fx.Store.UpsertAppEnvInScope(fx.Ctx, acct, app, scope, "MODE", "old"))
	must(fx.Store.UpsertAppSecretWithClassInScope(fx.Ctx, acct, app, scope, "TOKEN", "kid-1", "hash-1", "persistent", []byte("sealed-1")))

	begin := func(guard bool, secretKey string) state.ConfigChangeTicket {
		t.Helper()
		ticket, err := store.BeginConfigChange(fx.Ctx, state.ConfigChangeStart{AccountID: acct, AppID: app, Scope: scope,
			Actor: "user:conformance", Guard: guard, SecretKey: secretKey, Now: tick()})
		must(err)
		return ticket
	}
	finish := func(source, key, guardID string, unrestorable bool) state.ConfigRevision {
		t.Helper()
		revision, err := store.FinishConfigChange(fx.Ctx, state.ConfigChangeFinish{AccountID: acct, AppID: app, Scope: scope,
			Actor: "user:conformance", Source: source, Key: key, GuardID: guardID, Unrestorable: unrestorable, Now: tick()})
		must(err)
		return revision
	}

	first := begin(true, "")
	if first.GuardID == "" || first.BaseRevision != 1 {
		t.Fatalf("first guarded change = %+v, want a guard on baseline revision 1", first)
	}
	baseline, err := store.GetConfigRevision(fx.Ctx, app, scope, 1)
	must(err)
	if baseline.Source != state.ConfigSourceBaseline || baseline.Variables["MODE"] != "old" ||
		baseline.Secrets["TOKEN"].ValueHash != "hash-1" || baseline.Secrets["TOKEN"].Version < 1 {
		t.Fatalf("baseline = %+v, want MODE=old and TOKEN fingerprinted", baseline)
	}
	must(fx.Store.UpsertAppEnvInScope(fx.Ctx, acct, app, scope, "MODE", "new"))
	second := finish(state.ConfigSourceEnvSet, "MODE", first.GuardID, false)
	if second.Revision != 2 || second.Variables["MODE"] != "new" || second.GuardID != first.GuardID {
		t.Fatalf("env.set revision = %+v, want revision 2 with MODE=new in the guard", second)
	}

	joined := begin(true, "TOKEN")
	if joined.GuardID != first.GuardID || joined.BaseRevision != 1 {
		t.Fatalf("second guarded change = %+v, want to join guard %s on base 1", joined, first.GuardID)
	}
	must(fx.Store.UpsertAppSecretWithClassInScope(fx.Ctx, acct, app, scope, "TOKEN", "kid-2", "hash-2", "persistent", []byte("sealed-2")))
	finish(state.ConfigSourceSecretSet, "TOKEN", first.GuardID, false)
	begin(true, "TOKEN") // a second write of the same key keeps the first capture
	must(fx.Store.UpsertAppSecretWithClassInScope(fx.Ctx, acct, app, scope, "TOKEN", "kid-3", "hash-3", "persistent", []byte("sealed-3")))
	finish(state.ConfigSourceSecretSet, "TOKEN", first.GuardID, false)
	begin(true, "ADDED")
	must(fx.Store.UpsertAppSecretWithClassInScope(fx.Ctx, acct, app, scope, "ADDED", "kid-a", "hash-a", "ephemeral", []byte("sealed-a")))
	head := finish(state.ConfigSourceSecretSet, "ADDED", first.GuardID, false)

	retained, err := store.ConfigGuardSecrets(fx.Ctx, first.GuardID)
	must(err)
	if len(retained) != 2 || retained[0].Key != "ADDED" || retained[0].Existed || retained[1].Key != "TOKEN" ||
		!retained[1].Existed || string(retained[1].Ciphertext) != "sealed-1" || retained[1].Kid != "kid-1" ||
		retained[1].ValueHash != "hash-1" || retained[1].SecretClass != "persistent" {
		t.Fatalf("retained secrets = %+v, want ADDED as new and TOKEN's first sealed row", retained)
	}
	guard, err := store.LatestConfigGuard(fx.Ctx, app, scope)
	must(err)
	if guard.ID != first.GuardID || guard.Status != state.ConfigGuardWatching || guard.BaseRevision != 1 ||
		guard.HeadRevision != head.Revision || !guard.Restorable || guard.WindowEndsAt != nil {
		t.Fatalf("watching guard = %+v, want base 1, head %d, restorable, no window", guard, head.Revision)
	}
	if opened, err := store.OpenConfigGuardWindow(fx.Ctx, guard.ID, guard.LastChangeAt.Add(-time.Second), tick()); err != nil || opened {
		t.Fatalf("window for a stale change opened=%v err=%v, want refused", opened, err)
	}
	ends := tick().Add(10 * time.Minute)
	if opened, err := store.OpenConfigGuardWindow(fx.Ctx, guard.ID, guard.LastChangeAt, ends); err != nil || !opened {
		t.Fatalf("window opened=%v err=%v, want opened", opened, err)
	}
	if opened, _ := store.OpenConfigGuardWindow(fx.Ctx, guard.ID, guard.LastChangeAt, ends.Add(time.Minute)); opened {
		t.Fatal("window opened twice")
	}
	watching, err := store.ListWatchingConfigGuards(fx.Ctx, 50)
	must(err)
	if !containsConfigGuard(watching, guard.ID) {
		t.Fatalf("watching guards %+v miss %s", watching, guard.ID)
	}

	decision := json.RawMessage(`{"rule":"restore_5xx"}`)
	if closed, err := store.CloseConfigGuard(fx.Ctx, guard.ID, state.ConfigGuardRestored, decision, head.Revision-1, tick()); err != nil || closed {
		t.Fatalf("close with a stale head closed=%v err=%v, want refused", closed, err)
	}
	if closed, err := store.CloseConfigGuard(fx.Ctx, guard.ID, state.ConfigGuardRestored, decision, head.Revision, tick()); err != nil || !closed {
		t.Fatalf("close closed=%v err=%v, want closed", closed, err)
	}
	if retained, err := store.ConfigGuardSecrets(fx.Ctx, guard.ID); err != nil || len(retained) != 0 {
		t.Fatalf("retained after close = %+v err=%v, want erased", retained, err)
	}
	guard, err = store.LatestConfigGuard(fx.Ctx, app, scope)
	must(err)
	if guard.Status != state.ConfigGuardRestored || guard.ClosedAt == nil || string(guard.Decision) == "" {
		t.Fatalf("closed guard = %+v, want restored with a decision", guard)
	}

	// A write outside the guard path is recorded as a baseline before the
	// next change.
	must(fx.Store.UpsertAppEnvInScope(fx.Ctx, acct, app, scope, "MODE", "out-of-band"))
	unguarded := begin(false, "")
	if unguarded.GuardID != "" || unguarded.BaseRevision != head.Revision+1 {
		t.Fatalf("unguarded change = %+v, want no guard on a new baseline %d", unguarded, head.Revision+1)
	}
	outOfBand, err := store.GetConfigRevision(fx.Ctx, app, scope, unguarded.BaseRevision)
	must(err)
	if outOfBand.Source != state.ConfigSourceBaseline || outOfBand.Variables["MODE"] != "out-of-band" {
		t.Fatalf("out-of-band baseline = %+v", outOfBand)
	}

	// An unguarded write supersedes a watching guard.
	superseded := begin(true, "")
	begin(false, "")
	if g, _ := store.LatestConfigGuard(fx.Ctx, app, scope); g.ID != superseded.GuardID || g.Status != state.ConfigGuardSuperseded {
		t.Fatalf("guard after an unguarded write = %+v, want %s superseded", g, superseded.GuardID)
	}
	// A guard no write landed in is abandoned; one with a change is kept.
	abandoned := begin(true, "")
	must(store.AbandonConfigChange(fx.Ctx, abandoned.GuardID, tick()))
	if g, _ := store.LatestConfigGuard(fx.Ctx, app, scope); g.ID != abandoned.GuardID || g.Status != state.ConfigGuardSuperseded {
		t.Fatalf("abandoned guard = %+v, want superseded", g)
	}
	kept := begin(true, "")
	must(fx.Store.DeleteAppSecretInScope(fx.Ctx, acct, app, scope, "TOKEN"))
	finish(state.ConfigSourceSecretDelete, "TOKEN", kept.GuardID, true)
	must(store.AbandonConfigChange(fx.Ctx, kept.GuardID, tick()))
	g, err := store.LatestConfigGuard(fx.Ctx, app, scope)
	must(err)
	if g.ID != kept.GuardID || g.Status != state.ConfigGuardWatching || g.Restorable {
		t.Fatalf("guard with an unrestorable change = %+v, want watching and not restorable", g)
	}

	revisions, err := store.ListConfigRevisions(fx.Ctx, app, scope, 3)
	must(err)
	if len(revisions) != 3 || revisions[0].Revision <= revisions[1].Revision || revisions[0].Source != state.ConfigSourceSecretDelete {
		t.Fatalf("newest revisions = %+v, want three, newest first", revisions)
	}
	if _, err := store.GetConfigRevision(fx.Ctx, app, scope, 9999); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing revision err = %v, want ErrNotFound", err)
	}
	for _, bad := range []state.ConfigChangeFinish{
		{AccountID: acct, AppID: app, Scope: scope, Source: "env.patch", Key: "MODE", Now: tick()},
		{AccountID: acct, AppID: app, Scope: scope, Source: state.ConfigSourceEnvSet, Now: tick()},
		{AccountID: acct, AppID: app, Scope: scope, Source: state.ConfigSourceRollback, Key: "MODE", RestoredRevision: 1, Now: tick()},
		{AccountID: acct, AppID: app, Scope: scope, Source: state.ConfigSourceRollback, Now: tick()},
		{AccountID: acct, AppID: app, Scope: "Bad Scope", Source: state.ConfigSourceEnvSet, Key: "MODE", Now: tick()},
	} {
		if _, err := store.FinishConfigChange(fx.Ctx, bad); !errors.Is(err, state.ErrInvalidArgument) {
			t.Errorf("FinishConfigChange(%+v) err = %v, want ErrInvalidArgument", bad, err)
		}
	}
}

func containsConfigGuard(guards []state.ConfigGuard, id string) bool {
	for _, g := range guards {
		if g.ID == id {
			return true
		}
	}
	return false
}
