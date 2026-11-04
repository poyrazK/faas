package apidserver_test

import (
	"context"
	"crypto/x509"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func configureDomainDNS(t *testing.T) {
	t.Helper()
	a, aaaa, caa := *apidserver.ALookupFunc, *apidserver.AAAALookupFunc, *apidserver.CAALookupFunc
	cname, apps, owned, dial := *apidserver.CnameLookupFunc, *apidserver.AppsDomainFunc, *apidserver.OwnedDomainsFunc, *apidserver.DialCertFunc
	t.Cleanup(func() {
		*apidserver.ALookupFunc, *apidserver.AAAALookupFunc, *apidserver.CAALookupFunc = a, aaaa, caa
		*apidserver.CnameLookupFunc, *apidserver.AppsDomainFunc, *apidserver.OwnedDomainsFunc, *apidserver.DialCertFunc = cname, apps, owned, dial
	})
	*apidserver.OwnedDomainsFunc = func() []string { return []string{"gregale.dev"} }
	*apidserver.AppsDomainFunc = func() string { return "gregale.dev" }
	*apidserver.CAALookupFunc = func(context.Context, string) ([]string, error) { return nil, nil }
	*apidserver.DialCertFunc = func(context.Context, string) (*x509.Certificate, error) {
		return &x509.Certificate{NotAfter: time.Now().Add(24 * time.Hour)}, nil
	}
	t.Setenv("FAAS_CUSTOM_DOMAIN_TLS", "on_demand")
	t.Setenv("FAAS_CUSTOM_DOMAIN_TARGET", "edge.gregale.dev")
	t.Setenv("FAAS_CUSTOM_DOMAIN_ADDRESSES", "8.8.8.8")
}

func TestCustomDomainDNSRejectsProxiedOwnedApex(t *testing.T) {
	configureDomainDNS(t)
	*apidserver.CnameLookupFunc = func(context.Context, string) (string, error) { return "gregale.dev.", nil }
	*apidserver.ALookupFunc = func(context.Context, string) ([]string, error) { return []string{"1.1.1.1"}, nil }
	*apidserver.AAAALookupFunc = func(context.Context, string) ([]string, error) { return nil, nil }
	got := apidserver.CheckPointsToGregale(t.Context(), "shop.example.com")
	if got.Status != apidserver.ProbeFail {
		t.Fatalf("accepted proxied owned apex despite dedicated target: %+v", got)
	}
}

func TestDomainDoctorPreservesVerificationOnAddressLookupTimeout(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "both families timeout", true: "AAAA timeout after valid A"}[partial], func(t *testing.T) {
			e := apidserver.Setup(t, api.PlanPro)
			appID := apidserver.MustSeedApp(t, e, "timeout-apex")
			store := *e.Store()
			domain, err := store.CreateCustomDomain(t.Context(), "customer.example.com", appID, "token")
			if err != nil {
				t.Fatal(err)
			}
			if err = store.MarkDomainVerified(t.Context(), domain.Domain); err != nil {
				t.Fatal(err)
			}
			configureDomainDNS(t)
			*apidserver.CnameLookupFunc = func(context.Context, string) (string, error) { return domain.Domain + ".", nil }
			timeout := func(context.Context, string) ([]string, error) {
				return nil, &net.DNSError{Err: "timeout", IsTimeout: true, IsTemporary: true}
			}
			*apidserver.ALookupFunc, *apidserver.AAAALookupFunc = timeout, timeout
			if partial {
				*apidserver.ALookupFunc = func(context.Context, string) ([]string, error) { return []string{"8.8.8.8"}, nil }
			}
			if got := apidserver.CheckPointsToGregale(t.Context(), domain.Domain); string(got.Status) != "pending" {
				t.Fatalf("transient lookup was conclusive: %+v", got)
			}
			if err = (*e.S()).RunDoctorForDomain(t.Context(), slog.New(slog.DiscardHandler), domain.Domain); err != nil {
				t.Fatal(err)
			}
			got, err := store.DomainByName(t.Context(), domain.Domain)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Verified() {
				t.Fatalf("transient DNS revoked domain: %+v", got)
			}
		})
	}
}

func TestDomainDoctorPersistsRoutingAndIPv6Results(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "no AAAA conflict", true: "stray AAAA"}[conflict], func(t *testing.T) {
			e := apidserver.Setup(t, api.PlanPro)
			appID := apidserver.MustSeedApp(t, e, "doctor-observation")
			store := *e.Store()
			domain, err := store.CreateCustomDomain(t.Context(), "observe.example.com", appID, "token")
			if err != nil {
				t.Fatal(err)
			}
			configureDomainDNS(t)
			*apidserver.CnameLookupFunc = func(context.Context, string) (string, error) { return "edge.gregale.dev.", nil }
			*apidserver.ALookupFunc = func(context.Context, string) ([]string, error) { return []string{"8.8.8.8"}, nil }
			*apidserver.AAAALookupFunc = func(context.Context, string) ([]string, error) {
				if conflict {
					return []string{"2606:4700:4700::1111"}, nil
				}
				return nil, &net.DNSError{IsNotFound: true}
			}
			if err = (*e.S()).RunDoctorForDomain(t.Context(), slog.New(slog.DiscardHandler), domain.Domain); err != nil {
				t.Fatal(err)
			}
			obs, err := store.GetDoctorObservation(t.Context(), domain.Domain)
			if err != nil {
				t.Fatal(err)
			}
			if !obs.PointsToGregale || obs.IPv6Conflict != conflict {
				t.Fatalf("incorrect observation: %+v", obs)
			}
		})
	}
}

func TestDomainDoctorCAAResolverFailureStaysPending(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "caa-timeout")
	store := *e.Store()
	domain, err := store.CreateCustomDomain(t.Context(), "caa.example.com", appID, "token")
	if err != nil {
		t.Fatal(err)
	}
	configureDomainDNS(t)
	*apidserver.CnameLookupFunc = func(context.Context, string) (string, error) { return "edge.gregale.dev.", nil }
	*apidserver.ALookupFunc = func(context.Context, string) ([]string, error) { return []string{"8.8.8.8"}, nil }
	*apidserver.AAAALookupFunc = func(context.Context, string) ([]string, error) { return nil, nil }
	*apidserver.CAALookupFunc = func(context.Context, string) ([]string, error) {
		return nil, &net.DNSError{Err: "timeout", IsTimeout: true}
	}
	if err = (*e.S()).RunDoctorForDomain(t.Context(), slog.New(slog.DiscardHandler), domain.Domain); err != nil {
		t.Fatal(err)
	}
	obs, err := store.GetDoctorObservation(t.Context(), domain.Domain)
	if err != nil {
		t.Fatal(err)
	}
	report := apidserver.DoctorReportFromObs(domain, obs, false)
	for _, check := range report.Checks {
		if check.Name == "caa_permits" {
			if check.Status != "pending" {
				t.Fatalf("resolver failure became CAA permission: %+v", check)
			}
			return
		}
	}
	t.Fatal("missing CAA check")
}
