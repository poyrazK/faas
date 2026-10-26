package main

import (
	"path/filepath"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/secretbox"
)

func newControlTestIdentity(t *testing.T) *age.X25519Identity {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestLoadControlSealDomain(t *testing.T) {
	id := newControlTestIdentity(t)
	path := filepath.Join(t.TempDir(), "control.age")
	if err := secretbox.WriteHostKeyAtPath(path, id); err != nil {
		t.Fatal(err)
	}
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == envSealDomainControlIdentity {
				return v
			}
			return ""
		}
	}
	if ids, err := loadControlSealDomain(env("")); err != nil || ids != nil {
		t.Fatalf("unset = %v, %v; want nothing loaded", ids, err)
	}
	ids, err := loadControlSealDomain(env(path))
	if err != nil || len(ids) != 1 || ids[0].Recipient().String() != id.Recipient().String() {
		t.Fatalf("staged = %v, %v; want the control identity", ids, err)
	}
	if _, err := loadControlSealDomain(env(filepath.Join(t.TempDir(), "missing.age"))); err == nil {
		t.Fatal("a configured identity that is missing loaded")
	}
}

func TestControlReaders(t *testing.T) {
	control, fleet, host := newControlTestIdentity(t), newControlTestIdentity(t), newControlTestIdentity(t)
	got := controlReaders([]*age.X25519Identity{control}, []*age.X25519Identity{fleet, nil, host, fleet})
	want := []*age.X25519Identity{control, fleet, host}
	if len(got) != len(want) {
		t.Fatalf("readers = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Recipient().String() != want[i].Recipient().String() {
			t.Fatalf("reader %d = %s, want %s (control first, then legacy, each once)", i, got[i].Recipient(), want[i].Recipient())
		}
	}
}
