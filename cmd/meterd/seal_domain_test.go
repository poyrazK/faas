package main

import (
	"path/filepath"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/secretbox"
)

func newAlertingTestIdentity(t *testing.T) *age.X25519Identity {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestLoadAlertingSealDomain(t *testing.T) {
	id := newAlertingTestIdentity(t)
	path := filepath.Join(t.TempDir(), "alerting.age")
	if err := secretbox.WriteHostKeyAtPath(path, id); err != nil {
		t.Fatal(err)
	}
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == envSealDomainAlertingIdentity {
				return v
			}
			return ""
		}
	}
	if ids, err := loadAlertingSealDomain(env("")); err != nil || ids != nil {
		t.Fatalf("unset = %v, %v; want nothing loaded", ids, err)
	}
	ids, err := loadAlertingSealDomain(env(path))
	if err != nil || len(ids) != 1 || ids[0].Recipient().String() != id.Recipient().String() {
		t.Fatalf("staged = %v, %v; want the alerting identity", ids, err)
	}
	if _, err := loadAlertingSealDomain(env(filepath.Join(t.TempDir(), "missing.age"))); err == nil {
		t.Fatal("a configured identity that is missing loaded")
	}
}

func TestAlertingReaders(t *testing.T) {
	alerting, fleet, host := newAlertingTestIdentity(t), newAlertingTestIdentity(t), newAlertingTestIdentity(t)
	got := alertingReaders([]*age.X25519Identity{alerting}, []*age.X25519Identity{fleet, nil, host, fleet})
	want := []*age.X25519Identity{alerting, fleet, host}
	if len(got) != len(want) {
		t.Fatalf("readers = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Recipient().String() != want[i].Recipient().String() {
			t.Fatalf("reader %d = %s, want %s (alerting first, then legacy, each once)", i, got[i].Recipient(), want[i].Recipient())
		}
	}
}
