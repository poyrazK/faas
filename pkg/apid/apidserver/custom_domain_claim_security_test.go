package apidserver_test

import (
	"context"
	"crypto/x509"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func TestCertificateFailureMailUsesCurrentAppOrganization(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	unrelated, err := store.CreateAccount(t.Context(), "unrelated-company-admin@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	corp, err := store.CreateOrg(t.Context(), state.Org{Slug: "domain-corp-a", Name: "Corp A", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateOrg(t.Context(), state.Org{Slug: "domain-corp-b", Name: "Corp B", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range []state.Org{corp, other} {
		if err = store.AddOrgMember(t.Context(), o.ID, (*e.Acct()).ID, state.OrgRoleOwner, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err = store.AddOrgMember(t.Context(), other.ID, unrelated.ID, state.OrgRoleAdmin, nil); err != nil {
		t.Fatal(err)
	}
	currentOwner, err := store.CreateAccount(t.Context(), "current-app-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.AddOrgMember(t.Context(), corp.ID, currentOwner.ID, state.OrgRoleAdmin, nil); err != nil {
		t.Fatal(err)
	}
	if err = store.TransferOrgOwnership(t.Context(), corp.ID, (*e.Acct()).ID, currentOwner.ID); err != nil {
		t.Fatal(err)
	}
	viewer, err := store.CreateAccount(t.Context(), "app-viewer@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.AddOrgMember(t.Context(), corp.ID, viewer.ID, state.OrgRoleViewer, nil); err != nil {
		t.Fatal(err)
	}
	if err = store.RemoveOrgMember(t.Context(), corp.ID, (*e.Acct()).ID); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, OrgID: corp.ID, Slug: "corp-a-secret", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	const host = "private-app.corp-a.example.com"
	if _, err = store.CreateCustomDomain(t.Context(), host, app.ID, "proof"); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkDomainVerified(t.Context(), host); err != nil {
		t.Fatal(err)
	}
	if err = store.UpdateCustomDomainCertStatus(t.Context(), host, state.CustomDomainCertFailed, time.Time{}, "private deployment error", time.Now().Add(-20*time.Minute)); err != nil {
		t.Fatal(err)
	}
	configureDomainDNS(t)
	t.Setenv("FAAS_CUSTOM_DOMAIN_TLS", "")
	*apidserver.ALookupFunc = func(context.Context, string) ([]string, error) { return []string{"8.8.8.8"}, nil }
	*apidserver.AAAALookupFunc = func(context.Context, string) ([]string, error) { return nil, &net.DNSError{IsNotFound: true} }
	*apidserver.CnameLookupFunc = func(context.Context, string) (string, error) { return "edge.gregale.dev.", nil }
	*apidserver.DialCertFunc = func(context.Context, string) (*x509.Certificate, error) {
		return nil, errors.New("private deployment error")
	}
	mailer := &apidserver.RecordingMailer{}
	*(*e.S()).Mailer() = mailer
	if err = (*e.S()).RunDoctorForDomain(t.Context(), slog.New(slog.DiscardHandler), host); err != nil {
		t.Fatal(err)
	}
	messages := mailer.Snapshot()
	if len(messages) == 0 {
		t.Fatal("failure email was not exercised")
	}
	if len(messages) != 1 || len(messages[0].To) != 1 || messages[0].To[0] != currentOwner.Email {
		t.Fatalf("certificate notice must reach only the current owning organization's owners/admins: %+v", messages)
	}
}

func TestCustomDomainDoctorDiscardsPreviousClaimResults(t *testing.T) {
	t.Setenv("FAAS_DOMAIN_DOCTOR_ENABLED", "1")
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	oldApp := apidserver.MustSeedApp(t, e, "old-doctor-claim")
	account, err := store.CreateAccount(t.Context(), "replacement-doctor@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	nextApp, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "replacement-doctor", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	const host = "doctor-claim.example.com"
	if _, err = store.CreateCustomDomain(t.Context(), host, oldApp, "old-proof"); err != nil {
		t.Fatal(err)
	}
	configureDomainDNS(t)
	*apidserver.ALookupFunc = func(context.Context, string) ([]string, error) { return []string{"1.1.1.1"}, nil }
	*apidserver.AAAALookupFunc = func(context.Context, string) ([]string, error) { return nil, &net.DNSError{IsNotFound: true} }
	var swapErr error
	var freshHealthy bool
	*apidserver.CnameLookupFunc = func(ctx context.Context, _ string) (string, error) {
		replacement, err := store.TakeOverCustomDomainClaim(ctx, host, nextApp.ID, "", "new-proof", 100, 500)
		swapErr = err
		if swapErr == nil {
			// Preserve cancellation but bind the simulated new owner's proof
			// to its replacement claim; the outer doctor keeps the old claim.
			replacementCtx := state.WithCustomDomainProbe(ctx, replacement)
			var matched bool
			matched, swapErr = store.MarkDomainVerifiedIfChallenge(replacementCtx, host, "new-proof")
			if !matched && swapErr == nil {
				swapErr = errors.New("replacement did not verify")
			}
		}
		// Subsequent lookups see the repaired DNS, while this in-flight lookup
		// completes with the previous claim's answer.
		*apidserver.CnameLookupFunc = func(context.Context, string) (string, error) { return "edge.gregale.dev.", nil }
		freshHealthy = string(apidserver.CheckPointsToGregale(ctx, host).Status) == "ok"
		return "old-target.example.net.", nil
	}
	response := e.Do(t, http.MethodGet, "/v1/domains/"+host+"/doctor", nil, nil)
	if swapErr != nil || !freshHealthy {
		t.Fatalf("replacement setup failed: healthy=%v err=%v", freshHealthy, swapErr)
	}
	if response.Code != http.StatusNotFound {
		t.Fatalf("obsolete doctor request=%d %s", response.Code, response.Body.String())
	}
	current, err := store.DomainByName(t.Context(), host)
	if err != nil {
		t.Fatal(err)
	}
	if !current.Verified() || current.AppID != nextApp.ID || current.CertStatus == state.CustomDomainCertDNSDrifted {
		t.Fatalf("stale probe changed replacement: %+v", current)
	}
	if _, err = store.GetDoctorObservation(t.Context(), host); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stale probe cached its observation: %v", err)
	}
}

func TestDomainDoctorHonorsCAAValidationMethods(t *testing.T) {
	for _, tc := range []struct {
		name, mode, record string
		want               *bool
	}{
		{"on-demand DNS-only denied", "on_demand", `0 issue "letsencrypt.org; validationmethods=dns-01"`, boolPointer(false)},
		{"legacy DNS allowed", "", `0 issue "letsencrypt.org; validationmethods=dns-01"`, boolPointer(true)},
		{"on-demand HTTP allowed", "on_demand", `0 issue "letsencrypt.org; validationmethods=http-01"`, boolPointer(true)},
		{"account restriction uncertain", "on_demand", `0 issue "letsencrypt.org; accounturi=https://acme.example/acct/123"`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := apidserver.Setup(t, api.PlanPro)
			store := *e.Store()
			app := apidserver.MustSeedApp(t, e, "caa-methods")
			const host = "caa-method.example.com"
			if _, err := store.CreateCustomDomain(t.Context(), host, app, "proof"); err != nil {
				t.Fatal(err)
			}
			configureDomainDNS(t)
			t.Setenv("FAAS_CUSTOM_DOMAIN_TLS", tc.mode)
			*apidserver.ALookupFunc = func(context.Context, string) ([]string, error) { return []string{"8.8.8.8"}, nil }
			*apidserver.AAAALookupFunc = func(context.Context, string) ([]string, error) { return nil, &net.DNSError{IsNotFound: true} }
			*apidserver.CnameLookupFunc = func(context.Context, string) (string, error) { return "edge.gregale.dev.", nil }
			*apidserver.CAALookupFunc = func(context.Context, string) ([]string, error) { return []string{tc.record}, nil }
			if err := (*e.S()).RunDoctorForDomain(t.Context(), slog.New(slog.DiscardHandler), host); err != nil {
				t.Fatal(err)
			}
			obs, err := store.GetDoctorObservation(t.Context(), host)
			if err != nil {
				t.Fatal(err)
			}
			if (obs.CAAPermits == nil) != (tc.want == nil) || tc.want != nil && *obs.CAAPermits != *tc.want {
				t.Fatalf("CAA observation=%+v, want %v", obs, tc.want)
			}
		})
	}
}

func boolPointer(value bool) *bool { return &value }

type verifyWildcardBeforeClaimStore struct {
	MemStore *state.MemStore
	before   func()
}

func (s *verifyWildcardBeforeClaimStore) CreateCustomDomainIfUnderQuota(ctx context.Context, domain, app, token string, appLimit, accountLimit int) (state.CustomDomain, error) {
	s.before()
	return s.MemStore.CreateCustomDomainIfUnderQuota(ctx, domain, app, token, appLimit, accountLimit)
}

func (s *verifyWildcardBeforeClaimStore) CreateCustomDomainIfUnderQuotaWithActivity(ctx context.Context, domain, app, token string, appLimit, accountLimit int, activity state.OrgActivity) (state.CustomDomain, int64, error) {
	s.before()
	return s.MemStore.CreateCustomDomainIfUnderQuotaWithActivity(ctx, domain, app, token, appLimit, accountLimit, activity)
}

func TestCustomDomainCreateRechecksWildcardAfterPreflight(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	app := apidserver.MustSeedApp(t, e, "wildcard-preflight")
	account, err := store.CreateAccount(t.Context(), "wildcard-zone-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "wildcard-zone", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	const wildcard = "*.preflight.example.com"
	const host = "live.preflight.example.com"
	if _, err = store.CreateCustomDomain(t.Context(), wildcard, owner.ID, "proof"); err != nil {
		t.Fatal(err)
	}
	verified := false
	fake := &verifyWildcardBeforeClaimStore{MemStore: store, before: func() {
		verified = true
		if err := store.MarkDomainVerified(t.Context(), wildcard); err != nil {
			t.Fatal(err)
		}
	}}
	*(*e.S()).Store() = statetest.NewMem(fake)
	response := e.Do(t, http.MethodPost, "/v1/domains", api.CreateCustomDomainRequest{Domain: host, AppID: app}, nil)
	if !verified {
		t.Fatal("publication did not reach the post-preflight interleaving")
	}
	if response.Code != http.StatusConflict {
		t.Fatalf("foreign child claim returned %d: %s", response.Code, response.Body.String())
	}
	if _, err = store.DomainByName(t.Context(), host); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("rejected create published a shadow: %v", err)
	}
	policy := gateway.NewOnDemandTLSPolicy(store, "gregale.dev", nil, nil)
	if allowed, decision := policy.Decide(t.Context(), host); !allowed {
		t.Fatalf("rejected create darkened the wildcard's certificate: %s", decision)
	}
}
