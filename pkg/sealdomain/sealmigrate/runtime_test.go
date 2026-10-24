package sealmigrate_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/sealdomain/sealmigrate"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// runtimeDocs is an in-memory release-secret rekey document store. Like the
// SQL view, it lists only documents with an envelope labelled for another key.
type runtimeDocs struct {
	docs map[string]state.SecretRekeyDocument
	// rotate makes the next compare-and-swap of this cursor lose once.
	rotate string
}

type runtimeEnvelope struct {
	Key        string `json:"key"`
	Kid        string `json:"kid"`
	ValueHash  string `json:"value_hash"`
	Ciphertext []byte `json:"ciphertext"`
}

func (f *runtimeDocs) ListSecretRekeyDocuments(_ context.Context, kid, after string, limit int) ([]state.SecretRekeyDocument, error) {
	var out []state.SecretRekeyDocument
	for _, d := range f.docs {
		if d.Cursor() <= after {
			continue
		}
		var envs []runtimeEnvelope
		if err := json.Unmarshal(d.Payload, &envs); err != nil {
			return nil, err
		}
		for _, e := range envs {
			if e.Kid != kid {
				out = append(out, d)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Cursor() < out[j].Cursor() })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *runtimeDocs) CompareAndSwapSecretRekeyDocument(_ context.Context, before state.SecretRekeyDocument, after json.RawMessage) error {
	if before.Cursor() == f.rotate {
		f.rotate = ""
		return state.ErrConflict
	}
	current, ok := f.docs[before.Cursor()]
	if !ok || !bytes.Equal(current.Payload, before.Payload) {
		return state.ErrConflict
	}
	f.docs[before.Cursor()] = state.SecretRekeyDocument{Kind: before.Kind, ID: before.ID, Payload: after}
	return nil
}

func (f *runtimeDocs) add(t *testing.T, kind, id string, envs ...runtimeEnvelope) {
	t.Helper()
	raw, err := json.Marshal(envs)
	if err != nil {
		t.Fatal(err)
	}
	if f.docs == nil {
		f.docs = map[string]state.SecretRekeyDocument{}
	}
	d := state.SecretRekeyDocument{Kind: kind, ID: id, Payload: raw}
	f.docs[d.Cursor()] = d
}

func (f *runtimeDocs) envelopes(t *testing.T, cursor string) []runtimeEnvelope {
	t.Helper()
	var envs []runtimeEnvelope
	if err := json.Unmarshal(f.docs[cursor].Payload, &envs); err != nil {
		t.Fatal(err)
	}
	return envs
}

// sealedTo seals key's value to "to" and labels it with kid.
func sealedTo(t *testing.T, to *age.X25519Identity, kid, key string) runtimeEnvelope {
	t.Helper()
	blob, err := secretbox.SealOne(to.Recipient(), key, "value-of-"+key, 0)
	if err != nil {
		t.Fatal(err)
	}
	return runtimeEnvelope{Key: key, Kid: kid, ValueHash: "hash-of-" + key, Ciphertext: blob}
}

func TestMigrateRuntime(t *testing.T) {
	domain, fleet, stranger := mustIdentity(t), mustIdentity(t), mustIdentity(t)
	dk, fk := domain.Recipient().String(), fleet.Recipient().String()
	store := &runtimeDocs{rotate: "app|a/app/default/C_ROTATED"}
	store.add(t, "app", "a/app/default/A_FLEET", sealedTo(t, fleet, fk, "A_FLEET"))
	store.add(t, "app", "a/app/default/B_DONE", sealedTo(t, domain, dk, "B_DONE"))
	store.add(t, "app", "a/app/default/C_ROTATED", sealedTo(t, fleet, fk, "C_ROTATED"))
	store.add(t, "app", "a/app/default/D_STRANGER", sealedTo(t, stranger, "", "D_STRANGER"))
	store.add(t, "project", "a/p/production/DATABASE_URL", sealedTo(t, fleet, "", "DATABASE_URL"))
	// Sealed to the domain but labelled for the fleet: only the label moves.
	store.add(t, "project", "a/p/production/MISLABELLED", sealedTo(t, domain, fk, "MISLABELLED"))
	store.add(t, "bundle", "a/req-1", sealedTo(t, fleet, fk, "STRIPE_KEY"), sealedTo(t, domain, dk, "ALREADY"))
	store.add(t, "deployment", "dep-1", sealedTo(t, fleet, fk, "SESSION_SECRET"))

	report, err := sealmigrate.MigrateRuntime(context.Background(), store, domain, []*age.X25519Identity{fleet})
	if err != nil {
		t.Fatal(err)
	}
	// B_DONE is never listed: every envelope already names the domain.
	want := sealmigrate.MigrationReport{Domain: sealdomain.Runtime, Target: dk, Scanned: 7, Resealed: 5, Changed: 1, Failed: 1}
	if report != want {
		t.Fatalf("report = %+v\nwant     %+v", report, want)
	}
	for _, cursor := range []string{"app|a/app/default/A_FLEET", "project|a/p/production/DATABASE_URL", "project|a/p/production/MISLABELLED", "bundle|a/req-1", "deployment|dep-1"} {
		for _, e := range store.envelopes(t, cursor) {
			if e.Kid != dk {
				t.Fatalf("%s %s: kid = %s, want the domain recipient", cursor, e.Key, e.Kid)
			}
			if e.ValueHash != "hash-of-"+e.Key {
				t.Fatalf("%s %s: value hash changed to %q", cursor, e.Key, e.ValueHash)
			}
			if _, err := secretbox.Open(fleet, e.Ciphertext); err == nil {
				t.Fatalf("%s %s still opens with the fleet identity", cursor, e.Key)
			}
			if env, err := secretbox.Open(domain, e.Ciphertext); err != nil || env[e.Key] != "value-of-"+e.Key {
				t.Fatalf("%s %s: domain open = %v, %v", cursor, e.Key, env, err)
			}
		}
	}
	if e := store.envelopes(t, "app|a/app/default/D_STRANGER")[0]; e.Kid != "" {
		t.Fatalf("an envelope no identity opens was rewritten: kid = %q", e.Kid)
	}

	// The rotated row is re-sealed on the next run; the stranger stays failed.
	again, err := sealmigrate.MigrateRuntime(context.Background(), store, domain, []*age.X25519Identity{fleet})
	if err != nil {
		t.Fatal(err)
	}
	if want := (sealmigrate.MigrationReport{Domain: sealdomain.Runtime, Target: dk, Scanned: 2, Resealed: 1, Failed: 1}); again != want {
		t.Fatalf("second run = %+v\nwant          %+v", again, want)
	}
}

func TestMigrateRuntimePages(t *testing.T) {
	domain, fleet := mustIdentity(t), mustIdentity(t)
	store := &runtimeDocs{}
	const rows = 205 // more than one page
	for i := range rows {
		key := fmt.Sprintf("K%03d", i)
		store.add(t, "app", "a/app/default/"+key, sealedTo(t, fleet, fleet.Recipient().String(), key))
	}
	report, err := sealmigrate.MigrateRuntime(context.Background(), store, domain, []*age.X25519Identity{fleet})
	if err != nil {
		t.Fatal(err)
	}
	if report.Scanned != rows || report.Resealed != rows {
		t.Fatalf("report = %+v, want all %d re-sealed", report, rows)
	}
	again, err := sealmigrate.MigrateRuntime(context.Background(), store, domain, []*age.X25519Identity{fleet})
	if err != nil || again.Scanned != 0 {
		t.Fatalf("second run = %+v, %v; want nothing listed", again, err)
	}
}

func TestMigrateRuntimeNeedsIdentities(t *testing.T) {
	domain := mustIdentity(t)
	if _, err := sealmigrate.MigrateRuntime(context.Background(), &runtimeDocs{}, domain, nil); err == nil {
		t.Fatal("migrating without legacy identities succeeded")
	}
	if _, err := sealmigrate.MigrateRuntime(context.Background(), &runtimeDocs{}, nil, []*age.X25519Identity{domain}); err == nil {
		t.Fatal("migrating without the domain identity succeeded")
	}
}
