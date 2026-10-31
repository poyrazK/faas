package imaged

import (
	"context"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestResolveRegistryAuth_BuildSealDomain pins that the registry identities
// (ADR 20261022111812) open a password sealed to the build domain alone, and
// that the fleet identities, which crash captures keep, do not.
func TestResolveRegistryAuth_BuildSealDomain(t *testing.T) {
	newID := func() *age.X25519Identity {
		id, err := age.GenerateX25519Identity()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	build, fleet := newID(), newID()
	sealed, err := secretbox.SealBytesTo([]*age.X25519Recipient{build.Recipient()}, "registry_creds", []byte("s3cret"), 4096)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		registry []*age.X25519Identity
		wantAuth bool
		wantErr  bool
	}{
		{name: "build domain staged", registry: []*age.X25519Identity{build, fleet}, wantAuth: true},
		{name: "fleet identity alone", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := state.NewMemStore()
			ctx := context.Background()
			acct, _ := s.CreateAccount(ctx, "u@example.com", api.PlanHobby)
			app, _ := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "build-domain-app", RAMMB: 256, IdleTimeoutS: 60, MaxConcurrency: 2})
			if err := s.UpsertAppRegistryCredential(ctx, acct.ID, app.ID, "ghcr.io", "user", sealed); err != nil {
				t.Fatal(err)
			}
			h := New(s, &fakeNotifier{}, &recordingPuller{}, &fakeBuilder{bytesOut: 1024}, "./init", t.TempDir(), silentLogger()).
				WithSecretboxIdentities([]*age.X25519Identity{fleet})
			if tc.registry != nil {
				h.WithRegistryIdentities(tc.registry)
			}
			auth, err := h.resolveRegistryAuth(ctx, app, "ghcr.io")
			if (err != nil) != tc.wantErr {
				t.Fatalf("resolveRegistryAuth error = %v, want error %v", err, tc.wantErr)
			}
			if got := auth != nil && auth.Username == "user" && auth.Password == "s3cret"; got != tc.wantAuth {
				t.Fatalf("auth = %+v, want opened %v", auth, tc.wantAuth)
			}
			if got := h.registryCredentialIdentities(); tc.registry == nil && (len(got) != 1 || got[0] != fleet) {
				t.Fatalf("registry identities without the domain = %v, want the secretbox identities", got)
			}
		})
	}
}
