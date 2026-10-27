//go:build !no_pg

package e2e_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// releaseEdgeFeatures turns the edge feature platform flags
// (ADR-20261023113721) on for every account the way an operator does, and
// waits until apid and gatewayd-internal have applied them: challenge rules,
// WAF warn and block, and penalties are off until launch.
func releaseEdgeFeatures(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	store := state.NewPgStore(pool)
	value, err := json.Marshal(platformflags.Definition{Enabled: true, Default: true})
	if err != nil {
		t.Fatal(err)
	}
	versions := map[string]int64{}
	for _, flag := range platformflags.EdgeFeatures {
		key := platformflags.ConfigKey(flag)
		row, err := store.UpsertRuntimeConfig(t.Context(), state.RuntimeConfigUpdate{Key: key, Scope: state.RuntimeConfigScopeGlobal, DesiredValue: value,
			ApplyMode: state.RuntimeConfigApplyHot, ActorID: uuid.NewString(), Reason: "edge feature e2e"})
		if err != nil {
			t.Fatalf("release %s: %v", flag, err)
		}
		versions[key] = row.Version
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		applied := map[string]map[string]bool{"apid": {}, "gatewayd-internal": {}}
		keys := make([]string, 0, len(versions))
		for key := range versions {
			keys = append(keys, key)
		}
		receipts, err := store.ListRuntimeConfigApplyReceipts(t.Context(), keys)
		for _, r := range receipts {
			if seen, ok := applied[r.Daemon]; ok && err == nil && r.Status == state.RuntimeConfigReceiptApplied && r.Version >= versions[r.Key] {
				seen[r.Key] = true
			}
		}
		if len(applied["apid"]) == len(versions) && len(applied["gatewayd-internal"]) == len(versions) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("edge feature flags not applied within 30s: %v (err %v)", applied, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
