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

// registryPasswords is an in-memory pair of registry credential tables.
type registryPasswords struct {
	rows map[string]state.RegistrySealedCredential // keyed by kind|id
	// rotate makes the next write to this key lose once.
	rotate string
}

func (f *registryPasswords) ListRegistrySealedCredentials(_ context.Context, afterKind, afterID string, limit int) ([]state.RegistrySealedCredential, error) {
	var out []state.RegistrySealedCredential
	for _, c := range f.rows {
		if c.Kind > afterKind || (c.Kind == afterKind && c.ID > afterID) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *registryPasswords) ResealRegistryCredential(_ context.Context, c state.RegistrySealedCredential, sealed []byte) (bool, error) {
	key := c.Kind + "|" + c.ID
	if key == f.rotate {
		f.rotate = ""
		return false, nil
	}
	if cur, ok := f.rows[key]; !ok || !bytes.Equal(cur.Sealed, c.Sealed) {
		return false, nil
	}
	f.rows[key] = state.RegistrySealedCredential{Kind: c.Kind, ID: c.ID, Sealed: sealed}
	return true, nil
}

func TestMigrateBuild(t *testing.T) {
	domain, fleet, stranger := mustIdentity(t), mustIdentity(t), mustIdentity(t)
	password := func(ns string, to ...*age.X25519Identity) []byte {
		var rs []*age.X25519Recipient
		for _, id := range to {
			rs = append(rs, id.Recipient())
		}
		blob, err := secretbox.SealBytesTo(rs, ns, []byte("registry-password"), 4096)
		if err != nil {
			t.Fatal(err)
		}
		return blob
	}
	store := &registryPasswords{rotate: "job|j2", rows: map[string]state.RegistrySealedCredential{}}
	for _, c := range []state.RegistrySealedCredential{
		{Kind: state.RegistrySealedCredentialApp, ID: "a1", Sealed: password("registry_creds", fleet)},
		{Kind: state.RegistrySealedCredentialApp, ID: "a2", Sealed: password("registry_creds", domain, fleet)},
		{Kind: state.RegistrySealedCredentialApp, ID: "a3", Sealed: password("registry_creds", domain)},
		{Kind: state.RegistrySealedCredentialApp, ID: "a4", Sealed: password("app_secret", fleet)},
		{Kind: state.RegistrySealedCredentialJob, ID: "j1", Sealed: password("registry_creds", fleet)},
		{Kind: state.RegistrySealedCredentialJob, ID: "j2", Sealed: password("registry_creds", fleet)},
		{Kind: state.RegistrySealedCredentialJob, ID: "j3", Sealed: password("registry_creds", stranger)},
	} {
		store.rows[c.Kind+"|"+c.ID] = c
	}
	report, err := sealmigrate.MigrateBuild(context.Background(), store, domain, []*age.X25519Identity{fleet})
	if err != nil {
		t.Fatal(err)
	}
	want := sealmigrate.MigrationReport{Domain: sealdomain.Build, Target: domain.Recipient().String(), Scanned: 7, Resealed: 3, Current: 1, Changed: 1, Failed: 2}
	if report != want {
		t.Fatalf("report = %+v\nwant     %+v", report, want)
	}
	for _, key := range []string{"app|a1", "app|a2", "app|a3", "job|j1"} {
		sealed := store.rows[key].Sealed
		if ns, plain, err := secretbox.OpenBytes(domain, sealed); err != nil || ns != "registry_creds" || string(plain) != "registry-password" {
			t.Fatalf("%s: domain open = %q %q, %v", key, ns, plain, err)
		}
		if _, _, err := secretbox.OpenBytes(fleet, sealed); err == nil {
			t.Fatalf("%s still opens with the fleet identity", key)
		}
	}
	again, err := sealmigrate.MigrateBuild(context.Background(), store, domain, []*age.X25519Identity{fleet})
	if err != nil {
		t.Fatal(err)
	}
	// The rotated job password moves on the second run; the wrong namespace
	// and the stranger's password stay failed.
	if want := (sealmigrate.MigrationReport{Domain: sealdomain.Build, Target: domain.Recipient().String(), Scanned: 7, Resealed: 1, Current: 4, Failed: 2}); again != want {
		t.Fatalf("second run = %+v\nwant          %+v", again, want)
	}
}

func TestMigrateBuild_RequiresIdentities(t *testing.T) {
	domain := mustIdentity(t)
	if _, err := sealmigrate.MigrateBuild(context.Background(), &registryPasswords{}, domain, nil); err == nil {
		t.Fatal("migrating without legacy identities succeeded")
	}
	if _, err := sealmigrate.MigrateBuild(context.Background(), &registryPasswords{}, nil, []*age.X25519Identity{domain}); err == nil {
		t.Fatal("migrating without the domain identity succeeded")
	}
}
