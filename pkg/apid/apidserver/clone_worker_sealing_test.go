package apidserver_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// adr: 20261023173624 — the clone worker seals a clone checkpoint's app
// secret as apid does: to the runtime domain and bound to the account once
// the rollout turned those on, not to the fleet key, unbound.
func TestCloneWorkerSealsRuntimeSecretsLikeApid(t *testing.T) {
	prevRecipient, prevIDs, prevHMAC := *apidserver.SetSecretRecipient, *apidserver.MfaIdentities, *apidserver.HostHMACKey
	prevDomain, prevMode, prevBound := *apidserver.RuntimeDomainRecipient, *apidserver.RuntimeWriteMode, *apidserver.RuntimeSecretsBound
	t.Cleanup(func() {
		*apidserver.SetSecretRecipient, *apidserver.MfaIdentities, *apidserver.HostHMACKey = prevRecipient, prevIDs, prevHMAC
		*apidserver.RuntimeDomainRecipient, *apidserver.RuntimeWriteMode, *apidserver.RuntimeSecretsBound = prevDomain, prevMode, prevBound
	})

	dir := t.TempDir()
	write := func(name, content string, mode os.FileMode) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
		return path
	}
	fleet, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	domain, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"FAAS_FLEET_AGE_IDENTITY_PATH":            write("faas_fleet_age_identity", fleet.String(), 0o400),
		"FAAS_FLEET_AGE_RECIPIENT_PATH":           write("faas_fleet_age_recipient", fleet.Recipient().String(), 0o444),
		"FAAS_HOST_HMAC_KEY_PATH":                 write("faas_host_hmac_key", strings.Repeat("k", 32), 0o400),
		"FAAS_SEAL_DOMAIN_RUNTIME_RECIPIENT_PATH": write("faas_seal_domain_runtime_recipient", domain.Recipient().String(), 0o444),
		"FAAS_SEAL_DOMAIN_RUNTIME_WRITES":         "domain",
		"FAAS_RUNTIME_SECRET_BINDING":             "account",
	}
	if err := apidserver.LoadCloneWorkerSealing(func(k string) string { return env[k] }, slog.Default()); err != nil {
		t.Fatal(err)
	}
	if got := apidserver.RuntimeSecretRecipient(); got == nil || got.String() != domain.Recipient().String() {
		t.Fatalf("runtime recipient = %v, want the runtime domain's", got)
	}
	if *apidserver.RuntimeWriteMode != sealdomain.WriteDomain {
		t.Fatalf("runtime write mode = %q", *apidserver.RuntimeWriteMode)
	}
	const account = "0b6a6a1e-6f3c-4c8e-9d2a-5d1f7a3e9c10"
	if got := apidserver.RuntimeSecretBinding(account); got != (secretbox.Binding{AccountID: account}) {
		t.Fatalf("runtime binding = %+v, want the account", got)
	}

	env["FAAS_RUNTIME_SECRET_BINDING"] = "everyone"
	if err := apidserver.LoadCloneWorkerSealing(func(k string) string { return env[k] }, slog.Default()); err == nil {
		t.Fatal("an invalid binding mode started the clone worker")
	}
}
