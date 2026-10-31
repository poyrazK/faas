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

// TestRegistryAuth_FollowsTheBuildDomain pins who can open a private-registry
// password apid seals at each step of the build domain's rollout (ADR
// 20261022111812).
func TestRegistryAuth_FollowsTheBuildDomain(t *testing.T) {
	fleet, domain := mustSealIdentity(t), mustSealIdentity(t)
	for _, tc := range []struct {
		name        string
		staged      bool
		mode        sealdomain.WriteMode
		fleetOpens  bool
		domainOpens bool
		unavailable bool
	}{
		{name: "before the domain is staged", fleetOpens: true},
		{name: "staged: dual by default", staged: true, fleetOpens: true, domainOpens: true},
		{name: "staged, opted out", staged: true, mode: sealdomain.WriteFleet, fleetOpens: true},
		{name: "domain only", staged: true, mode: sealdomain.WriteDomain, domainOpens: true},
		{name: "domain only without the recipient", mode: sealdomain.WriteDomain, unavailable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := setupRegistry(t, api.PlanHobby)
			withBuildRecipients(t, fleet.Recipient(), tc.staged, domain.Recipient(), tc.mode)
			app := apidserver.CreateApp(t, e, "build-domain-app")
			rec := e.Do(t, "PUT", "/v1/apps/"+app.Slug+"/registry-credentials", api.PutAppRegistryCredentialRequest{
				Registry: "https://registry.gregale.dev", Username: "alice", Password: registryAuthMark,
			}, nil)
			if tc.unavailable {
				if rec.Code != 503 {
					t.Fatalf("PUT = %d, want 503", rec.Code)
				}
				return
			}
			if rec.Code != 200 {
				t.Fatalf("PUT = %d %s", rec.Code, rec.Body.String())
			}
			row, err := (*e.Store()).GetAppRegistryCredential(context.Background(), (*e.Acct()).ID, app.ID, "registry.gregale.dev")
			if err != nil {
				t.Fatal(err)
			}
			for who, want := range map[*age.X25519Identity]bool{fleet: tc.fleetOpens, domain: tc.domainOpens} {
				ns, plain, err := secretbox.OpenBytes(who, row.PasswordEncrypted)
				if opened := err == nil && ns == "registry_creds" && string(plain) == registryAuthMark; opened != want {
					t.Errorf("%s opens = %v, want %v", who.Recipient(), opened, want)
				}
			}
		})
	}
}

func TestLoadSealDomainsBuild(t *testing.T) {
	domain := mustSealIdentity(t)
	pub := filepath.Join(t.TempDir(), "build.age.pub")
	if err := secretbox.WriteRecipientFile(pub, domain); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	withOutboundRecipients(t, nil, false, nil, "")
	withSourceRecipients(t, nil, false, nil, "")
	withBuildRecipients(t, nil, false, nil, "")
	for _, tc := range []struct {
		name    string
		env     map[string]string
		wantErr bool
		staged  bool
	}{
		{name: "nothing staged", env: map[string]string{}},
		{name: "recipient staged", env: map[string]string{"FAAS_SEAL_DOMAIN_BUILD_RECIPIENT_PATH": pub}, staged: true},
		{name: "unknown mode", env: map[string]string{"FAAS_SEAL_DOMAIN_BUILD_WRITES": "both"}, wantErr: true},
		{name: "domain mode without the recipient", env: map[string]string{"FAAS_SEAL_DOMAIN_BUILD_WRITES": "domain"}, wantErr: true},
		{name: "domain mode with the recipient", env: map[string]string{"FAAS_SEAL_DOMAIN_BUILD_WRITES": "domain", "FAAS_SEAL_DOMAIN_BUILD_RECIPIENT_PATH": pub}, staged: true},
		{name: "missing recipient file", env: map[string]string{"FAAS_SEAL_DOMAIN_BUILD_RECIPIENT_PATH": pub + ".missing"}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := apidserver.LoadSealDomains(func(k string) string { return tc.env[k] }, log)
			if (err != nil) != tc.wantErr {
				t.Fatalf("LoadSealDomains error = %v, want error %v", err, tc.wantErr)
			}
			if !tc.wantErr && ((*apidserver.BuildDomainRecipient) != nil) != tc.staged {
				t.Fatalf("domain recipient staged = %v, want %v", (*apidserver.BuildDomainRecipient) != nil, tc.staged)
			}
		})
	}
}

func withBuildRecipients(t *testing.T, fleet *age.X25519Recipient, staged bool, domain *age.X25519Recipient, mode sealdomain.WriteMode) {
	t.Helper()
	prevFleet, prevDomain, prevMode := *apidserver.SetSecretRecipient, *apidserver.BuildDomainRecipient, *apidserver.BuildWriteMode
	t.Cleanup(func() {
		*apidserver.SetSecretRecipient, *apidserver.BuildDomainRecipient, *apidserver.BuildWriteMode = prevFleet, prevDomain, prevMode
	})
	*apidserver.SetSecretRecipient = nil
	if fleet != nil {
		*apidserver.SetSecretRecipient = func() *age.X25519Recipient { return fleet }
	}
	*apidserver.BuildDomainRecipient = nil
	if staged {
		*apidserver.BuildDomainRecipient = func() *age.X25519Recipient { return domain }
	}
	*apidserver.BuildWriteMode = mode
}
