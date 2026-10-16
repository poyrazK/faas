package apidserver_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRuntimeConfigManagerDoesNotPromotePendingNonHotValue(t *testing.T) {
	previous := httpsec.HSTSEnabled
	t.Cleanup(func() { httpsec.SetHSTSEnabled(previous) })
	store := state.NewMemStore()
	row, err := store.UpsertRuntimeConfig(context.Background(), state.RuntimeConfigUpdate{
		Key: apidserver.RuntimeConfigHSTS, Scope: state.RuntimeConfigScopeGlobal,
		DesiredValue: json.RawMessage("false"), ApplyMode: state.RuntimeConfigApplyGraceful,
		Reason: "test pending state",
	})
	if err != nil {
		t.Fatalf("UpsertRuntimeConfig: %v", err)
	}
	m := apidserver.NewRuntimeConfigManager(func(string) string { return "" })
	if err := m.Reconcile(context.Background(), store); err != nil {
		t.Fatalf("reconcile pending: %v", err)
	}
	if got := m.Bool(apidserver.RuntimeConfigHSTS, true); !got {
		t.Fatalf("pending graceful value became effective: got %v", got)
	}
	if err := store.MarkRuntimeConfigApplied(context.Background(), row.Key, row.Scope, row.ScopeID, row.Version, row.DesiredValue, ""); err != nil {
		t.Fatalf("MarkRuntimeConfigApplied: %v", err)
	}
	if err := m.Reconcile(context.Background(), store); err != nil {
		t.Fatalf("reconcile applied: %v", err)
	}
	if got := m.Bool(apidserver.RuntimeConfigHSTS, true); got {
		t.Fatalf("applied graceful value did not become effective: got %v", got)
	}
}

func TestRuntimeConfigManagerValidation(t *testing.T) {
	m := apidserver.NewRuntimeConfigManager(func(string) string { return "" })
	if err := m.Apply(apidserver.RuntimeConfigDomainDoctorTTL, json.RawMessage(`0`)); err == nil {
		t.Fatal("expected TTL validation error")
	}
	if err := m.Apply(apidserver.RuntimeConfigDomainDoctor, json.RawMessage(`"true"`)); err == nil {
		t.Fatal("expected boolean validation error")
	}
}

func TestRuntimeConfigManagerReconcileMarksInvalidHotValueAndContinues(t *testing.T) {
	previous := httpsec.HSTSEnabled
	t.Cleanup(func() { httpsec.SetHSTSEnabled(previous) })
	store := state.NewMemStore()
	bad, err := store.UpsertRuntimeConfig(context.Background(), state.RuntimeConfigUpdate{
		Key: apidserver.RuntimeConfigDomainDoctorTTL, Scope: state.RuntimeConfigScopeGlobal,
		DesiredValue: json.RawMessage(`0`), ApplyMode: state.RuntimeConfigApplyHot,
		Reason: "invalid persisted value",
	})
	if err != nil {
		t.Fatalf("Upsert invalid value: %v", err)
	}
	good, err := store.UpsertRuntimeConfig(context.Background(), state.RuntimeConfigUpdate{
		Key: apidserver.RuntimeConfigHSTS, Scope: state.RuntimeConfigScopeGlobal,
		DesiredValue: json.RawMessage(`false`), ApplyMode: state.RuntimeConfigApplyHot,
		Reason: "valid persisted value",
	})
	if err != nil {
		t.Fatalf("Upsert valid value: %v", err)
	}

	m := apidserver.NewRuntimeConfigManager(func(string) string { return "" })
	if err := m.Reconcile(context.Background(), store); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	bad, err = store.GetRuntimeConfig(context.Background(), bad.Key, bad.Scope, bad.ScopeID)
	if err != nil {
		t.Fatalf("Get invalid row: %v", err)
	}
	if bad.Status != state.RuntimeConfigFailed || bad.LastError == "" {
		t.Fatalf("invalid row = status %q error %q, want failed with an error", bad.Status, bad.LastError)
	}
	good, err = store.GetRuntimeConfig(context.Background(), good.Key, good.Scope, good.ScopeID)
	if err != nil {
		t.Fatalf("Get valid row: %v", err)
	}
	if good.Status != state.RuntimeConfigApplied {
		t.Fatalf("valid row status = %q, want applied", good.Status)
	}
	if got := m.Bool(apidserver.RuntimeConfigHSTS, true); got {
		t.Fatal("valid hot value was not applied after an invalid row")
	}
}

func TestRuntimeConfigManagerVersionOrdering(t *testing.T) {
	m := apidserver.NewRuntimeConfigManager(func(string) string { return "" })
	if applied, err := m.ApplyVersion(apidserver.RuntimeConfigDataPlacement, json.RawMessage(`false`), 2); err != nil || !applied {
		t.Fatalf("apply version 2 = applied %v, err %v; want applied", applied, err)
	}
	if applied, err := m.ApplyVersion(apidserver.RuntimeConfigDataPlacement, json.RawMessage(`true`), 1); err != nil || applied {
		t.Fatalf("apply stale version 1 = applied %v, err %v; want ignored", applied, err)
	}
	if got := m.Bool(apidserver.RuntimeConfigDataPlacement, true); got {
		t.Fatal("stale version overwrote the newer value")
	}
	if applied, err := m.ApplyVersion(apidserver.RuntimeConfigDataPlacement, json.RawMessage(`true`), 3); err != nil || !applied {
		t.Fatalf("apply version 3 = applied %v, err %v; want applied", applied, err)
	}
	if got := m.Bool(apidserver.RuntimeConfigDataPlacement, false); !got {
		t.Fatal("newer version did not become effective")
	}
}

func TestRuntimeConfigManagerConcurrentVersionedApply(t *testing.T) {
	m := apidserver.NewRuntimeConfigManager(func(string) string { return "" })
	const updates = 100
	var wg sync.WaitGroup
	wg.Add(updates)
	for version := int64(1); version <= updates; version++ {
		version := version
		go func() {
			defer wg.Done()
			value := json.RawMessage(`false`)
			if version%2 == 0 {
				value = json.RawMessage(`true`)
			}
			if _, err := m.ApplyVersion(apidserver.RuntimeConfigDataPlacement, value, version); err != nil {
				t.Errorf("apply version %d: %v", version, err)
			}
		}()
	}
	wg.Wait()
	if got := m.Bool(apidserver.RuntimeConfigDataPlacement, false); !got {
		t.Fatal("the highest completed version did not remain effective")
	}
	(*m.Mu()).RLock()
	gotVersion := (*m.Versions())[apidserver.RuntimeConfigDataPlacement]
	(*m.Mu()).RUnlock()
	if gotVersion != updates {
		t.Fatalf("effective version = %d, want %d", gotVersion, updates)
	}
}

func TestRuntimeConfigManagerBootstrapCannotOverrideDurableValue(t *testing.T) {
	m := apidserver.NewRuntimeConfigManager(func(string) string { return "" })
	if applied, err := m.ApplyVersion(apidserver.RuntimeConfigDataPlacement, json.RawMessage(`true`), 4); err != nil || !applied {
		t.Fatalf("apply durable value = applied %v, err %v; want applied", applied, err)
	}
	if err := m.Apply(apidserver.RuntimeConfigDataPlacement, json.RawMessage(`false`)); err != nil {
		t.Fatalf("apply bootstrap fallback: %v", err)
	}
	if got := m.Bool(apidserver.RuntimeConfigDataPlacement, false); !got {
		t.Fatal("bootstrap fallback overwrote the durable value")
	}
}

func TestRuntimeConfigManagerReconcileSurvivesRestart(t *testing.T) {
	store := state.NewMemStore()
	row, err := store.UpsertRuntimeConfig(context.Background(), state.RuntimeConfigUpdate{
		Key: apidserver.RuntimeConfigDataPlacement, Scope: state.RuntimeConfigScopeGlobal,
		DesiredValue: json.RawMessage(`true`), ApplyMode: state.RuntimeConfigApplyHot,
		Reason: "restart persistence",
	})
	if err != nil {
		t.Fatalf("UpsertRuntimeConfig: %v", err)
	}

	first := apidserver.NewRuntimeConfigManager(func(string) string { return "" })
	if err := first.Reconcile(context.Background(), store); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if got := first.Bool(apidserver.RuntimeConfigDataPlacement, false); !got {
		t.Fatal("first manager did not apply durable value")
	}
	row, err = store.GetRuntimeConfig(context.Background(), row.Key, row.Scope, row.ScopeID)
	if err != nil {
		t.Fatalf("GetRuntimeConfig: %v", err)
	}
	if row.Status != state.RuntimeConfigApplied {
		t.Fatalf("stored status = %q, want applied", row.Status)
	}

	second := apidserver.NewRuntimeConfigManager(func(string) string { return "" })
	if err := second.Reconcile(context.Background(), store); err != nil {
		t.Fatalf("restart reconcile: %v", err)
	}
	if got := second.Bool(apidserver.RuntimeConfigDataPlacement, false); !got {
		t.Fatal("durable value was not restored after manager restart")
	}
}
