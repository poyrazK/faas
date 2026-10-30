package apidserver_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestAccountValues_FollowTheAccountDomain pins who opens an account value
// apid seals at each step of the account rollout (ADR 20261022111812), the
// key id filed beside a workflow plan, and that apid opens with the domain
// first while keeping the fleet identity for values sealed before it.
func TestAccountValues_FollowTheAccountDomain(t *testing.T) {
	fleet, domain := mustSealIdentity(t), mustSealIdentity(t)
	for _, tc := range edgeDomainSteps {
		t.Run(tc.name, func(t *testing.T) {
			withAccountDomain(t, fleet, tc.staged, domain, tc.mode)
			recipients, err := apidserver.AccountRecipients(fleet.Recipient())
			planRecipients, kid, planErr := apidserver.WorkflowPlanRecipients()
			if tc.unavailable {
				if err == nil || planErr == nil {
					t.Fatal("sealed with no domain identity staged for the write mode")
				}
				return
			}
			if err != nil || planErr != nil {
				t.Fatal(err, planErr)
			}
			sealed, err := secretbox.SealBytesTo(recipients, api.InboundWebhookSecretNamespace, []byte("whsec_account"), api.InboundWebhookSigningSecretMaxBytes)
			if err != nil {
				t.Fatal(err)
			}
			assertOpens(t, sealed, api.InboundWebhookSecretNamespace, "whsec_account", map[*age.X25519Identity]bool{fleet: tc.fleetOpens, domain: tc.domainOpens})
			if len(planRecipients) != len(recipients) {
				t.Fatalf("plan recipients = %d, want the account recipients (%d)", len(planRecipients), len(recipients))
			}
			wantKID := fleet.Recipient().String()
			if !tc.fleetOpens {
				wantKID = domain.Recipient().String()
			}
			if kid != wantKID {
				t.Fatalf("plan key id = %q, want %q", kid, wantKID)
			}
			ids := apidserver.AccountIdentities()
			if !apidserver.HoldsIdentityFor(ids, kid) {
				t.Fatalf("apid holds no identity for the plan key id %q", kid)
			}
			if tc.staged && ids[0].Recipient().String() != domain.Recipient().String() {
				t.Fatalf("first account identity = %s, want the domain's", ids[0].Recipient())
			}
		})
	}
}

// TestAccountReaders_OpenDomainOnlyValues seals each account value to the
// domain alone and opens it through apid's own reader for it, so a reader
// left on the fleet identities fails here.
func TestAccountReaders_OpenDomainOnlyValues(t *testing.T) {
	fleet, domain := mustSealIdentity(t), mustSealIdentity(t)
	withAccountDomain(t, fleet, true, domain, sealdomain.WriteDomain)
	ctx := context.Background()

	webhook, prob := apidserver.SealInboundWebhookSigningSecret("whsec_inbound_account")
	if prob != nil {
		t.Fatal(prob)
	}
	assertOpens(t, webhook, api.InboundWebhookSecretNamespace, "whsec_inbound_account", map[*age.X25519Identity]bool{fleet: false, domain: true})
	// Stripe verification opens the secret before it checks the signature:
	// a bad-signature answer means the secret opened.
	_, _, prob = apidserver.VerifyInboundWebhook(ctx, state.InboundWebhookEndpoint{Provider: state.InboundWebhookProviderStripe, SigningSecretSealed: webhook}, http.Header{}, []byte(`{}`), time.Now())
	if prob == nil || prob.Code != api.ErrInboundWebhookBadSignature().Code {
		t.Fatalf("inbound webhook verify = %+v; want the secret opened and the signature refused", prob)
	}

	seal, open := apidserver.StatementInvoicesHostSecrets()
	namespace := api.BillingProviderKeyNamespace("stripe", "acct-1")
	key, err := seal(namespace, []byte("sk_test_account"), api.BillingProviderKeyMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	assertOpens(t, key, namespace, "sk_test_account", map[*age.X25519Identity]bool{fleet: false, domain: true})
	if ns, plain, err := open(key); err != nil || ns != namespace || string(plain) != "sk_test_account" {
		t.Fatalf("billing key open = %q, %v", ns, err)
	}

	ack, prob := apidserver.SealMCPPagerDutyWebhookSecret(ctx, "pd-ack-secret")
	if prob != nil {
		t.Fatal(prob)
	}
	assertOpens(t, ack, api.MCPReadinessPagerDutyWebhookSecretNamespace, "pd-ack-secret", map[*age.X25519Identity]bool{fleet: false, domain: true})
}

// TestMFA_EnrollsAndConfirmsInTheAccountDomain runs /enroll and /confirm
// with apid writing account values to the domain alone: the stored TOTP
// secret opens only with the domain identity, and /confirm opens it.
func TestMFA_EnrollsAndConfirmsInTheAccountDomain(t *testing.T) {
	e := apidserver.SetupWithMFA(t, api.PlanPro, true, false)
	fleet := (*apidserver.MfaIdentity)()
	domain := mustSealIdentity(t)
	withAccountDomain(t, fleet, true, domain, sealdomain.WriteDomain)

	e.GenerateEnrolledAccount(t)
	acct, err := (*e.Store()).AccountByID(context.Background(), e.Acct().ID)
	if err != nil {
		t.Fatal(err)
	}
	if !acct.MFAEnrolled() {
		t.Fatal("account not enrolled after /confirm")
	}
	if _, err := secretbox.Open(fleet, acct.MFASecretEncrypted); err == nil {
		t.Fatal("the fleet identity opens the stored TOTP secret")
	}
	if env, err := secretbox.Open(domain, acct.MFASecretEncrypted); err != nil || env["MFA_SECRET"] == "" {
		t.Fatalf("domain open of the TOTP secret: %v", err)
	}
}

func TestLoadSealDomainsAccount(t *testing.T) {
	domain := mustSealIdentity(t)
	path := filepath.Join(t.TempDir(), "account.age")
	if err := secretbox.WriteHostKeyAtPath(path, domain); err != nil {
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
	withAccountDomain(t, nil, false, nil, "")
	for _, tc := range []struct {
		name    string
		env     map[string]string
		wantErr bool
		staged  bool
	}{
		{name: "nothing staged", env: map[string]string{}},
		{name: "identity staged", env: map[string]string{"FAAS_SEAL_DOMAIN_ACCOUNT_IDENTITY_PATH": path}, staged: true},
		{name: "unknown mode", env: map[string]string{"FAAS_SEAL_DOMAIN_ACCOUNT_WRITES": "both"}, wantErr: true},
		{name: "domain mode without the identity", env: map[string]string{"FAAS_SEAL_DOMAIN_ACCOUNT_WRITES": "domain"}, wantErr: true},
		{name: "domain mode with the identity", env: map[string]string{"FAAS_SEAL_DOMAIN_ACCOUNT_WRITES": "domain", "FAAS_SEAL_DOMAIN_ACCOUNT_IDENTITY_PATH": path}, staged: true},
		{name: "missing identity file", env: map[string]string{"FAAS_SEAL_DOMAIN_ACCOUNT_IDENTITY_PATH": path + ".missing"}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := apidserver.LoadSealDomains(func(k string) string { return tc.env[k] }, log)
			if (err != nil) != tc.wantErr {
				t.Fatalf("LoadSealDomains error = %v, want error %v", err, tc.wantErr)
			}
			if !tc.wantErr && (len(*apidserver.AccountDomainIdentities) > 0) != tc.staged {
				t.Fatalf("domain identity staged = %v, want %v", len(*apidserver.AccountDomainIdentities) > 0, tc.staged)
			}
		})
	}
}

// withAccountDomain stages the account rollout: fleet is apid's fleet
// identity (what it opens with and seals to before the domain), domain the
// account identity once staged.
func withAccountDomain(t *testing.T, fleet *age.X25519Identity, staged bool, domain *age.X25519Identity, mode sealdomain.WriteMode) {
	t.Helper()
	prevIdents, prevIdent, prevRecipient, prevSecret := *apidserver.MfaIdentities, *apidserver.MfaIdentity, *apidserver.MfaRecipient, *apidserver.SetSecretRecipient
	prevDomain, prevMode := *apidserver.AccountDomainIdentities, *apidserver.AccountWriteMode
	t.Cleanup(func() {
		*apidserver.MfaIdentities, *apidserver.MfaIdentity, *apidserver.MfaRecipient, *apidserver.SetSecretRecipient = prevIdents, prevIdent, prevRecipient, prevSecret
		*apidserver.AccountDomainIdentities, *apidserver.AccountWriteMode = prevDomain, prevMode
	})
	*apidserver.MfaIdentities, *apidserver.MfaIdentity, *apidserver.MfaRecipient, *apidserver.SetSecretRecipient = nil, nil, nil, nil
	if fleet != nil {
		*apidserver.MfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{fleet} }
		*apidserver.MfaIdentity = func() *age.X25519Identity { return fleet }
		*apidserver.MfaRecipient = func() *age.X25519Recipient { return fleet.Recipient() }
		*apidserver.SetSecretRecipient = func() *age.X25519Recipient { return fleet.Recipient() }
	}
	*apidserver.AccountDomainIdentities = nil
	if staged {
		*apidserver.AccountDomainIdentities = []*age.X25519Identity{domain}
	}
	*apidserver.AccountWriteMode = mode
}

// TestCloneSealRecipient pins who project environment clone values are
// sealed to: the fleet recipient at every step short of domain-only, then
// the account domain alone, and apid opens either through the identity the
// row's key id names.
func TestCloneSealRecipient(t *testing.T) {
	fleet, domain := mustSealIdentity(t), mustSealIdentity(t)
	for _, tc := range edgeDomainSteps {
		t.Run(tc.name, func(t *testing.T) {
			withAccountDomain(t, fleet, tc.staged, domain, tc.mode)
			got := apidserver.CloneSealRecipient()
			if tc.unavailable {
				if got != nil {
					t.Fatalf("clone recipient = %s with no domain identity in domain mode", got)
				}
				return
			}
			want := fleet.Recipient().String()
			if tc.mode == sealdomain.WriteDomain {
				want = domain.Recipient().String()
			}
			if got == nil || got.String() != want {
				t.Fatalf("clone recipient = %v, want %s", got, want)
			}
			if !apidserver.HoldsIdentityFor(apidserver.AccountIdentities(), got.String()) {
				t.Fatalf("apid holds no identity for the clone key id %s", got)
			}
		})
	}
}
