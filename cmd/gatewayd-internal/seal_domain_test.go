package main

import (
	"context"
	"path/filepath"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

func newEdgeTestIdentity(t *testing.T) *age.X25519Identity {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestLoadEdgeSealDomain(t *testing.T) {
	id := newEdgeTestIdentity(t)
	path := filepath.Join(t.TempDir(), "edge.age")
	if err := secretbox.WriteHostKeyAtPath(path, id); err != nil {
		t.Fatal(err)
	}
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == envSealDomainEdgeIdentity {
				return v
			}
			return ""
		}
	}
	if ids, err := loadEdgeSealDomain(env("")); err != nil || ids != nil {
		t.Fatalf("unset = %v, %v; want nothing loaded", ids, err)
	}
	ids, err := loadEdgeSealDomain(env(path))
	if err != nil || len(ids) != 1 || ids[0].Recipient().String() != id.Recipient().String() {
		t.Fatalf("staged = %v, %v; want the edge identity", ids, err)
	}
	if _, err := loadEdgeSealDomain(env(filepath.Join(t.TempDir(), "missing.age"))); err == nil {
		t.Fatal("a configured identity that is missing loaded")
	}
}

func TestEdgeReaders(t *testing.T) {
	edge, fleet, host := newEdgeTestIdentity(t), newEdgeTestIdentity(t), newEdgeTestIdentity(t)
	got := edgeReaders([]*age.X25519Identity{edge}, []*age.X25519Identity{fleet, nil, host, fleet})
	want := []*age.X25519Identity{edge, fleet, host}
	if len(got) != len(want) {
		t.Fatalf("readers = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Recipient().String() != want[i].Recipient().String() {
			t.Fatalf("reader %d = %s, want %s (edge first, then legacy, each once)", i, got[i].Recipient(), want[i].Recipient())
		}
	}
}

// TestEdgeReadersOpenBothGenerations pins the rollout window (ADR
// 20261022111812): with the edge identity staged, gatewayd-internal opens
// credentials sealed to the domain and those still sealed to the fleet key.
func TestEdgeReadersOpenBothGenerations(t *testing.T) {
	edge, fleet, stranger := newEdgeTestIdentity(t), newEdgeTestIdentity(t), newEdgeTestIdentity(t)
	readers := edgeReaders([]*age.X25519Identity{edge}, []*age.X25519Identity{fleet})
	seal := func(to *age.X25519Identity, namespace, plain string) []byte {
		t.Helper()
		blob, err := secretbox.SealBytes(to.Recipient(), namespace, []byte(plain), 0)
		if err != nil {
			t.Fatal(err)
		}
		return blob
	}

	unseal := newAppLogDrainUnsealer(readers)
	for name, blob := range map[string][]byte{
		"domain": seal(edge, appLogDrainSecretSealLabel, "Authorization: Bearer d"),
		"fleet":  seal(fleet, appLogDrainSecretSealLabel, "Authorization: Bearer d"),
	} {
		if got, err := unseal(blob); err != nil || got != "Authorization: Bearer d" {
			t.Errorf("drain header sealed to %s = %q, %v", name, got, err)
		}
	}
	if _, err := unseal(seal(stranger, appLogDrainSecretSealLabel, "x: y")); err == nil {
		t.Error("a header sealed to another key opened")
	}
	if _, err := unseal(seal(edge, api.AppPublicAuthBasicSealNamespace, "x: y")); err == nil {
		t.Error("a basic-auth blob opened as a drain header")
	}
	if newAppLogDrainUnsealer(nil) != nil {
		t.Error("an unsealer with no identities was built")
	}

	basic := newPublicAuthUnsealer(func() []*age.X25519Identity { return readers })
	for name, blob := range map[string][]byte{
		"domain": seal(edge, api.AppPublicAuthBasicSealNamespace, "editor\npass"),
		"fleet":  seal(fleet, api.AppPublicAuthBasicSealNamespace, "editor\npass"),
	} {
		if user, pass, err := basic.UnsealBasicAuth(context.Background(), blob); err != nil || user != "editor" || pass != "pass" {
			t.Errorf("basic auth sealed to %s = %q %q, %v", name, user, pass, err)
		}
	}
}
