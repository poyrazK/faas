package apidserver_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
)

func domainRecoveryApp(t *testing.T, store *state.MemStore, email, slug string) state.App {
	t.Helper()
	a, err := store.CreateAccount(t.Context(), email, api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: a.ID, Slug: slug, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func domainRecoveryVerified(t *testing.T, store *state.MemStore, host, app, token string) state.CustomDomain {
	t.Helper()
	d, err := store.CreateCustomDomain(t.Context(), host, app, token)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.MarkDomainVerified(t.Context(), host); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestCustomDomainDashboardDiscardsReplacementDiagnostics(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	app := apidserver.MustSeedApp(t, e, "old-dashboard")
	const host = "dashboard-transfer.example.com"
	old, err := store.CreateCustomDomain(t.Context(), host, app, "old-proof")
	if err != nil {
		t.Fatal(err)
	}
	// renderAppDomains has already loaded and authorized this app and list.
	listed, err := store.ListDomainsForApp(t.Context(), app)
	if err != nil || len(listed) != 1 {
		t.Fatalf("list=%v err=%v", listed, err)
	}
	owner := domainRecoveryApp(t, store, "new-dashboard@example.com", "new-dashboard")
	if _, err = store.TakeOverCustomDomainClaim(t.Context(), host, owner.ID, "", "new-proof", 100, 500); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkDomainVerified(t.Context(), host); err != nil {
		t.Fatal(err)
	}
	const secret = "new-owner-private-origin-error"
	if err = store.UpsertDoctorObservation(t.Context(), state.DomainDoctorObservation{Domain: host, ObservedAt: time.Now(), CertState: "failed", LastError: secret}); err != nil {
		t.Fatal(err)
	}
	items := apidserver.ProjectDashboardDomainsForTest(*e.S(), t.Context(), slog.New(slog.DiscardHandler), "old-dashboard", listed)
	if len(items) != 1 {
		t.Fatalf("not exercised: %+v", items)
	}
	if items[0].Doctor != nil {
		t.Fatalf("old app %s/token %s receives replacement %s diagnostics: %+v", old.AppID, old.ChallengeToken, owner.ID, items[0].Doctor)
	}
}

type domainRecoveryAskStore struct {
	mem    *state.MemStore
	before func()
}

func (s *domainRecoveryAskStore) DomainByName(c context.Context, n string) (state.CustomDomain, error) {
	return s.mem.DomainByName(c, n)
}
func (s *domainRecoveryAskStore) WildcardDomainForHost(c context.Context, n string) (state.CustomDomain, error) {
	return s.mem.WildcardDomainForHost(c, n)
}
func (s *domainRecoveryAskStore) AppByID(c context.Context, n string) (state.App, error) {
	return s.mem.AppByID(c, n)
}
func (s *domainRecoveryAskStore) AccountByID(c context.Context, n string) (state.Account, error) {
	return s.mem.AccountByID(c, n)
}
func (s *domainRecoveryAskStore) OrgByID(c context.Context, n string) (state.Org, error) {
	return s.mem.OrgByID(c, n)
}
func (s *domainRecoveryAskStore) CustomDomainTLSHostAllowed(c context.Context, d, h string) (bool, error) {
	return s.mem.CustomDomainTLSHostAllowed(c, d, h)
}
func (s *domainRecoveryAskStore) AdmitCustomDomainTLSHost(c context.Context, a state.CustomDomainTLSAdmission) (state.CustomDomainTLSVerdict, error) {
	if s.before != nil {
		f := s.before
		s.before = nil
		f()
	}
	return s.mem.AdmitCustomDomainTLSHost(c, a)
}

func TestCustomDomainTLSAdmissionRejectsReplacementClaim(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	oldApp := apidserver.MustSeedApp(t, e, "old-tls-admission")
	other := domainRecoveryApp(t, store, "new-tls@example.com", "new-tls-admission")
	const wildcard = "*.admission-transfer.example.com"
	const host = "live.admission-transfer.example.com"
	domainRecoveryVerified(t, store, wildcard, oldApp, "old-proof")
	if err := store.SetCustomDomainTLSAllowedHosts(t.Context(), wildcard, []string{"live"}); err != nil {
		t.Fatal(err)
	}
	fake := &domainRecoveryAskStore{mem: store, before: func() {
		if err := store.DeleteCustomDomain(t.Context(), wildcard); err != nil {
			t.Fatal(err)
		}
		domainRecoveryVerified(t, store, wildcard, other.ID, "new-proof")
		if err := store.SetCustomDomainTLSAllowedHosts(t.Context(), wildcard, []string{"new-owner-only"}); err != nil {
			t.Fatal(err)
		}
	}}
	policy := gateway.NewOnDemandTLSPolicy(fake, "gregale.dev", nil, nil)
	allowed, decision := policy.Decide(t.Context(), host)
	freshAllowed, freshDecision := policy.Decide(t.Context(), host)
	if freshAllowed || freshDecision != gateway.OnDemandTLSDenyNotAllowlisted {
		t.Fatalf("new claim not restrictive: %v/%s", freshAllowed, freshDecision)
	}
	if allowed {
		t.Fatalf("obsolete permission admits using replacement claim: in-flight=%v/%s current=%v/%s", allowed, decision, freshAllowed, freshDecision)
	}
}

func TestCustomDomainCertificateDialTriesAlternateAddress(t *testing.T) {
	srv := httptest.NewTLSServer(http.NewServeMux())
	defer srv.Close()
	cert := srv.Certificate()
	host := cert.DNSNames[0]
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	conn, err := tls.Dial("tcp", srv.Listener.Addr().String(), &tls.Config{RootCAs: pool, ServerName: host, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatalf("healthy alternate endpoint is not healthy: %v", err)
	}
	_ = conn.Close()
	prevIP, prevDial := *apidserver.CertLookupIPFunc, *apidserver.CertDialContextFunc
	t.Cleanup(func() { *apidserver.CertLookupIPFunc = prevIP; *apidserver.CertDialContextFunc = prevDial })
	*apidserver.CertLookupIPFunc = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("1.1.1.1")}}, nil
	}
	var attempts []string
	*apidserver.CertDialContextFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		attempts = append(attempts, address)
		if strings.HasPrefix(address, "8.8.8.8:") {
			return nil, errors.New("first family/path unavailable")
		}
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	_, err = apidserver.DialCert(t.Context(), host)
	if len(attempts) < 2 {
		t.Fatalf("healthy alternate ignored: attempts=%v err=%v", attempts, err)
	}
}

func domainRecoveryFailedClaim(t *testing.T, store *state.MemStore, host, app, token string) {
	t.Helper()
	domainRecoveryVerified(t, store, host, app, token)
	if err := store.UpdateCustomDomainCertStatus(t.Context(), host, state.CustomDomainCertFailed, time.Time{}, "ongoing certificate failure", time.Now().Add(-20*time.Minute)); err != nil {
		t.Fatal(err)
	}
}

type domainRecoveryFlakyMailer struct {
	attempts, delivered int
	keys                []string
}

func (m *domainRecoveryFlakyMailer) Send(_ context.Context, message apidserver.Message) error {
	m.attempts++
	m.keys = append(m.keys, message.MessageID)
	if m.attempts == 1 {
		return mail.ErrTransient
	}
	m.delivered++
	return nil
}

func TestCertificateFailureEmailRetriesAfterTransportRecovery(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	app := apidserver.MustSeedApp(t, e, "failed-mail")
	const host = "mail-retry.example.com"
	domainRecoveryFailedClaim(t, store, host, app, "proof")
	mailer := &domainRecoveryFlakyMailer{}
	*(*e.S()).Mailer() = mailer
	log := slog.New(slog.DiscardHandler)
	if err := apidserver.SendCertIssuanceFailedEmailForTest(*e.S(), t.Context(), log, host); !errors.Is(err, mail.ErrTransient) {
		t.Fatalf("first attempt=%v", err)
	}
	if err := apidserver.SendCertIssuanceFailedEmailForTest(*e.S(), t.Context(), log, host); err != nil {
		t.Fatal(err)
	}
	if mailer.delivered != 1 {
		d, _ := store.DomainByName(t.Context(), host)
		t.Fatalf("transport recovered but alert cannot retry: attempts=%d delivered=%d cooldown=%v", mailer.attempts, mailer.delivered, d.CertFailureEmailAt)
	}
	if len(mailer.keys) != 2 || mailer.keys[0] != mailer.keys[1] {
		t.Fatalf("retry changed provider key: %v", mailer.keys)
	}
}

func TestCertificateFailureEmailKeyFollowsClaimOwner(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	app := apidserver.MustSeedApp(t, e, "old-mail-key")
	other := domainRecoveryApp(t, store, "new-mail-owner@example.com", "new-mail-key")
	const host = "mail-key-transfer.example.com"
	domainRecoveryFailedClaim(t, store, host, app, "old-proof")
	mailer := &apidserver.RecordingMailer{}
	*(*e.S()).Mailer() = mailer
	log := slog.New(slog.DiscardHandler)
	if err := apidserver.SendCertIssuanceFailedEmailForTest(*e.S(), t.Context(), log, host); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteCustomDomain(t.Context(), host); err != nil {
		t.Fatal(err)
	}
	domainRecoveryFailedClaim(t, store, host, other.ID, "new-proof")
	if err := apidserver.SendCertIssuanceFailedEmailForTest(*e.S(), t.Context(), log, host); err != nil {
		t.Fatal(err)
	}
	messages := mailer.Snapshot()
	if len(messages) != 2 {
		t.Fatalf("messages=%d", len(messages))
	}
	if messages[0].MessageID == messages[1].MessageID {
		t.Fatalf("different owning accounts/recipients share provider idempotency key %q: old=%v new=%v", messages[0].MessageID, messages[0].To, messages[1].To)
	}
}

func TestCustomDomainLegacyWildcardCAAUsesIssuewild(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	app := apidserver.MustSeedApp(t, e, "legacy-wildcard-caa")
	const host = "*.legacy-caa.example.com"
	if _, err := store.CreateCustomDomain(t.Context(), host, app, "proof"); err != nil {
		t.Fatal(err)
	}
	configureDomainDNS(t)
	t.Setenv("FAAS_CUSTOM_DOMAIN_TLS", "")
	*apidserver.ALookupFunc = func(context.Context, string) ([]string, error) { return []string{"8.8.8.8"}, nil }
	*apidserver.AAAALookupFunc = func(context.Context, string) ([]string, error) { return nil, &net.DNSError{IsNotFound: true} }
	*apidserver.CnameLookupFunc = func(context.Context, string) (string, error) { return "edge.gregale.dev.", nil }
	var caaHost string
	*apidserver.CAALookupFunc = func(_ context.Context, host string) ([]string, error) {
		caaHost = host
		return []string{"0 issue \"letsencrypt.org\"", "0 issuewild \";\""}, nil
	}
	if err := (*e.S()).RunDoctorForDomain(t.Context(), slog.New(slog.DiscardHandler), host); err != nil {
		t.Fatal(err)
	}
	obs, err := store.GetDoctorObservation(t.Context(), host)
	if err != nil {
		t.Fatal(err)
	}
	if obs.CAAPermits == nil || *obs.CAAPermits {
		t.Fatalf("legacy DNS-01 wildcard issuance shown permitted despite issuewild denial: %+v", obs)
	}
	if caaHost != "legacy-caa.example.com" {
		t.Fatalf("wildcard CAA queried at %q", caaHost)
	}
}
