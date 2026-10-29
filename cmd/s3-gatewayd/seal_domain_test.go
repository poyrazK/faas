package main

import (
	"path/filepath"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/secretbox"
)

func newStorageTestIdentity(t *testing.T) *age.X25519Identity {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestLoadStorageSealDomain(t *testing.T) {
	id := newStorageTestIdentity(t)
	path := filepath.Join(t.TempDir(), "storage.age")
	if err := secretbox.WriteHostKeyAtPath(path, id); err != nil {
		t.Fatal(err)
	}
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == envSealDomainStorageIdentity {
				return v
			}
			return ""
		}
	}
	if ids, err := loadStorageSealDomain(env("")); err != nil || ids != nil {
		t.Fatalf("unset = %v, %v; want nothing loaded", ids, err)
	}
	ids, err := loadStorageSealDomain(env(path))
	if err != nil || len(ids) != 1 || ids[0].Recipient().String() != id.Recipient().String() {
		t.Fatalf("staged = %v, %v; want the storage identity", ids, err)
	}
	if _, err := loadStorageSealDomain(env(filepath.Join(t.TempDir(), "missing.age"))); err == nil {
		t.Fatal("a configured identity that is missing loaded")
	}
}

func TestStorageReaders(t *testing.T) {
	storage, fleet, host := newStorageTestIdentity(t), newStorageTestIdentity(t), newStorageTestIdentity(t)
	got := storageReaders([]*age.X25519Identity{storage}, []*age.X25519Identity{fleet, nil, host, fleet})
	want := []*age.X25519Identity{storage, fleet, host}
	if len(got) != len(want) {
		t.Fatalf("readers = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Recipient().String() != want[i].Recipient().String() {
			t.Fatalf("reader %d = %s, want %s (storage first, then legacy, each once)", i, got[i].Recipient(), want[i].Recipient())
		}
	}
}
