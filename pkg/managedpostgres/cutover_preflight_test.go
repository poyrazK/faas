package managedpostgres

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func verifiedPreflightFixture(t *testing.T) (*CutoverService, *MemoryStore, Cutover, *time.Time, *bool) {
	t.Helper()
	s, store, c, now, enabled := preparedCutover(t)
	s.verifier = &cutoverProbe{}
	if _, err := s.Verify(t.Context(), c.AccountID, c.ID); err != nil {
		t.Fatal(err)
	}
	c, err := s.Reconcile(t.Context(), c.AccountID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, store, c, now, enabled
}

func TestCutoverPreflightRecoversWithoutProviderOrPublication(t *testing.T) {
	s, store, c, now, enabled := verifiedPreflightFixture(t)
	*enabled = false
	p, err := s.RequestPreflight(t.Context(), c.AccountID, c.ID)
	if err != nil || p.State != "pending" || p.Revision != 1 {
		t.Fatal(p, err)
	}
	again, err := s.RequestPreflight(t.Context(), c.AccountID, c.ID)
	if err != nil || again.Revision != p.Revision {
		t.Fatal("pending retry changed revision", again, err)
	}
	// Recovery uses only the durable request, even with the provider gate closed.
	s.verifier = &cutoverProbe{run: func(context.Context, Binding, SealedCredential, Database) error {
		t.Fatal("preflight called provider")
		return nil
	}}
	if err = s.Sweep(t.Context(), 10); err != nil {
		t.Fatal(err)
	}
	p, err = s.Preflight(t.Context(), c.AccountID, c.ID)
	if err != nil || p.State != "blocked" || !p.VerificationFresh || !p.SourceMatches || !p.TargetMatches || !p.BindingsMatch || len(p.BindingIDs) != 2 || !slices.Contains(p.Blockers, "activation_disabled") || !slices.Contains(p.Blockers, "runtime_safety_unproven") {
		t.Fatal(p, err)
	}
	for _, m := range c.Credentials {
		b, _ := store.GetBinding(t.Context(), c.AccountID, m.SourceBindingID)
		if b.DatabaseID != c.Source.ID || b.CredentialGeneration != m.SourceCredentialGeneration {
			t.Fatal("preflight published target", b)
		}
	}
	p.Blockers[0] = "mutated"
	p.RuntimeDrain.Blockers[0] = "mutated"
	p, err = s.Preflight(t.Context(), c.AccountID, c.ID)
	if err != nil || !slices.Contains(p.Blockers, "activation_disabled") || p.RuntimeDrain.Blockers[0] != "runtime_observation_unavailable" {
		t.Fatal("returned mutable report", p, err)
	}
	*now = now.Add(CutoverVerificationMaxAge + time.Second)
	p, err = s.RequestPreflight(t.Context(), c.AccountID, c.ID)
	if err != nil || p.Revision != 2 || p.VerificationFresh || len(p.Blockers) != 0 {
		t.Fatal("refresh retained evidence", p, err)
	}
	if err = s.Sweep(t.Context(), 10); err != nil {
		t.Fatal(err)
	}
	p, err = s.Preflight(t.Context(), c.AccountID, c.ID)
	if err != nil || p.VerificationFresh || !slices.Contains(p.Blockers, "verification_required") {
		t.Fatal("expired verification passed", p, err)
	}
}

func TestCutoverPreflightRejectsChangedInputs(t *testing.T) {
	for _, mode := range []string{"source", "target", "lineage", "target_binding", "generation", "binding_access", "extra_binding", "missing_binding", "envelope", "future_verification"} {
		t.Run(mode, func(t *testing.T) {
			s, store, c, now, _ := verifiedPreflightFixture(t)
			m := c.Credentials[0]
			switch mode {
			case "source":
				d := store.databases[c.Source.ID]
				d.ProviderResourceID = "changed"
				store.databases[d.ID] = d
			case "target":
				d := store.databases[c.Target.ID]
				d.DesiredGeneration++
				store.databases[d.ID] = d
			case "lineage":
				d := store.databases[c.Target.ID]
				d.RestoreSourceResourceID = "other"
				store.databases[d.ID] = d
			case "target_binding":
				store.bindings["extra"] = Binding{DatabaseID: c.Target.ID, State: BindingStateReady}
			case "generation":
				b := store.bindings[m.SourceBindingID]
				b.CredentialGeneration++
				store.bindings[b.ID] = b
			case "binding_access":
				b := store.bindings[m.SourceBindingID]
				b.Access = CredentialReadOnly
				store.bindings[b.ID] = b
			case "extra_binding":
				b := store.bindings[m.SourceBindingID]
				b.ID = "extra"
				store.bindings[b.ID] = b
			case "missing_binding":
				delete(store.bindings, m.SourceBindingID)
			case "envelope":
				c.Credentials[0].Sealed.Ciphertext = nil
				store.cutovers[c.ID] = c
			case "future_verification":
				c.Credentials[0].VerifiedAt = now.Add(time.Second)
				store.cutovers[c.ID] = c
			}
			if _, err := s.RequestPreflight(t.Context(), c.AccountID, c.ID); err != nil {
				t.Fatal(err)
			}
			p, err := store.EvaluateCutoverPreflight(t.Context(), c.AccountID, c.ID, *now)
			if err != nil || (p.SourceMatches && p.TargetMatches && p.BindingsMatch && p.VerificationFresh) {
				t.Fatal("changed input passed", p, err)
			}
		})
	}
}

func TestCutoverPreflightTenantAndCancellation(t *testing.T) {
	s, store, c, now, _ := verifiedPreflightFixture(t)
	for _, op := range []func(context.Context, string, string) (CutoverPreflight, error){s.RequestPreflight, s.Preflight} {
		if _, err := op(t.Context(), "other", c.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("tenant leak", err)
		}
	}
	if _, err := s.RequestPreflight(t.Context(), c.AccountID, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Cancel(t.Context(), c.AccountID, c.ID); err != nil {
		t.Fatal(err)
	}
	p, err := store.EvaluateCutoverPreflight(t.Context(), c.AccountID, c.ID, *now)
	if err != nil || !slices.Contains(p.Blockers, "cutover_inactive") {
		t.Fatal("pending cancellation lost", p, err)
	}
	if _, err = s.RequestPreflight(t.Context(), c.AccountID, c.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("cancelled intent refreshed", err)
	}
}

func TestCutoverUsesDatasetIdentityForRestoreLineage(t *testing.T) {
	s, store, _, _, _, now, _, r := cutoverFixture(t)
	source, target := store.databases[r.SourceDatabaseID], store.databases[r.TargetDatabaseID]
	source.DataResourceID = "dataset-distinct-from-compute"
	target.RestoreSourceResourceID = source.DataResourceID
	store.databases[source.ID], store.databases[target.ID] = source, target
	c, err := s.Prepare(t.Context(), r)
	if err != nil {
		t.Fatal("valid dataset lineage rejected", err)
	}
	for range len(c.Credentials) {
		if _, err = s.Reconcile(t.Context(), c.AccountID, c.ID); err != nil {
			t.Fatal(err)
		}
	}
	s.verifier = &cutoverProbe{}
	if _, err = s.Verify(t.Context(), c.AccountID, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Reconcile(t.Context(), c.AccountID, c.ID); err != nil {
		t.Fatal(err)
	}
	for _, valid := range []bool{true, false} {
		if !valid {
			target.RestoreSourceResourceID = source.ProviderResourceID
			store.databases[target.ID] = target
		}
		if _, err = s.RequestPreflight(t.Context(), c.AccountID, c.ID); err != nil {
			t.Fatal(err)
		}
		p, err := store.EvaluateCutoverPreflight(t.Context(), c.AccountID, c.ID, *now)
		if err != nil || p.TargetMatches != valid {
			t.Fatal("compute endpoint accepted as dataset lineage", p.TargetMatches, valid, err)
		}
	}
}
