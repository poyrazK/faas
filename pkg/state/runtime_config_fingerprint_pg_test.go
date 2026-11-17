package state_test

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// The runtime-config watchers skip their five-second full read while this
// fingerprint holds, so every write that can change what they apply must
// move it: a new row, a new desired value, and an apply acknowledgement.
func TestRuntimeConfigFingerprintMovesOnEveryWrite(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	s := state.NewPgStore(pool)
	fingerprint := func() string {
		t.Helper()
		got, err := s.RuntimeConfigFingerprint(t.Context(), state.RuntimeConfigScopeGlobal)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	seen := map[string]string{"empty": fingerprint()}
	step := func(name string) {
		t.Helper()
		got := fingerprint()
		for prior, value := range seen {
			if value == got {
				t.Fatalf("fingerprint after %s equals the one after %s: %q", name, prior, got)
			}
		}
		if again := fingerprint(); again != got {
			t.Fatalf("fingerprint after %s is unstable: %q then %q", name, got, again)
		}
		seen[name] = got
	}

	key := "fingerprint_test_flag"
	row, err := s.UpsertRuntimeConfig(t.Context(), state.RuntimeConfigUpdate{Key: key, Scope: state.RuntimeConfigScopeGlobal,
		DesiredValue: json.RawMessage(`true`), ApplyMode: state.RuntimeConfigApplyHot})
	if err != nil {
		t.Fatal(err)
	}
	step("insert")
	if err := s.MarkRuntimeConfigApplied(t.Context(), row.Key, row.Scope, row.ScopeID, row.Version, row.DesiredValue, ""); err != nil {
		t.Fatal(err)
	}
	step("apply")
	version := row.Version
	if _, err := s.UpsertRuntimeConfig(t.Context(), state.RuntimeConfigUpdate{Key: key, Scope: state.RuntimeConfigScopeGlobal,
		DesiredValue: json.RawMessage(`false`), ApplyMode: state.RuntimeConfigApplyHot, ExpectedVersion: &version}); err != nil {
		t.Fatal(err)
	}
	step("update")
}
