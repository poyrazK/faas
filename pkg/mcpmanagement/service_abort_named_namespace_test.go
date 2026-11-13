// adr: 20261104173000
package mcpmanagement

import (
	"testing"
	"time"
)

func TestServiceAbortNamedNamespaceCompatibility(t *testing.T) {
	m, id, _, _ := namedServiceAbortOutcomeFixture(t)
	store := m.opts.planStore
	var defaults, named serviceAbortOutcomeSnapshot
	if err := store.read(serviceAbortOutcomeNamespace+"named-abort", &defaults); err != nil {
		t.Fatal(err)
	}
	if err := store.read(namedServiceAbortOutcomeNamespace+"named-abort", &named); err != nil {
		t.Fatal(err)
	}
	if len(defaults.Items) != 0 || len(named.Items) != 1 || !serviceAbortReferenceMatches(m.actions[id], named.Items[id]) {
		t.Fatal("named reference entered the N-1 default-only namespace")
	}
	// An N-1 writer knows the default namespace and preserves opaque namespaces.
	if err := store.updateMany(map[string]any{serviceAbortOutcomeNamespace + "named-abort": serviceAbortOutcomeSnapshot{Version: 1, Items: map[string]serviceAbortOutcomeLookup{}}, "future/opaque": map[string]int{"version": 9}}); err != nil {
		t.Fatal(err)
	}
	opts := m.opts
	opts.AllowWrites = false
	_, restored, err := newHistoryTestManagementServer(m.client, opts)
	if err != nil {
		t.Fatal(err)
	}
	result, err := restored.getActionOutcome(t.Context(), getActionArgs{App: "alpha", Project: "shop", Environment: "staging", PlanID: id})
	if err != nil || result.(*guardedRead).value.(actionOutcome).Outcome != "verified_receipt" {
		t.Fatalf("named evidence lost across old-style rewrite: %v", err)
	}
	delete(restored.plans, id)
	entry := restored.actions[id]
	entry.RetainedUntil = time.Now().Add(-time.Minute)
	restored.actions[id] = entry
	if err := restored.persistPlans(); err != nil {
		t.Fatal(err)
	}
	named = serviceAbortOutcomeSnapshot{}
	if err := store.read(namedServiceAbortOutcomeNamespace+"named-abort", &named); err != nil || len(named.Items) != 0 {
		t.Fatalf("expired named lookup retained: %+v %v", named, err)
	}
	var opaque map[string]int
	if err := store.read("future/opaque", &opaque); err != nil || opaque["version"] != 9 {
		t.Fatal("unknown namespace lost")
	}
}

func TestServiceAbortNamedNamespaceValidation(t *testing.T) {
	for _, kind := range []string{"version", "binding", "wrong_bucket"} {
		t.Run(kind, func(t *testing.T) {
			m, id, _, _ := namedServiceAbortOutcomeFixture(t)
			store := m.opts.planStore
			var saved serviceAbortOutcomeSnapshot
			if err := store.read(namedServiceAbortOutcomeNamespace+"named-abort", &saved); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "version":
				saved.Version = 2
			case "binding":
				ref := saved.Items[id]
				ref.Binding = m.actions[id].PlanID
				saved.Items[id] = ref
			case "wrong_bucket":
				if err := store.update(serviceAbortOutcomeNamespace+"named-abort", saved); err != nil {
					t.Fatal(err)
				}
				saved.Items = map[string]serviceAbortOutcomeLookup{}
			}
			if err := store.update(namedServiceAbortOutcomeNamespace+"named-abort", saved); err != nil {
				t.Fatal(err)
			}
			if _, _, err := newHistoryTestManagementServer(m.client, m.opts); err == nil {
				t.Fatalf("accepted invalid named namespace: %s", kind)
			}
		})
	}
}

func TestServiceAbortNamedGrantPurge(t *testing.T) {
	m, _, _, _ := namedServiceAbortOutcomeFixture(t)
	store := m.opts.planStore
	var saved serviceAbortOutcomeSnapshot
	if err := store.read(namedServiceAbortOutcomeNamespace+"named-abort", &saved); err != nil {
		t.Fatal(err)
	}
	if err := store.update(namedServiceAbortOutcomeNamespace+"other", saved); err != nil {
		t.Fatal(err)
	}
	if err := store.update("oauth", oauthSnapshot{Grants: map[string]storedGrant{"other": {}}}); err != nil {
		t.Fatal(err)
	}
	var removed, retained serviceAbortOutcomeSnapshot
	if err := store.read(namedServiceAbortOutcomeNamespace+"named-abort", &removed); err != nil {
		t.Fatal(err)
	}
	if err := store.read(namedServiceAbortOutcomeNamespace+"other", &retained); err != nil {
		t.Fatal(err)
	}
	if len(removed.Items) != 0 || len(retained.Items) != 1 {
		t.Fatal("grant purge did not isolate named lookup namespaces")
	}
}
