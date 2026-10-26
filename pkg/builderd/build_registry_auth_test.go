// adr: 20261026090000
package builderd

import (
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A Dockerfile build pulls its base images with the app's registry
// credentials; an ECR access key is left out because a builder cannot
// exchange it and must never send it to a registry.
func TestLoadBuildRegistryCredentials(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(t.Context(), "registry-build@example.com", api.PlanPro)
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "registry-build", RAMMB: 256, IdleTimeoutS: 60, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	const ecr = "123456789012.dkr.ecr.eu-west-1.amazonaws.com"
	for _, row := range []struct{ registry, user, sealed string }{
		{"ghcr.io", "octo", "sealed-gh"},
		{ecr, "AKIAIOSFODNN7EXAMPLE", "sealed-aws-key"},
		{"registry-1.docker.io", "hub", "sealed-hub"},
	} {
		if err := store.UpsertAppRegistryCredential(t.Context(), acct.ID, app.ID, row.registry, row.user, []byte(row.sealed)); err != nil {
			t.Fatal(err)
		}
	}
	creds, skipped, err := loadBuildRegistryCredentials(t.Context(), store, app)
	if err != nil {
		t.Fatal(err)
	}
	want := []SealedRegistryCredential{
		{Registry: "ghcr.io", Username: "octo", PasswordSealed: []byte("sealed-gh")},
		{Registry: "registry-1.docker.io", Username: "hub", PasswordSealed: []byte("sealed-hub")},
	}
	if !reflect.DeepEqual(creds, want) || !reflect.DeepEqual(skipped, []string{ecr}) {
		t.Fatalf("creds = %+v, skipped = %v", creds, skipped)
	}
	proto := registryCredentialsToProto(creds)
	if len(proto) != 2 || proto[0].GetRegistry() != "ghcr.io" || string(proto[1].GetPasswordSealed()) != "sealed-hub" {
		t.Fatalf("proto = %+v", proto)
	}
	if registryCredentialsToProto(nil) != nil {
		t.Fatal("no credentials produced a proto list")
	}
}

// The cache partition follows the app and its sealed credentials, and is
// empty without credentials so existing cache keys stay unchanged.
func TestRegistryAuthScope(t *testing.T) {
	gh := SealedRegistryCredential{Registry: "ghcr.io", Username: "octo", PasswordSealed: []byte("a")}
	hub := SealedRegistryCredential{Registry: "registry-1.docker.io", Username: "hub", PasswordSealed: []byte("b")}
	if registryAuthScope("app-1", nil) != "" {
		t.Fatal("a build without credentials got a scope")
	}
	base := registryAuthScope("app-1", []SealedRegistryCredential{gh, hub})
	if base == "" || base != registryAuthScope("app-1", []SealedRegistryCredential{hub, gh}) {
		t.Fatal("the scope depends on credential order")
	}
	rotated := gh
	rotated.PasswordSealed = []byte("c")
	for name, other := range map[string]string{
		"another app":         registryAuthScope("app-2", []SealedRegistryCredential{gh, hub}),
		"a replaced password": registryAuthScope("app-1", []SealedRegistryCredential{rotated, hub}),
	} {
		if other == base {
			t.Errorf("%s shares the cache scope", name)
		}
	}
}
