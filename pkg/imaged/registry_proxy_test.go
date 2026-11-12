package imaged

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/registryproxy"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261110210000 — slice 3j-2: with the registry proxy, a stored
// credential becomes a pull through secretd; the node opens nothing (it
// holds no identity here) and never sees the password.
func TestResolveRegistryAuth_ThroughSecretd(t *testing.T) {
	s := state.NewMemStore()
	ctx := context.Background()
	acct, _ := s.CreateAccount(ctx, "u@example.com", api.PlanHobby)
	app, _ := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "proxied-app", RAMMB: 256, IdleTimeoutS: 60, MaxConcurrency: 2})
	if err := s.UpsertAppRegistryCredential(ctx, acct.ID, app.ID, "ghcr.io", "robot", []byte("sealed-elsewhere")); err != nil {
		t.Fatal(err)
	}
	h := New(s, &fakeNotifier{}, &recordingPuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger()).
		WithRegistryProxy(&registryproxy.Proxy{})

	auth, err := h.resolveRegistryAuth(ctx, app, "ghcr.io")
	if err != nil {
		t.Fatal(err)
	}
	if !auth.Proxied() || auth.Username != "" || auth.Password != "" {
		t.Fatalf("auth = %+v, want a proxy transport and no credential", auth)
	}
	if auth, err := h.resolveRegistryAuth(ctx, app, "registry.example.com"); err != nil || auth != nil {
		t.Fatalf("a registry without a credential = %+v, %v; want anonymous", auth, err)
	}
}
