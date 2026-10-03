package main

import (
	"context"
	"crypto/x509"
	"errors"
	"log/slog"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func notFoundLookup(context.Context, string) ([]string, error) {
	return nil, &net.DNSError{Err: "no such host", IsNotFound: true}
}

func staticLookup(addrs ...string) func(context.Context, string) ([]string, error) {
	return func(context.Context, string) ([]string, error) { return addrs, nil }
}

func withCNAME(t *testing.T, cname string) {
	t.Helper()
	prev := cnameLookupFunc
	cnameLookupFunc = func(context.Context, string) (string, error) { return cname, nil }
	t.Cleanup(func() { cnameLookupFunc = prev })
}

func TestCustomDomainDNSRecords(t *testing.T) {
	withSeams(t, nil, nil, nil, "gregale.dev")
	for _, tc := range []struct {
		name      string
		target    string
		addresses string
		domain    string
		want      []api.DNSRecordInstruction
	}{
		{
			name: "no target configured falls back to the apps domain", domain: "shop.example.com",
			want: []api.DNSRecordInstruction{
				{Type: "TXT", Name: "_faas-verify.shop.example.com", Value: "tok", Purpose: api.DNSRecordPurposeVerification},
				{Type: "CNAME", Name: "shop.example.com", Value: "gregale.dev", Purpose: api.DNSRecordPurposeRouting},
			},
		},
		{
			name: "edge target with apex alternatives", target: "edge.gregale.dev",
			addresses: "203.0.113.10,2001:db8::10", domain: "shop.example.com",
			want: []api.DNSRecordInstruction{
				{Type: "TXT", Name: "_faas-verify.shop.example.com", Value: "tok", Purpose: api.DNSRecordPurposeVerification},
				{Type: "CNAME", Name: "shop.example.com", Value: "edge.gregale.dev", Purpose: api.DNSRecordPurposeRouting},
				{Type: "A", Name: "shop.example.com", Value: "203.0.113.10", Purpose: api.DNSRecordPurposeRouting, Alternative: true},
				{Type: "AAAA", Name: "shop.example.com", Value: "2001:db8::10", Purpose: api.DNSRecordPurposeRouting, Alternative: true},
			},
		},
		{
			name: "wildcard routes the wildcard name and proves the zone", target: "edge.gregale.dev", domain: "*.tenants.example.com",
			want: []api.DNSRecordInstruction{
				{Type: "TXT", Name: "_faas-verify.tenants.example.com", Value: "tok", Purpose: api.DNSRecordPurposeVerification},
				{Type: "CNAME", Name: "*.tenants.example.com", Value: "edge.gregale.dev", Purpose: api.DNSRecordPurposeRouting},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FAAS_CUSTOM_DOMAIN_TARGET", tc.target)
			t.Setenv("FAAS_CUSTOM_DOMAIN_ADDRESSES", tc.addresses)
			got := customDomainDNSRecords(state.CustomDomain{Domain: tc.domain, ChallengeToken: "tok"})
			if !slices.Equal(got, tc.want) {
				t.Fatalf("records = %+v\nwant      %+v", got, tc.want)
			}
		})
	}
}

func TestCheckPointsToGregaleWithEdgeTarget(t *testing.T) {
	t.Setenv("FAAS_CUSTOM_DOMAIN_TARGET", "edge.gregale.dev")
	t.Setenv("FAAS_CUSTOM_DOMAIN_ADDRESSES", "203.0.113.10")
	for _, tc := range []struct {
		name    string
		cname   string
		a       []string
		want    probeStatus
		wantRem string
	}{
		{name: "cname to edge target", cname: "edge.gregale.dev.", want: probeOK},
		// The proxied apps-domain apex cannot complete ACME for the
		// customer's name once a direct edge target is configured.
		{name: "cname to apps apex", cname: "gregale.dev.", want: probeFail,
			wantRem: "Set CNAME shop.example.com → edge.gregale.dev (or, at a zone apex, A/AAAA → 203.0.113.10)"},
		// Go reports the queried name as its own canonical name when the
		// zone apex holds A records.
		{name: "apex A at edge", cname: "shop.example.com.", a: []string{"203.0.113.10"}, want: probeOK},
		{name: "apex A elsewhere", cname: "shop.example.com.", a: []string{"198.51.100.1"}, want: probeFail},
		{name: "apex A partly elsewhere", cname: "shop.example.com.", a: []string{"203.0.113.10", "198.51.100.1"}, want: probeFail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withSeams(t, staticLookup(tc.a...), notFoundLookup, nil, "gregale.dev")
			withCNAME(t, tc.cname)
			got := checkPointsToGregale(context.Background(), "shop.example.com")
			if got.Status != tc.want {
				t.Fatalf("status = %s (%s), want %s", got.Status, got.Detail, tc.want)
			}
			if tc.wantRem != "" && got.Remediation != tc.wantRem {
				t.Fatalf("remediation = %q, want %q", got.Remediation, tc.wantRem)
			}
		})
	}
}

func TestCheckAAAAConflictAcceptsEdgeAddresses(t *testing.T) {
	t.Setenv("FAAS_CUSTOM_DOMAIN_ADDRESSES", "203.0.113.10,2001:db8::10")
	withSeams(t, nil, staticLookup("2001:db8::10"), nil, "gregale.dev")
	if got := checkAAAAConflict(context.Background(), "shop.example.com"); got.Status != probeOK {
		t.Fatalf("AAAA at the edge = %s (%s), want ok", got.Status, got.Detail)
	}
	withSeams(t, nil, staticLookup("2001:db8::99"), nil, "gregale.dev")
	if got := checkAAAAConflict(context.Background(), "shop.example.com"); got.Status != probeFail {
		t.Fatalf("stray AAAA = %s, want fail", got.Status)
	}
}

func TestWithinOnDemandIssuanceGrace(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	grace := time.Duration(api.OnDemandTLSIssuanceGraceSeconds) * time.Second
	for _, tc := range []struct {
		name     string
		mode     string
		verified time.Time
		want     bool
	}{
		{name: "flag off", mode: "", verified: now.Add(-time.Minute), want: false},
		{name: "unverified", mode: "on_demand", want: false},
		{name: "just verified", mode: "on_demand", verified: now.Add(-time.Minute), want: true},
		{name: "grace elapsed", mode: "on_demand", verified: now.Add(-grace - time.Second), want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FAAS_CUSTOM_DOMAIN_TLS", tc.mode)
			d := state.CustomDomain{Domain: "shop.example.com", VerifiedAt: tc.verified}
			if got := withinOnDemandIssuanceGrace(d, now); got != tc.want {
				t.Fatalf("withinOnDemandIssuanceGrace = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestDoctorReportsPendingWhileEdgeIssues pins that the first port-443 probe
// after verification, which is what makes the edge obtain the certificate,
// does not record a failure (and send the failure email) when it times out.
func TestDoctorReportsPendingWhileEdgeIssues(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want state.CustomDomainCertStatus
	}{
		{mode: "on_demand", want: state.CustomDomainCertPending},
		{mode: "", want: state.CustomDomainCertFailed},
	} {
		t.Run("mode="+tc.mode, func(t *testing.T) {
			t.Setenv("FAAS_CUSTOM_DOMAIN_TLS", tc.mode)
			t.Setenv("FAAS_CUSTOM_DOMAIN_TARGET", "edge.gregale.dev")
			ctx := context.Background()
			store := state.NewMemStore()
			acct, err := store.CreateAccount(ctx, "edge@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "edge", RAMMB: 256, Status: state.AppActive})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.CreateCustomDomain(ctx, "shop.example.com", app.ID, "tok"); err != nil {
				t.Fatal(err)
			}
			if err := store.MarkDomainVerified(ctx, "shop.example.com"); err != nil {
				t.Fatal(err)
			}
			withSeams(t, staticLookup("203.0.113.10"), notFoundLookup, notFoundLookup, "gregale.dev")
			withCNAME(t, "edge.gregale.dev.")
			prevDial := dialCertFunc
			dialCertFunc = func(context.Context, string) (*x509.Certificate, error) {
				return nil, errors.Join(errCertFailure, context.DeadlineExceeded)
			}
			t.Cleanup(func() { dialCertFunc = prevDial })

			log := slog.New(slog.DiscardHandler)
			srv := &server{store: store, audit: newAuditor(store, log, nil), notif: noopNotifier{}}
			if err := srv.runDoctorForDomain(ctx, log, "shop.example.com"); err != nil {
				t.Fatal(err)
			}
			got, err := store.DomainByName(ctx, "shop.example.com")
			if err != nil {
				t.Fatal(err)
			}
			if got.CertStatus != tc.want {
				t.Fatalf("cert status = %q, want %q (last error %q)", got.CertStatus, tc.want, got.CertLastError)
			}
			if tc.want == state.CustomDomainCertPending && got.CertLastError != "" {
				t.Fatalf("pending status carries an error: %q", got.CertLastError)
			}
		})
	}
}

func TestDomainResponseCarriesDNSRecords(t *testing.T) {
	t.Setenv("FAAS_CUSTOM_DOMAIN_TARGET", "edge.gregale.dev")
	resp := domainResponse(state.CustomDomain{Domain: "shop.example.com", ChallengeToken: "tok"})
	if len(resp.DNSRecords) != 2 || !strings.EqualFold(resp.DNSRecords[1].Value, "edge.gregale.dev") {
		t.Fatalf("dns_records = %+v", resp.DNSRecords)
	}
}
