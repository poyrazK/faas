package apidserver_test

import (
	"context"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// adr: 20261023173624 — runtime secrets are bound to their account once the
// unit switch is on, and written in the original format before.

func TestRuntimeSecretBindingSwitchValues(t *testing.T) {
	for value, want := range map[string]bool{"": false, "off": false, "account": true} {
		got, err := apidserver.LoadRuntimeSecretBinding(func(string) string { return value })
		if err != nil || got != want {
			t.Errorf("FAAS_RUNTIME_SECRET_BINDING=%q: %v, %v; want %v", value, got, err, want)
		}
	}
	if _, err := apidserver.LoadRuntimeSecretBinding(func(string) string { return "app" }); err == nil {
		t.Error("an unknown binding mode loaded")
	}
}

func TestAppSecretsBindToTheAppAccount(t *testing.T) {
	fleet, restore := apidserver.WithTestIdentities(t)
	defer restore()
	e := apidserver.SetupSecrets(t, api.PlanHobby)
	prev := *apidserver.SetSecretRecipient
	*apidserver.SetSecretRecipient = func() *age.X25519Recipient { return fleet.Recipient() }
	t.Cleanup(func() { *apidserver.SetSecretRecipient = prev })
	prevBound := *apidserver.RuntimeSecretsBound
	t.Cleanup(func() { *apidserver.RuntimeSecretsBound = prevBound })

	app := apidserver.CreateApp(t, e, "bound-secrets")
	for _, tc := range []struct {
		bound bool
		key   string
		want  secretbox.Binding
	}{
		{false, "UNBOUND_KEY", secretbox.Binding{}},
		{true, "BOUND_KEY", secretbox.Binding{AccountID: app.AccountID}},
	} {
		*apidserver.RuntimeSecretsBound = tc.bound
		if rec := e.Do(t, "PUT", "/v1/apps/"+app.Slug+"/secrets/"+tc.key, api.PutAppSecretRequest{Value: "v"}, nil); rec.Code != 200 {
			t.Fatalf("PUT %s: %d %s", tc.key, rec.Code, rec.Body.String())
		}
		rows, err := (*e.Store()).ListAppSecrets(context.Background(), app.AccountID, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, row := range rows {
			if row.Key != tc.key {
				continue
			}
			found = true
			env, binding, err := secretbox.OpenMultiBound([]*age.X25519Identity{fleet}, row.Ciphertext)
			if err != nil || env[tc.key] != "v" || binding != tc.want {
				t.Errorf("%s: %v, %+v, %v; want binding %+v", tc.key, env, binding, err, tc.want)
			}
		}
		if !found {
			t.Fatalf("%s was not stored", tc.key)
		}
	}
}
