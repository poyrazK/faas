package apidserver_test

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// TestStorageCredential_FollowsTheStorageDomain pins who opens a Gregale S3
// secret access key apid seals at each step of the storage rollout, and the
// key id it files the credential under (ADR 20261022111812): the fleet's
// while the fleet key still opens it, so s3-gatewayd's startup rekey leaves
// it alone, and the domain's once only the domain does.
func TestStorageCredential_FollowsTheStorageDomain(t *testing.T) {
	fleet, domain := mustSealIdentity(t), mustSealIdentity(t)
	const secret = "0123456789abcdef0123456789abcdef01234567"
	for _, tc := range edgeDomainSteps {
		t.Run(tc.name, func(t *testing.T) {
			withStorageRecipients(t, fleet.Recipient(), tc.staged, domain.Recipient(), tc.mode)
			recipients, kid, err := apidserver.StorageCredentialRecipients()
			if tc.unavailable {
				if err == nil {
					t.Fatal("got recipients with none staged for the write mode")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			wantKID := fleet.Recipient().String()
			if !tc.fleetOpens {
				wantKID = domain.Recipient().String()
			}
			if kid != wantKID {
				t.Fatalf("key id = %q, want %q", kid, wantKID)
			}
			sealed, err := secretbox.SealBytesTo(recipients, s3gateway.CredentialSecretNamespace, []byte(secret), 64)
			if err != nil {
				t.Fatal(err)
			}
			assertOpens(t, sealed, s3gateway.CredentialSecretNamespace, secret, map[*age.X25519Identity]bool{fleet: tc.fleetOpens, domain: tc.domainOpens})
		})
	}
}

func TestLoadSealDomainsStorage(t *testing.T) {
	domain := mustSealIdentity(t)
	pub := filepath.Join(t.TempDir(), "storage.age.pub")
	if err := secretbox.WriteRecipientFile(pub, domain); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	withOutboundRecipients(t, nil, false, nil, "")
	withSourceRecipients(t, nil, false, nil, "")
	withBuildRecipients(t, nil, false, nil, "")
	withEdgeRecipients(t, nil, false, nil, "")
	withAlertingRecipients(t, nil, false, nil, "")
	withStorageRecipients(t, nil, false, nil, "")
	for _, tc := range []struct {
		name    string
		env     map[string]string
		wantErr bool
		staged  bool
	}{
		{name: "nothing staged", env: map[string]string{}},
		{name: "recipient staged", env: map[string]string{"FAAS_SEAL_DOMAIN_STORAGE_RECIPIENT_PATH": pub}, staged: true},
		{name: "unknown mode", env: map[string]string{"FAAS_SEAL_DOMAIN_STORAGE_WRITES": "both"}, wantErr: true},
		{name: "domain mode without the recipient", env: map[string]string{"FAAS_SEAL_DOMAIN_STORAGE_WRITES": "domain"}, wantErr: true},
		{name: "domain mode with the recipient", env: map[string]string{"FAAS_SEAL_DOMAIN_STORAGE_WRITES": "domain", "FAAS_SEAL_DOMAIN_STORAGE_RECIPIENT_PATH": pub}, staged: true},
		{name: "missing recipient file", env: map[string]string{"FAAS_SEAL_DOMAIN_STORAGE_RECIPIENT_PATH": pub + ".missing"}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := apidserver.LoadSealDomains(func(k string) string { return tc.env[k] }, log)
			if (err != nil) != tc.wantErr {
				t.Fatalf("LoadSealDomains error = %v, want error %v", err, tc.wantErr)
			}
			if !tc.wantErr && ((*apidserver.StorageDomainRecipient) != nil) != tc.staged {
				t.Fatalf("domain recipient staged = %v, want %v", (*apidserver.StorageDomainRecipient) != nil, tc.staged)
			}
		})
	}
}

func withStorageRecipients(t *testing.T, fleet *age.X25519Recipient, staged bool, domain *age.X25519Recipient, mode sealdomain.WriteMode) {
	t.Helper()
	prevFleet, prevDomain, prevMode := *apidserver.SetSecretRecipient, *apidserver.StorageDomainRecipient, *apidserver.StorageWriteMode
	t.Cleanup(func() {
		*apidserver.SetSecretRecipient, *apidserver.StorageDomainRecipient, *apidserver.StorageWriteMode = prevFleet, prevDomain, prevMode
	})
	*apidserver.SetSecretRecipient = nil
	if fleet != nil {
		*apidserver.SetSecretRecipient = func() *age.X25519Recipient { return fleet }
	}
	*apidserver.StorageDomainRecipient = nil
	if staged {
		*apidserver.StorageDomainRecipient = func() *age.X25519Recipient { return domain }
	}
	*apidserver.StorageWriteMode = mode
}
