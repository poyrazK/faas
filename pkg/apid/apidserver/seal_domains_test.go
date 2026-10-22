package apidserver_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/outbound"
	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestSealOutboundCredential_FollowsTheDomainRollout pins who can open an
// outbound credential apid seals at each step of ADR 20261022111812.
func TestSealOutboundCredential_FollowsTheDomainRollout(t *testing.T) {
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
			withOutboundRecipients(t, fleet.Recipient(), tc.staged, domain.Recipient(), tc.mode)
			rec := httptest.NewRecorder()
			sealed, ok := apidserver.SealOutboundCredential(rec, "Bearer sk-domain")
			if tc.unavailable {
				if ok || rec.Code != http.StatusServiceUnavailable {
					t.Fatalf("seal = %v, status %d; want 503", ok, rec.Code)
				}
				return
			}
			if !ok {
				t.Fatalf("seal failed: %d %s", rec.Code, rec.Body.String())
			}
			for who, want := range map[*age.X25519Identity]bool{fleet: tc.fleetOpens, domain: tc.domainOpens} {
				ns, plain, err := secretbox.OpenBytes(who, sealed)
				if opened := err == nil && ns == outbound.ManagedAuthorizationSealNamespace && string(plain) == "Bearer sk-domain"; opened != want {
					t.Errorf("%s opens = %v, want %v", who.Recipient(), opened, want)
				}
			}
		})
	}
}

func TestLoadSealDomains(t *testing.T) {
	domain := mustSealIdentity(t)
	pub := filepath.Join(t.TempDir(), "outbound.age.pub")
	if err := secretbox.WriteRecipientFile(pub, domain); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	withOutboundRecipients(t, nil, false, nil, "")
	for _, tc := range []struct {
		name    string
		env     map[string]string
		wantErr bool
		staged  bool
	}{
		{name: "nothing staged", env: map[string]string{}},
		{name: "recipient staged", env: map[string]string{"FAAS_SEAL_DOMAIN_OUTBOUND_RECIPIENT_PATH": pub}, staged: true},
		{name: "unknown mode", env: map[string]string{"FAAS_SEAL_DOMAIN_OUTBOUND_WRITES": "both"}, wantErr: true},
		{name: "domain mode without the recipient", env: map[string]string{"FAAS_SEAL_DOMAIN_OUTBOUND_WRITES": "domain"}, wantErr: true},
		{name: "domain mode with the recipient", env: map[string]string{"FAAS_SEAL_DOMAIN_OUTBOUND_WRITES": "domain", "FAAS_SEAL_DOMAIN_OUTBOUND_RECIPIENT_PATH": pub}, staged: true},
		{name: "missing recipient file", env: map[string]string{"FAAS_SEAL_DOMAIN_OUTBOUND_RECIPIENT_PATH": pub + ".missing"}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := apidserver.LoadSealDomains(func(k string) string { return tc.env[k] }, log)
			if (err != nil) != tc.wantErr {
				t.Fatalf("LoadSealDomains error = %v, want error %v", err, tc.wantErr)
			}
			if !tc.wantErr && ((*apidserver.OutboundDomainRecipient) != nil) != tc.staged {
				t.Fatalf("domain recipient staged = %v, want %v", (*apidserver.OutboundDomainRecipient) != nil, tc.staged)
			}
		})
	}
}

// TestSetGithubWebhookSecret_StoresASealedCopy pins the ADR 20261022111812
// fix for the plaintext webhook secret: the admin route writes a copy githubd
// opens, and refuses to write when it cannot seal.
func TestSetGithubWebhookSecret_StoresASealedCopy(t *testing.T) {
	fleet := mustSealIdentity(t)
	secret := []byte("0123456789abcdef0123456789abcdef")
	body := `{"installation_id":4242,"secret_hex":"` + hex.EncodeToString(secret) + `"}`
	post := func(t *testing.T) (*httptest.ResponseRecorder, *state.MemStore) {
		srv, store, cookie := apidserver.NewForceHarness(t, nil)
		req := httptest.NewRequest(http.MethodPost, "/v1/admin/github-webhook-secrets", strings.NewReader(body))
		req.AddCookie(cookie)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "test-webhook-secret")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec, store
	}
	setSecretRecipient := func(t *testing.T, r *age.X25519Recipient) {
		prev := *apidserver.SetSecretRecipient
		t.Cleanup(func() { *apidserver.SetSecretRecipient = prev })
		*apidserver.SetSecretRecipient = nil
		if r != nil {
			*apidserver.SetSecretRecipient = func() *age.X25519Recipient { return r }
		}
	}

	t.Run("sealed copy", func(t *testing.T) {
		setSecretRecipient(t, fleet.Recipient())
		rec, store := post(t)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
		}
		got, err := store.GetGithubWebhookSecret(context.Background(), 4242)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got.Value, secret) {
			t.Fatal("the plaintext column the previous release reads was not written")
		}
		opened, err := sealdomain.OpenGithubWebhookSecret([]*age.X25519Identity{fleet}, got.Sealed)
		if err != nil || !bytes.Equal(opened, secret) {
			t.Fatalf("sealed copy opens to %q, %v", opened, err)
		}
	})
	t.Run("no recipient", func(t *testing.T) {
		setSecretRecipient(t, nil)
		rec, store := post(t)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", rec.Code)
		}
		if _, err := store.GetGithubWebhookSecret(context.Background(), 4242); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("a secret was stored without its sealed copy: %v", err)
		}
	})
}

func withOutboundRecipients(t *testing.T, fleet *age.X25519Recipient, staged bool, domain *age.X25519Recipient, mode sealdomain.WriteMode) {
	t.Helper()
	prevFleet, prevDomain, prevMode := *apidserver.OutboundCredentialRecipient, *apidserver.OutboundDomainRecipient, *apidserver.OutboundWriteMode
	t.Cleanup(func() {
		*apidserver.OutboundCredentialRecipient, *apidserver.OutboundDomainRecipient, *apidserver.OutboundWriteMode = prevFleet, prevDomain, prevMode
	})
	*apidserver.OutboundCredentialRecipient = nil
	if fleet != nil {
		*apidserver.OutboundCredentialRecipient = func() *age.X25519Recipient { return fleet }
	}
	*apidserver.OutboundDomainRecipient = nil
	if staged {
		*apidserver.OutboundDomainRecipient = func() *age.X25519Recipient { return domain }
	}
	*apidserver.OutboundWriteMode = mode
}

func mustSealIdentity(t *testing.T) *age.X25519Identity {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
