package apidserver_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"filippo.io/age"
	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/executionpayload"
	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// TestDeliveryValues_FollowTheDeliveryDomain pins who opens each value apid
// seals for schedd at each step of the delivery rollout (ADR
// 20261022111812): app webhook signing secrets, commit-source connections
// and execution payloads, and the key id filed beside a payload, which
// names the identity schedd opens it with.
func TestDeliveryValues_FollowTheDeliveryDomain(t *testing.T) {
	fleet, domain := mustSealIdentity(t), mustSealIdentity(t)
	sourceID := uuid.NewString()
	const conn = "postgres://relay@db.example.com:5432/app?sslmode=verify-full"
	request := api.ResolvedExecutionRequest{Source: "def main(input, context): return input", Input: json.RawMessage(`{"n":1}`)}
	for _, tc := range edgeDomainSteps {
		t.Run(tc.name, func(t *testing.T) {
			withDeliveryRecipients(t, fleet.Recipient(), tc.staged, domain.Recipient(), tc.mode)
			opens := map[*age.X25519Identity]bool{fleet: tc.fleetOpens, domain: tc.domainOpens}

			secret, prob := apidserver.SealAppWebhookSecret("whsec_0123456789")
			commitRecipients, commitErr := apidserver.CommitConnectionRecipients()
			payloadRecipients, kid, payloadErr := apidserver.ExecutionPayloadRecipients()
			if tc.unavailable {
				if prob == nil || commitErr == nil || payloadErr == nil {
					t.Fatalf("sealed with none staged for the write mode: %v, %v, %v", prob, commitErr, payloadErr)
				}
				return
			}
			if prob != nil || commitErr != nil || payloadErr != nil {
				t.Fatalf("seal: %v, %v, %v", prob, commitErr, payloadErr)
			}
			assertOpens(t, secret, api.AppWebhookSecretNamespace, "whsec_0123456789", opens)

			connection, err := commit.SealConnectionTo(commitRecipients, sourceID, conn)
			if err != nil {
				t.Fatal(err)
			}
			assertOpens(t, connection, commit.ConnectionNamespace(sourceID), conn, opens)

			wantKID := fleet.Recipient().String()
			if !tc.fleetOpens {
				wantKID = domain.Recipient().String()
			}
			if kid != wantKID {
				t.Fatalf("payload key id = %q, want %q", kid, wantKID)
			}
			payload, err := executionpayload.SealRequestTo(payloadRecipients, request)
			if err != nil {
				t.Fatal(err)
			}
			for who, wantOpen := range opens {
				_, _, err := secretbox.OpenBytes(who, payload)
				if (err == nil) != wantOpen {
					t.Errorf("%s opens the payload = %v, want %v", who.Recipient(), err == nil, wantOpen)
				}
			}
			// schedd holds the domain identity first, then fleet and host.
			if _, err := executionpayload.DecodeRequest(context.Background(), []*age.X25519Identity{domain, fleet}, payload, kid); err != nil {
				t.Fatalf("schedd decode under key id %q: %v", kid, err)
			}
		})
	}
}

// TestCommitConnection_NeedsTheSharedFleetRecipient keeps the commit rule
// from before the domain: a connection is never sealed to a host's own
// recipient, which schedd on another node cannot open.
func TestCommitConnection_NeedsTheSharedFleetRecipient(t *testing.T) {
	host := mustSealIdentity(t)
	withDeliveryRecipients(t, host.Recipient(), false, nil, "")
	*apidserver.OutboundCredentialRecipient = nil
	if _, err := apidserver.CommitConnectionRecipients(); err == nil {
		t.Fatal("a commit connection was sealed to the host recipient")
	}
}

func TestLoadSealDomainsDelivery(t *testing.T) {
	domain := mustSealIdentity(t)
	pub := filepath.Join(t.TempDir(), "delivery.age.pub")
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
	withDeliveryRecipients(t, nil, false, nil, "")
	for _, tc := range []struct {
		name    string
		env     map[string]string
		wantErr bool
		staged  bool
	}{
		{name: "nothing staged", env: map[string]string{}},
		{name: "recipient staged", env: map[string]string{"FAAS_SEAL_DOMAIN_DELIVERY_RECIPIENT_PATH": pub}, staged: true},
		{name: "unknown mode", env: map[string]string{"FAAS_SEAL_DOMAIN_DELIVERY_WRITES": "both"}, wantErr: true},
		{name: "domain mode without the recipient", env: map[string]string{"FAAS_SEAL_DOMAIN_DELIVERY_WRITES": "domain"}, wantErr: true},
		{name: "domain mode with the recipient", env: map[string]string{"FAAS_SEAL_DOMAIN_DELIVERY_WRITES": "domain", "FAAS_SEAL_DOMAIN_DELIVERY_RECIPIENT_PATH": pub}, staged: true},
		{name: "missing recipient file", env: map[string]string{"FAAS_SEAL_DOMAIN_DELIVERY_RECIPIENT_PATH": pub + ".missing"}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := apidserver.LoadSealDomains(func(k string) string { return tc.env[k] }, log)
			if (err != nil) != tc.wantErr {
				t.Fatalf("LoadSealDomains error = %v, want error %v", err, tc.wantErr)
			}
			if !tc.wantErr && ((*apidserver.DeliveryDomainRecipient) != nil) != tc.staged {
				t.Fatalf("domain recipient staged = %v, want %v", (*apidserver.DeliveryDomainRecipient) != nil, tc.staged)
			}
		})
	}
}

// withDeliveryRecipients stages the delivery rollout: fleet is both the
// fleet recipient apid seals webhook secrets and payloads to and the shared
// one commit connections require.
func withDeliveryRecipients(t *testing.T, fleet *age.X25519Recipient, staged bool, domain *age.X25519Recipient, mode sealdomain.WriteMode) {
	t.Helper()
	prevFleet, prevShared := *apidserver.SetSecretRecipient, *apidserver.OutboundCredentialRecipient
	prevDomain, prevMode := *apidserver.DeliveryDomainRecipient, *apidserver.DeliveryWriteMode
	t.Cleanup(func() {
		*apidserver.SetSecretRecipient, *apidserver.OutboundCredentialRecipient = prevFleet, prevShared
		*apidserver.DeliveryDomainRecipient, *apidserver.DeliveryWriteMode = prevDomain, prevMode
	})
	*apidserver.SetSecretRecipient, *apidserver.OutboundCredentialRecipient = nil, nil
	if fleet != nil {
		*apidserver.SetSecretRecipient = func() *age.X25519Recipient { return fleet }
		*apidserver.OutboundCredentialRecipient = func() *age.X25519Recipient { return fleet }
	}
	*apidserver.DeliveryDomainRecipient = nil
	if staged {
		*apidserver.DeliveryDomainRecipient = func() *age.X25519Recipient { return domain }
	}
	*apidserver.DeliveryWriteMode = mode
}
