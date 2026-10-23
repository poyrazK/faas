package sealmigrate_test

import (
	"bytes"
	"context"
	"sort"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/sealdomain/sealmigrate"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

type appSecretRows struct {
	rows []state.AppSecret
	// rotate makes the compare-and-swap of this key lose.
	rotate string
}

func (f *appSecretRows) ListAppSecretsForRekey(_ context.Context, limit int, cursor string) ([]state.AppSecret, error) {
	sort.Slice(f.rows, func(i, j int) bool { return f.rows[i].Key < f.rows[j].Key })
	var out []state.AppSecret
	for _, r := range f.rows {
		if cursor != "" && "a|app|default|"+r.Key < cursor {
			continue
		}
		out = append(out, r)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (f *appSecretRows) ResealAppSecretForFleet(_ context.Context, row state.AppSecret, kid string, sealed []byte) error {
	if row.Key == f.rotate {
		return state.ErrConflict
	}
	for i := range f.rows {
		if f.rows[i].Key == row.Key && bytes.Equal(f.rows[i].Ciphertext, row.Ciphertext) {
			f.rows[i].Ciphertext, f.rows[i].Kid = sealed, kid
			return nil
		}
	}
	return state.ErrConflict
}

func TestMigrateRuntime(t *testing.T) {
	domain, fleet, stranger := mustIdentity(t), mustIdentity(t), mustIdentity(t)
	row := func(key string, to *age.X25519Identity) state.AppSecret {
		blob, err := secretbox.SealOne(to.Recipient(), key, "value-of-"+key, 0)
		if err != nil {
			t.Fatal(err)
		}
		return state.AppSecret{AccountID: "a", AppID: "app", Scope: "default", Key: key, Ciphertext: blob, Kid: to.Recipient().String()}
	}
	store := &appSecretRows{rotate: "C_ROTATED", rows: []state.AppSecret{
		row("A_FLEET", fleet), row("B_DONE", domain), row("C_ROTATED", fleet), row("D_STRANGER", stranger),
	}}
	report, err := sealmigrate.MigrateRuntime(context.Background(), store, domain, []*age.X25519Identity{fleet})
	if err != nil {
		t.Fatal(err)
	}
	want := sealmigrate.MigrationReport{Domain: sealdomain.Runtime, Target: domain.Recipient().String(), Scanned: 4, Resealed: 1, Current: 1, Changed: 1, Failed: 1}
	if report != want {
		t.Fatalf("report = %+v\nwant     %+v", report, want)
	}
	for _, r := range store.rows {
		if r.Key != "A_FLEET" {
			continue
		}
		if r.Kid != domain.Recipient().String() {
			t.Fatalf("re-sealed kid = %s, want the domain recipient", r.Kid)
		}
		if _, err := secretbox.Open(fleet, r.Ciphertext); err == nil {
			t.Fatal("a re-sealed runtime secret still opens with the fleet identity")
		}
		if env, err := secretbox.Open(domain, r.Ciphertext); err != nil || env["A_FLEET"] != "value-of-A_FLEET" {
			t.Fatalf("domain open = %v, %v", env, err)
		}
	}
	again, err := sealmigrate.MigrateRuntime(context.Background(), store, domain, []*age.X25519Identity{fleet})
	if err != nil || again.Resealed != 0 || again.Current != 2 {
		t.Fatalf("second run = %+v, %v; want nothing re-sealed (idempotent)", again, err)
	}
}
