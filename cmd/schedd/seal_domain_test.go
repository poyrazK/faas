package main

import (
	"path/filepath"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/secretbox"
)

func newSealDomainTestIdentity(t *testing.T) *age.X25519Identity {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestLoadSealDomains(t *testing.T) {
	for _, tc := range []struct {
		env  string
		load func(func(string) string) ([]*age.X25519Identity, error)
	}{
		{envSealDomainControlIdentity, loadControlSealDomain},
		{envSealDomainDeliveryIdentity, loadDeliverySealDomain},
	} {
		t.Run(tc.env, func(t *testing.T) {
			id := newSealDomainTestIdentity(t)
			path := filepath.Join(t.TempDir(), "domain.age")
			if err := secretbox.WriteHostKeyAtPath(path, id); err != nil {
				t.Fatal(err)
			}
			env := func(v string) func(string) string {
				return func(k string) string {
					if k == tc.env {
						return v
					}
					return ""
				}
			}
			if ids, err := tc.load(env("")); err != nil || ids != nil {
				t.Fatalf("unset = %v, %v; want nothing loaded", ids, err)
			}
			ids, err := tc.load(env(path))
			if err != nil || len(ids) != 1 || ids[0].Recipient().String() != id.Recipient().String() {
				t.Fatalf("staged = %v, %v; want the domain identity", ids, err)
			}
			if _, err := tc.load(env(filepath.Join(t.TempDir(), "missing.age"))); err == nil {
				t.Fatal("a configured identity that is missing loaded")
			}
		})
	}
}

func TestDomainReaders(t *testing.T) {
	control, fleet, host := newSealDomainTestIdentity(t), newSealDomainTestIdentity(t), newSealDomainTestIdentity(t)
	got := domainReaders([]*age.X25519Identity{control}, []*age.X25519Identity{fleet, nil, host, fleet})
	want := []*age.X25519Identity{control, fleet, host}
	if len(got) != len(want) {
		t.Fatalf("readers = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Recipient().String() != want[i].Recipient().String() {
			t.Fatalf("reader %d = %s, want %s (the domain first, then legacy, each once)", i, got[i].Recipient(), want[i].Recipient())
		}
	}
}
