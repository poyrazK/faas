package sealmigrate_test

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/sealdomain/sealmigrate"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261023173624 — the binding pass binds stored runtime secrets to
// the account that owns them.

// bindingDocs lists every document, like ListSecretBindingDocuments, with the
// owner recorded for its cursor.
type bindingDocs struct {
	runtimeDocs
	owners map[string]string
}

func (f *bindingDocs) ListSecretBindingDocuments(_ context.Context, after string, limit int) ([]state.SecretBindingDocument, error) {
	var out []state.SecretBindingDocument
	for cursor, d := range f.docs {
		if cursor > after {
			out = append(out, state.SecretBindingDocument{SecretRekeyDocument: d, AccountID: f.owners[cursor]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Cursor() < out[j].Cursor() })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *bindingDocs) addOwned(t *testing.T, owner, kind, id string, envs ...runtimeEnvelope) {
	t.Helper()
	f.add(t, kind, id, envs...)
	if f.owners == nil {
		f.owners = map[string]string{}
	}
	f.owners[kind+"|"+id] = owner
}

// boundTo seals key's value to "to", bound to account, and labels it kid.
func boundTo(t *testing.T, to *age.X25519Identity, kid, key, account string) runtimeEnvelope {
	t.Helper()
	blob, err := secretbox.SealOneBound(to.Recipient(), secretbox.Binding{AccountID: account}, key, "value-of-"+key, 0)
	if err != nil {
		t.Fatal(err)
	}
	return runtimeEnvelope{Key: key, Kid: kid, ValueHash: "hash-of-" + key, Ciphertext: blob}
}

func TestBindRuntime(t *testing.T) {
	domain, fleet, stranger := mustIdentity(t), mustIdentity(t), mustIdentity(t)
	dk, fk := domain.Recipient().String(), fleet.Recipient().String()
	store := &bindingDocs{runtimeDocs: runtimeDocs{rotate: "app|acct-a/app/default/E_ROTATED"}}
	store.addOwned(t, "acct-a", "app", "acct-a/app/default/A_DOMAIN", sealedTo(t, domain, dk, "A_DOMAIN"))
	store.addOwned(t, "acct-a", "app", "acct-a/app/default/B_FLEET", sealedTo(t, fleet, fk, "B_FLEET"))
	store.addOwned(t, "acct-a", "app", "acct-a/app/default/C_BOUND", boundTo(t, domain, dk, "C_BOUND", "acct-a"))
	store.addOwned(t, "acct-a", "app", "acct-a/app/default/D_BOUND_FLEET", boundTo(t, fleet, fk, "D_BOUND_FLEET", "acct-a"))
	store.addOwned(t, "acct-a", "app", "acct-a/app/default/E_ROTATED", sealedTo(t, domain, dk, "E_ROTATED"))
	store.addOwned(t, "acct-a", "app", "acct-a/app/default/F_FOREIGN", boundTo(t, domain, dk, "F_FOREIGN", "acct-b"))
	store.addOwned(t, "acct-a", "app", "acct-a/app/default/G_STRANGER", sealedTo(t, stranger, "", "G_STRANGER"))
	store.addOwned(t, "acct-a", "bundle", "acct-a/req-1", boundTo(t, domain, dk, "ALREADY", "acct-a"), sealedTo(t, domain, dk, "STRIPE_KEY"))
	store.addOwned(t, "acct-d", "deployment", "dep-1", sealedTo(t, domain, dk, "SESSION_SECRET"))
	store.addOwned(t, "", "deployment", "dep-orphan", sealedTo(t, domain, dk, "ORPHAN"))

	report, err := sealmigrate.BindRuntime(context.Background(), store, domain, []*age.X25519Identity{fleet})
	if err != nil {
		t.Fatal(err)
	}
	want := sealmigrate.BindingReport{
		MigrationReport:     sealmigrate.MigrationReport{Domain: sealdomain.Runtime, Target: dk, Scanned: 10, Resealed: 5, Current: 1, Changed: 1, Failed: 2},
		Mismatched:          1,
		MismatchedDocuments: []string{"app|acct-a/app/default/F_FOREIGN"},
	}
	if fmt.Sprint(report) != fmt.Sprint(want) {
		t.Fatalf("report = %+v\nwant     %+v", report, want)
	}
	for cursor, owner := range map[string]string{
		"app|acct-a/app/default/A_DOMAIN":      "acct-a",
		"app|acct-a/app/default/B_FLEET":       "acct-a",
		"app|acct-a/app/default/C_BOUND":       "acct-a",
		"app|acct-a/app/default/D_BOUND_FLEET": "acct-a",
		"bundle|acct-a/req-1":                  "acct-a",
		"deployment|dep-1":                     "acct-d",
	} {
		for _, e := range store.envelopes(t, cursor) {
			if e.Kid != dk || e.ValueHash != "hash-of-"+e.Key {
				t.Fatalf("%s %s: kid %s hash %s, want the domain kid and the hash unchanged", cursor, e.Key, e.Kid, e.ValueHash)
			}
			env, binding, err := secretbox.OpenMultiBound([]*age.X25519Identity{domain}, e.Ciphertext)
			if err != nil || env[e.Key] != "value-of-"+e.Key || binding.AccountID != owner {
				t.Fatalf("%s %s: domain open = %v, %+v, %v; want bound to %s", cursor, e.Key, env, binding, err, owner)
			}
			if _, err := secretbox.Open(fleet, e.Ciphertext); err == nil {
				t.Fatalf("%s %s still opens with the fleet identity", cursor, e.Key)
			}
		}
	}
	// The foreign-bound envelope is the copy the binding exists to catch:
	// the pass reports it and never rebinds it.
	foreign := store.envelopes(t, "app|acct-a/app/default/F_FOREIGN")[0]
	if _, binding, err := secretbox.OpenMultiBound([]*age.X25519Identity{domain}, foreign.Ciphertext); err != nil || binding.AccountID != "acct-b" {
		t.Fatalf("foreign envelope = %+v, %v; want it still bound to acct-b", binding, err)
	}

	// The rotated document is bound on the next run; nothing else changes.
	again, err := sealmigrate.BindRuntime(context.Background(), store, domain, []*age.X25519Identity{fleet})
	if err != nil {
		t.Fatal(err)
	}
	want.Resealed, want.Current, want.Changed = 1, 6, 0
	if fmt.Sprint(again) != fmt.Sprint(want) {
		t.Fatalf("second run = %+v\nwant          %+v", again, want)
	}
	third, err := sealmigrate.BindRuntime(context.Background(), store, domain, nil)
	if err != nil {
		t.Fatal(err)
	}
	if third.Resealed != 0 || third.Current != 7 {
		t.Fatalf("third run without legacy identities = %+v, want every bound document current", third)
	}
}

func TestBindRuntimePages(t *testing.T) {
	domain := mustIdentity(t)
	store := &bindingDocs{}
	const rows = 205 // more than one page
	for i := range rows {
		key := fmt.Sprintf("K%03d", i)
		store.addOwned(t, "acct-a", "app", "acct-a/app/default/"+key, sealedTo(t, domain, domain.Recipient().String(), key))
	}
	report, err := sealmigrate.BindRuntime(context.Background(), store, domain, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Scanned != rows || report.Resealed != rows {
		t.Fatalf("report = %+v, want all %d bound", report, rows)
	}
	again, err := sealmigrate.BindRuntime(context.Background(), store, domain, nil)
	if err != nil || again.Scanned != rows || again.Current != rows || again.Resealed != 0 {
		t.Fatalf("second run = %+v, %v; want every document current", again, err)
	}
}

func TestBindRuntimeNeedsTheDomain(t *testing.T) {
	if _, err := sealmigrate.BindRuntime(context.Background(), &bindingDocs{}, nil, []*age.X25519Identity{mustIdentity(t)}); err == nil {
		t.Fatal("binding without the domain identity succeeded")
	}
	if _, err := sealmigrate.BindRuntime(context.Background(), nil, mustIdentity(t), nil); err == nil {
		t.Fatal("binding without a store succeeded")
	}
}
