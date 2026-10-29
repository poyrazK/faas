package apidserver_test

import (
	"context"
	"crypto/ed25519"
	"log/slog"
	"path/filepath"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/sealsig"
)

// adr: 20261028161502 — with its runtime signing key, apid signs every
// runtime secret it seals, and stores the signature before the row.
func TestAppSecretsCarryApidsSignature(t *testing.T) {
	fleet, restore := apidserver.WithTestIdentities(t)
	defer restore()
	e := apidserver.SetupSecrets(t, api.PlanHobby)
	prev := *apidserver.SetSecretRecipient
	*apidserver.SetSecretRecipient = func() *age.X25519Recipient { return fleet.Recipient() }
	t.Cleanup(func() { *apidserver.SetSecretRecipient = prev })

	dir := t.TempDir()
	signKey, verifyKey := filepath.Join(dir, "runtime-signing.key"), filepath.Join(dir, "runtime-signing.pub")
	if err := sealsig.WriteSigningKeyPair(signKey, verifyKey); err != nil {
		t.Fatal(err)
	}
	store := *e.Store()
	none, err := apidserver.InstallRuntimeSigner(context.Background(), func(string) string { return "" }, store, slog.Default())
	if err != nil {
		t.Fatalf("no key configured = %v", err)
	}
	none()
	uninstall, err := apidserver.InstallRuntimeSigner(context.Background(), func(name string) string {
		if name == "FAAS_APID_RUNTIME_SIGNING_KEY_FILE" {
			return signKey
		}
		return ""
	}, store, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(uninstall)

	app := apidserver.CreateApp(t, e, "signed-secrets")
	if rec := e.Do(t, "PUT", "/v1/apps/"+app.Slug+"/secrets/SIGNED_KEY", api.PutAppSecretRequest{Value: "v"}, nil); rec.Code != 200 {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body.String())
	}
	rows, err := store.ListAppSecrets(context.Background(), app.AccountID, app.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListAppSecrets = %v, %v", rows, err)
	}
	keys, err := sealsig.LoadVerifyKeys(verifyKey)
	if err != nil {
		t.Fatal(err)
	}
	digest := sealsig.RuntimeDigest(rows[0].Ciphertext)
	found, err := store.RuntimeSecretSignatures(context.Background(), [][]byte{digest})
	if err != nil {
		t.Fatal(err)
	}
	if err := sealsig.NewVerifier([]ed25519.PublicKey(keys), true).Check(sealsig.RuntimeSecretMessage(rows[0].Ciphertext), found[string(digest)]); err != nil {
		t.Fatalf("the stored secret's signature = %v", err)
	}
}
