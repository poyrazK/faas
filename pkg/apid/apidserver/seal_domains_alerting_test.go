package apidserver_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// TestNotificationChannel_FollowsTheAlertingDomain pins who can open a
// channel destination apid seals at each step of the alerting rollout (ADR
// 20261022111812).
func TestNotificationChannel_FollowsTheAlertingDomain(t *testing.T) {
	fleet, domain := mustSealIdentity(t), mustSealIdentity(t)
	for _, tc := range edgeDomainSteps {
		t.Run(tc.name, func(t *testing.T) {
			e := apidserver.SetupAlerts(t, api.PlanPro)
			withAlertingRecipients(t, fleet.Recipient(), tc.staged, domain.Recipient(), tc.mode)
			rec := e.Do(t, http.MethodPost, "/v1/notification-channels", api.CreateNotificationChannelRequest{Name: "ops", Kind: "slack", SlackWebhookURL: testSlackURL}, nil)
			if tc.unavailable {
				if rec.Code != http.StatusServiceUnavailable {
					t.Fatalf("create = %d %s, want 503", rec.Code, rec.Body)
				}
				return
			}
			if rec.Code != http.StatusCreated {
				t.Fatalf("create = %d %s", rec.Code, rec.Body)
			}
			var created api.NotificationChannelResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
				t.Fatal(err)
			}
			row, err := (*e.Store()).GetNotificationChannel(context.Background(), (*e.Acct()).ID, created.ID)
			if err != nil {
				t.Fatal(err)
			}
			assertOpens(t, row.TargetSealed, alertchannels.SealNamespace, testSlackURL, map[*age.X25519Identity]bool{fleet: tc.fleetOpens, domain: tc.domainOpens})
		})
	}
}

// TestAlertingSealers_FollowTheAlertingDomain covers the preset webhook
// secret and the two MCP readiness keys.
func TestAlertingSealers_FollowTheAlertingDomain(t *testing.T) {
	fleet, domain := mustSealIdentity(t), mustSealIdentity(t)
	pagerDutyKey := "0123456789abcdef0123456789abcdef"
	for _, tc := range edgeDomainSteps {
		t.Run(tc.name, func(t *testing.T) {
			withAlertingRecipients(t, fleet.Recipient(), tc.staged, domain.Recipient(), tc.mode)
			for _, s := range []struct {
				name, namespace, value string
				seal                   func(string) ([]byte, *api.Problem)
			}{
				{"preset webhook secret", "alert_rule_secret", "whsec-preset", func(v string) ([]byte, *api.Problem) {
					return apidserver.SealPresetWebhookSecret(context.Background(), v)
				}},
				{"MCP fallback secret", "alert_rule_secret", "whsec-fallback", func(v string) ([]byte, *api.Problem) {
					return apidserver.SealMCPFallbackSecret(context.Background(), v)
				}},
				{"MCP PagerDuty key", api.MCPReadinessPagerDutySecretNamespace, pagerDutyKey, func(v string) ([]byte, *api.Problem) {
					return apidserver.SealMCPPagerDutyKey(context.Background(), v)
				}},
			} {
				sealed, prob := s.seal(s.value)
				if tc.unavailable {
					if prob == nil {
						t.Fatalf("%s: sealed with no recipient for the write mode", s.name)
					}
					continue
				}
				if prob != nil {
					t.Fatalf("%s: %v", s.name, prob)
				}
				assertOpens(t, sealed, s.namespace, s.value, map[*age.X25519Identity]bool{fleet: tc.fleetOpens, domain: tc.domainOpens})
			}
		})
	}
}

func TestLoadSealDomainsAlerting(t *testing.T) {
	domain := mustSealIdentity(t)
	pub := filepath.Join(t.TempDir(), "alerting.age.pub")
	if err := secretbox.WriteRecipientFile(pub, domain); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	withOutboundRecipients(t, nil, false, nil, "")
	withSourceRecipients(t, nil, false, nil, "")
	withBuildRecipients(t, nil, false, nil, "")
	withEdgeRecipients(t, nil, false, nil, "")
	withAlertingRecipients(t, nil, false, nil, "")
	for _, tc := range []struct {
		name    string
		env     map[string]string
		wantErr bool
		staged  bool
	}{
		{name: "nothing staged", env: map[string]string{}},
		{name: "recipient staged", env: map[string]string{"FAAS_SEAL_DOMAIN_ALERTING_RECIPIENT_PATH": pub}, staged: true},
		{name: "unknown mode", env: map[string]string{"FAAS_SEAL_DOMAIN_ALERTING_WRITES": "both"}, wantErr: true},
		{name: "domain mode without the recipient", env: map[string]string{"FAAS_SEAL_DOMAIN_ALERTING_WRITES": "domain"}, wantErr: true},
		{name: "domain mode with the recipient", env: map[string]string{"FAAS_SEAL_DOMAIN_ALERTING_WRITES": "domain", "FAAS_SEAL_DOMAIN_ALERTING_RECIPIENT_PATH": pub}, staged: true},
		{name: "missing recipient file", env: map[string]string{"FAAS_SEAL_DOMAIN_ALERTING_RECIPIENT_PATH": pub + ".missing"}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := apidserver.LoadSealDomains(func(k string) string { return tc.env[k] }, log)
			if (err != nil) != tc.wantErr {
				t.Fatalf("LoadSealDomains error = %v, want error %v", err, tc.wantErr)
			}
			if !tc.wantErr && ((*apidserver.AlertingDomainRecipient) != nil) != tc.staged {
				t.Fatalf("domain recipient staged = %v, want %v", (*apidserver.AlertingDomainRecipient) != nil, tc.staged)
			}
		})
	}
}

func assertOpens(t *testing.T, sealed []byte, namespace, value string, want map[*age.X25519Identity]bool) {
	t.Helper()
	for who, wantOpen := range want {
		ns, plain, err := secretbox.OpenBytes(who, sealed)
		if opened := err == nil && ns == namespace && string(plain) == value; opened != wantOpen {
			t.Errorf("%s opens %s = %v, want %v", who.Recipient(), namespace, opened, wantOpen)
		}
	}
}

func withAlertingRecipients(t *testing.T, fleet *age.X25519Recipient, staged bool, domain *age.X25519Recipient, mode sealdomain.WriteMode) {
	t.Helper()
	prevFleet, prevDomain, prevMode := *apidserver.SetSecretRecipient, *apidserver.AlertingDomainRecipient, *apidserver.AlertingWriteMode
	t.Cleanup(func() {
		*apidserver.SetSecretRecipient, *apidserver.AlertingDomainRecipient, *apidserver.AlertingWriteMode = prevFleet, prevDomain, prevMode
	})
	*apidserver.SetSecretRecipient = nil
	if fleet != nil {
		*apidserver.SetSecretRecipient = func() *age.X25519Recipient { return fleet }
	}
	*apidserver.AlertingDomainRecipient = nil
	if staged {
		*apidserver.AlertingDomainRecipient = func() *age.X25519Recipient { return domain }
	}
	*apidserver.AlertingWriteMode = mode
}
