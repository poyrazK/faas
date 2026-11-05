package apidserver_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// edgeDomainSteps are the edge domain's rollout steps and who opens what apid
// seals at each (ADR 20261022111812).
var edgeDomainSteps = []struct {
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
}

// TestPublicAuthBasic_FollowsTheEdgeDomain pins who can open an app's
// basic-auth credential apid seals at each step of the edge rollout.
func TestPublicAuthBasic_FollowsTheEdgeDomain(t *testing.T) {
	fleet, domain := mustSealIdentity(t), mustSealIdentity(t)
	for _, tc := range edgeDomainSteps {
		t.Run(tc.name, func(t *testing.T) {
			e := apidserver.Setup(t, api.PlanPro)
			withEdgeRecipients(t, fleet.Recipient(), tc.staged, domain.Recipient(), tc.mode)
			app := apidserver.SeedAppForAudit(t, e, "edge-basic-app")
			rec := e.Do(t, http.MethodPatch, "/v1/apps/"+app.Slug, api.UpdateAppRequest{PublicAuth: &api.PublicAuthBlock{
				Mode: api.AppPublicAuthModeBasic, BasicUser: "editor", BasicPass: "edge-domain-pass",
			}}, nil)
			if tc.unavailable {
				if rec.Code != http.StatusServiceUnavailable {
					t.Fatalf("PATCH = %d %s, want 503", rec.Code, rec.Body.String())
				}
				return
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("PATCH = %d %s", rec.Code, rec.Body.String())
			}
			row, err := (*e.Store()).AppBySlug(context.Background(), app.Slug)
			if err != nil {
				t.Fatal(err)
			}
			for who, want := range map[*age.X25519Identity]bool{fleet: tc.fleetOpens, domain: tc.domainOpens} {
				ns, plain, err := secretbox.OpenBytes(who, row.PublicAuthBasicSealed)
				opened := err == nil && ns == api.AppPublicAuthBasicSealNamespace && string(plain) == "editor\nedge-domain-pass"
				if opened != want {
					t.Errorf("%s opens = %v, want %v", who.Recipient(), opened, want)
				}
			}
		})
	}
}

// TestLogDrainAuthHeader_FollowsTheEdgeDomain pins the same for a log drain's
// authentication header, which customer and standard drains share.
func TestLogDrainAuthHeader_FollowsTheEdgeDomain(t *testing.T) {
	fleet, domain := mustSealIdentity(t), mustSealIdentity(t)
	for _, tc := range edgeDomainSteps {
		t.Run(tc.name, func(t *testing.T) {
			withEdgeRecipients(t, fleet.Recipient(), tc.staged, domain.Recipient(), tc.mode)
			sealed, err := apidserver.SealAppLogDrainAuthHeader("Authorization: Bearer drain-token")
			if tc.unavailable {
				if err == nil {
					t.Fatal("sealed with no recipient for the write mode")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for who, want := range map[*age.X25519Identity]bool{fleet: tc.fleetOpens, domain: tc.domainOpens} {
				ns, plain, err := secretbox.OpenBytes(who, sealed)
				opened := err == nil && ns == "APP_LOG_DRAIN_AUTH_HEADER" && string(plain) == "Authorization: Bearer drain-token"
				if opened != want {
					t.Errorf("%s opens = %v, want %v", who.Recipient(), opened, want)
				}
			}
		})
	}
}

func TestLoadSealDomainsEdge(t *testing.T) {
	domain := mustSealIdentity(t)
	pub := filepath.Join(t.TempDir(), "edge.age.pub")
	if err := secretbox.WriteRecipientFile(pub, domain); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	withOutboundRecipients(t, nil, false, nil, "")
	withSourceRecipients(t, nil, false, nil, "")
	withBuildRecipients(t, nil, false, nil, "")
	withEdgeRecipients(t, nil, false, nil, "")
	for _, tc := range []struct {
		name    string
		env     map[string]string
		wantErr bool
		staged  bool
	}{
		{name: "nothing staged", env: map[string]string{}},
		{name: "recipient staged", env: map[string]string{"FAAS_SEAL_DOMAIN_EDGE_RECIPIENT_PATH": pub}, staged: true},
		{name: "unknown mode", env: map[string]string{"FAAS_SEAL_DOMAIN_EDGE_WRITES": "both"}, wantErr: true},
		{name: "domain mode without the recipient", env: map[string]string{"FAAS_SEAL_DOMAIN_EDGE_WRITES": "domain"}, wantErr: true},
		{name: "domain mode with the recipient", env: map[string]string{"FAAS_SEAL_DOMAIN_EDGE_WRITES": "domain", "FAAS_SEAL_DOMAIN_EDGE_RECIPIENT_PATH": pub}, staged: true},
		{name: "missing recipient file", env: map[string]string{"FAAS_SEAL_DOMAIN_EDGE_RECIPIENT_PATH": pub + ".missing"}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := apidserver.LoadSealDomains(func(k string) string { return tc.env[k] }, log)
			if (err != nil) != tc.wantErr {
				t.Fatalf("LoadSealDomains error = %v, want error %v", err, tc.wantErr)
			}
			if !tc.wantErr && ((*apidserver.EdgeDomainRecipient) != nil) != tc.staged {
				t.Fatalf("domain recipient staged = %v, want %v", (*apidserver.EdgeDomainRecipient) != nil, tc.staged)
			}
		})
	}
}

func withEdgeRecipients(t *testing.T, fleet *age.X25519Recipient, staged bool, domain *age.X25519Recipient, mode sealdomain.WriteMode) {
	t.Helper()
	prevFleet, prevDomain, prevMode := *apidserver.SetSecretRecipient, *apidserver.EdgeDomainRecipient, *apidserver.EdgeWriteMode
	t.Cleanup(func() {
		*apidserver.SetSecretRecipient, *apidserver.EdgeDomainRecipient, *apidserver.EdgeWriteMode = prevFleet, prevDomain, prevMode
	})
	*apidserver.SetSecretRecipient = nil
	if fleet != nil {
		*apidserver.SetSecretRecipient = func() *age.X25519Recipient { return fleet }
	}
	*apidserver.EdgeDomainRecipient = nil
	if staged {
		*apidserver.EdgeDomainRecipient = func() *age.X25519Recipient { return domain }
	}
	*apidserver.EdgeWriteMode = mode
}
