package sealmigrate_test

import (
	"context"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/basicverifier"
	"github.com/onebox-faas/faas/pkg/sealdomain/sealmigrate"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261110210000 slice 3h — every sealed basic-auth credential without
// a verifier gets one; other edge credentials and present verifiers are left.
func TestBackfillBasicAuthVerifiers(t *testing.T) {
	ctx := context.Background()
	edge, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	seal := func(ns, plain string) []byte {
		t.Helper()
		b, err := secretbox.SealBytes(edge.Recipient(), ns, []byte(plain), 4096)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	fresh := seal(api.AppPublicAuthBasicSealNamespace, "alice\nright")
	present := seal(api.AppPublicAuthBasicSealNamespace, "bob\nother")
	foreign := []byte("does not open")
	rows := &edgeCredentials{rows: map[string]state.EdgeSealedCredential{
		"a": {Kind: state.EdgeSealedCredentialBasicAuth, ID: "app-1", Sealed: fresh},
		"b": {Kind: state.EdgeSealedCredentialBasicAuth, ID: "app-2", Sealed: present},
		"c": {Kind: state.EdgeSealedCredentialBasicAuth, ID: "app-3", Sealed: foreign},
		"d": {Kind: state.EdgeSealedCredentialLogDrain, ID: "drain-1", Sealed: seal("APP_LOG_DRAIN_AUTH_HEADER", "Bearer x")},
	}}
	store := state.NewMemStore()
	if err := store.PutAppBasicAuthVerifier(ctx, basicverifier.Key(present), "$argon2id$kept"); err != nil {
		t.Fatal(err)
	}
	report, err := sealmigrate.BackfillBasicAuthVerifiers(ctx, rows, store, []*age.X25519Identity{edge})
	if err != nil {
		t.Fatal(err)
	}
	if report != (sealmigrate.VerifierReport{Scanned: 3, Present: 1, Written: 1, Failed: 1}) {
		t.Fatalf("report = %+v", report)
	}
	v, err := store.AppBasicAuthVerifier(ctx, basicverifier.Key(fresh))
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := basicverifier.Verify(ctx, v, "alice", "right"); !ok || err != nil {
		t.Fatalf("backfilled verifier = %v, %v", ok, err)
	}
	if kept, _ := store.AppBasicAuthVerifier(ctx, basicverifier.Key(present)); kept != "$argon2id$kept" {
		t.Fatalf("present verifier replaced: %q", kept)
	}
}
