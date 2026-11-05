package apidserver_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// TestSetGithubWebhookSecret_FollowsTheSourceDomain pins who can open the
// sealed copy of a GitHub webhook secret at each step of the source domain's
// rollout (ADR 20261022111812).
func TestSetGithubWebhookSecret_FollowsTheSourceDomain(t *testing.T) {
	fleet, domain := mustSealIdentity(t), mustSealIdentity(t)
	secret := []byte("0123456789abcdef0123456789abcdef")
	body := `{"installation_id":4343,"secret_hex":"` + hex.EncodeToString(secret) + `"}`
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
			withSourceRecipients(t, fleet.Recipient(), tc.staged, domain.Recipient(), tc.mode)
			srv, store, cookie := apidserver.NewForceHarness(t, nil)
			req := httptest.NewRequest(http.MethodPost, "/v1/admin/github-webhook-secrets", strings.NewReader(body))
			req.AddCookie(cookie)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "test-source-domain-"+strings.ReplaceAll(tc.name, " ", "-"))
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if tc.unavailable {
				if rec.Code != http.StatusServiceUnavailable {
					t.Fatalf("status = %d, want 503", rec.Code)
				}
				return
			}
			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
			}
			got, err := store.GetGithubWebhookSecret(context.Background(), 4343)
			if err != nil {
				t.Fatal(err)
			}
			for who, want := range map[*age.X25519Identity]bool{fleet: tc.fleetOpens, domain: tc.domainOpens} {
				opened, err := sealdomain.OpenGithubWebhookSecret([]*age.X25519Identity{who}, got.Sealed)
				if ok := err == nil && bytes.Equal(opened, secret); ok != want {
					t.Errorf("%s opens = %v, want %v", who.Recipient(), ok, want)
				}
			}
		})
	}
}

func TestLoadSealDomainsSource(t *testing.T) {
	domain := mustSealIdentity(t)
	pub := filepath.Join(t.TempDir(), "source.age.pub")
	if err := secretbox.WriteRecipientFile(pub, domain); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	withOutboundRecipients(t, nil, false, nil, "")
	withSourceRecipients(t, nil, false, nil, "")
	for _, tc := range []struct {
		name    string
		env     map[string]string
		wantErr bool
		staged  bool
	}{
		{name: "nothing staged", env: map[string]string{}},
		{name: "recipient staged", env: map[string]string{"FAAS_SEAL_DOMAIN_SOURCE_RECIPIENT_PATH": pub}, staged: true},
		{name: "unknown mode", env: map[string]string{"FAAS_SEAL_DOMAIN_SOURCE_WRITES": "both"}, wantErr: true},
		{name: "domain mode without the recipient", env: map[string]string{"FAAS_SEAL_DOMAIN_SOURCE_WRITES": "domain"}, wantErr: true},
		{name: "domain mode with the recipient", env: map[string]string{"FAAS_SEAL_DOMAIN_SOURCE_WRITES": "domain", "FAAS_SEAL_DOMAIN_SOURCE_RECIPIENT_PATH": pub}, staged: true},
		{name: "missing recipient file", env: map[string]string{"FAAS_SEAL_DOMAIN_SOURCE_RECIPIENT_PATH": pub + ".missing"}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := apidserver.LoadSealDomains(func(k string) string { return tc.env[k] }, log)
			if (err != nil) != tc.wantErr {
				t.Fatalf("LoadSealDomains error = %v, want error %v", err, tc.wantErr)
			}
			if !tc.wantErr && ((*apidserver.SourceDomainRecipient) != nil) != tc.staged {
				t.Fatalf("domain recipient staged = %v, want %v", (*apidserver.SourceDomainRecipient) != nil, tc.staged)
			}
		})
	}
}

func withSourceRecipients(t *testing.T, fleet *age.X25519Recipient, staged bool, domain *age.X25519Recipient, mode sealdomain.WriteMode) {
	t.Helper()
	prevFleet, prevDomain, prevMode := *apidserver.SetSecretRecipient, *apidserver.SourceDomainRecipient, *apidserver.SourceWriteMode
	t.Cleanup(func() {
		*apidserver.SetSecretRecipient, *apidserver.SourceDomainRecipient, *apidserver.SourceWriteMode = prevFleet, prevDomain, prevMode
	})
	*apidserver.SetSecretRecipient = nil
	if fleet != nil {
		*apidserver.SetSecretRecipient = func() *age.X25519Recipient { return fleet }
	}
	*apidserver.SourceDomainRecipient = nil
	if staged {
		*apidserver.SourceDomainRecipient = func() *age.X25519Recipient { return domain }
	}
	*apidserver.SourceWriteMode = mode
}
