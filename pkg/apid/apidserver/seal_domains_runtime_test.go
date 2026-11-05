package apidserver_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// TestRuntimeSealDomainSealsAppSecretsForVmmdOnly pins ADR 20261022111812:
// in domain mode an app secret apid seals opens with the runtime domain
// identity (vmmd's) and not with the fleet identity apid holds, and its kid
// names the domain.
func TestRuntimeSealDomainSealsAppSecretsForVmmdOnly(t *testing.T) {
	fleet, restore := apidserver.WithTestIdentities(t)
	defer restore()
	e := apidserver.SetupSecrets(t, api.PlanHobby)
	domain := mustSealIdentity(t)
	withRuntimeWrites(t, domain.Recipient(), sealdomain.WriteDomain)
	// SetupSecrets installs its own recipient; point the fleet seal back at
	// the identity this test can open with.
	prev := *apidserver.SetSecretRecipient
	*apidserver.SetSecretRecipient = func() *age.X25519Recipient { return fleet.Recipient() }
	t.Cleanup(func() { *apidserver.SetSecretRecipient = prev })

	app := apidserver.CreateApp(t, e, "runtime-domain")
	if rec := e.Do(t, "PUT", "/v1/apps/"+app.Slug+"/secrets/OPENAI_API_KEY", api.PutAppSecretRequest{Value: "sk-live"}, nil); rec.Code != 200 {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body.String())
	}
	rows, err := (*e.Store()).ListAppSecrets(context.Background(), app.AccountID, app.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("stored secrets = %d, %v", len(rows), err)
	}
	if rows[0].Kid != domain.Recipient().String() {
		t.Fatalf("kid = %s, want the runtime domain recipient", rows[0].Kid)
	}
	if env, err := secretbox.Open(domain, rows[0].Ciphertext); err != nil || env["OPENAI_API_KEY"] != "sk-live" {
		t.Fatalf("domain open = %v, %v", env, err)
	}
	if _, err := secretbox.Open(fleet, rows[0].Ciphertext); err == nil {
		t.Fatal("the fleet identity apid holds opened a runtime secret sealed to the domain")
	}
	if err := apidserver.RuntimeSealDomainBlocksRekey(); err == nil {
		t.Fatal("apid's rekey runner may still re-seal runtime secrets in domain mode")
	}
}

func TestRuntimeSecretRecipientFollowsTheWriteMode(t *testing.T) {
	fleet, restore := apidserver.WithTestIdentities(t)
	defer restore()
	domain := mustSealIdentity(t)
	for _, tc := range []struct {
		mode sealdomain.WriteMode
		want *age.X25519Recipient
	}{
		{sealdomain.WriteAuto, fleet.Recipient()},
		{sealdomain.WriteFleet, fleet.Recipient()},
		{sealdomain.WriteDomain, domain.Recipient()},
	} {
		withRuntimeWrites(t, domain.Recipient(), tc.mode)
		if got := apidserver.RuntimeSecretRecipient(); got == nil || got.String() != tc.want.String() {
			t.Errorf("mode %q: runtime recipient = %v, want %s", tc.mode, got, tc.want)
		}
	}
}

func TestLoadSealDomainsRuntime(t *testing.T) {
	domain := mustSealIdentity(t)
	pub := filepath.Join(t.TempDir(), "runtime.age.pub")
	if err := secretbox.WriteRecipientFile(pub, domain); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	withOutboundRecipients(t, nil, false, nil, "")
	withRuntimeWrites(t, nil, "")
	for _, tc := range []struct {
		name    string
		env     map[string]string
		wantErr bool
	}{
		{name: "nothing staged", env: map[string]string{}},
		{name: "recipient staged, fleet writes", env: map[string]string{"FAAS_SEAL_DOMAIN_RUNTIME_RECIPIENT_PATH": pub}},
		{name: "domain writes", env: map[string]string{"FAAS_SEAL_DOMAIN_RUNTIME_RECIPIENT_PATH": pub, "FAAS_SEAL_DOMAIN_RUNTIME_WRITES": "domain"}},
		{name: "domain writes without the recipient", env: map[string]string{"FAAS_SEAL_DOMAIN_RUNTIME_WRITES": "domain"}, wantErr: true},
		{name: "dual is refused for runtime", env: map[string]string{"FAAS_SEAL_DOMAIN_RUNTIME_RECIPIENT_PATH": pub, "FAAS_SEAL_DOMAIN_RUNTIME_WRITES": "dual"}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := apidserver.LoadSealDomains(func(k string) string { return tc.env[k] }, log)
			if (err != nil) != tc.wantErr {
				t.Fatalf("LoadSealDomains error = %v, want error %v", err, tc.wantErr)
			}
		})
	}
}

func withRuntimeWrites(t *testing.T, domain *age.X25519Recipient, mode sealdomain.WriteMode) {
	t.Helper()
	prevDomain, prevMode := *apidserver.RuntimeDomainRecipient, *apidserver.RuntimeWriteMode
	t.Cleanup(func() { *apidserver.RuntimeDomainRecipient, *apidserver.RuntimeWriteMode = prevDomain, prevMode })
	*apidserver.RuntimeDomainRecipient = nil
	if domain != nil {
		*apidserver.RuntimeDomainRecipient = func() *age.X25519Recipient { return domain }
	}
	*apidserver.RuntimeWriteMode = mode
}
