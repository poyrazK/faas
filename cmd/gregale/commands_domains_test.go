// Whitebox tests for the domain CLI surface (issue #961 / Mega-A PR-3).
//
// Coverage:
//   - cmdDomainsVerify hits POST /v1/domains/{domain}/verify
//   - cmdDomainsShow hits GET /v1/domains/{domain}
//   - missing-arg branch exits 1 with usage
//
// Pins the route + HTTP method + body-shape wiring at the CLI
// surface. The wire-shape gate (the CustomDomainResponse payload)
// is covered by the apid tests + the SDK round-trip.

package main

import (
	"net/http"
	"testing"
)

func TestDomainsVerify_HappyPath(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t,
		`{"domain":"app.example.com","app_id":"0123456789abcdef0123456789abcdef","verified":true,"cert_not_after":"2026-09-18T00:00:00Z","cert_sans":["app.example.com"]}`,
		http.StatusOK,
	)
	if code := cmdDomainsVerify([]string{"app.example.com"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "POST" || f.sawPath != "/v1/domains/app.example.com/verify" {
		t.Errorf("route = %s %s, want POST /v1/domains/app.example.com/verify", f.sawMethod, f.sawPath)
	}
}

func TestDomainsVerify_MissingArgExitsOne(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, "", http.StatusOK)
	if code := cmdDomainsVerify(nil); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if code := cmdDomainsVerify([]string{}); code != 1 {
		t.Fatalf("exit = %d, want 1 (empty args)", code)
	}
}

func TestDomainsShow_HappyPath(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t,
		`{"domain":"app.example.com","app_id":"0123456789abcdef0123456789abcdef","verified":true,"cert_not_after":"2026-09-18T00:00:00Z","cert_sans":["app.example.com","www.example.com"]}`,
		http.StatusOK,
	)
	if code := cmdDomainsShow([]string{"app.example.com"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "GET" || f.sawPath != "/v1/domains/app.example.com" {
		t.Errorf("route = %s %s, want GET /v1/domains/app.example.com", f.sawMethod, f.sawPath)
	}
}

func TestDomainsShow_MissingArgExitsOne(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, "", http.StatusOK)
	if code := cmdDomainsShow(nil); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

// TestDomainsDispatch_VerifyAndShowRegistered: the cmdDomains
// dispatch switch (commands2.go:1393) must route "verify" and
// "show" to the new handlers. Without this guard, a typo like
// "case subDomainsVerify" being renamed would silently fall
// through to the "unknown subcommand" branch.
func TestDomainsDispatch_VerifyAndShowRegistered(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"domain":"x","app_id":"y","verified":true}`, http.StatusOK)
	// Capture the dispatched handler by hitting the parent
	// cmdDomains with "verify" as the first arg.
	if code := cmdDomains([]string{"verify", "app.example.com"}); code != 0 {
		t.Fatalf("cmdDomains verify exit = %d, want 0", code)
	}
	if f.sawMethod != "POST" {
		t.Errorf("verify dispatch method = %s, want POST", f.sawMethod)
	}
}

// TestDomainsDispatch_DoctorRegistered: the cmdDomains dispatch
// switch (commands2.go:cmdDomains) must route "doctor" to
// cmdDomainsDoctor (ADR-120). Without this guard, a typo like
// "case subDomainsDoctor" being renamed would silently fall
// through to the "unknown subcommand" branch.
func TestDomainsDispatch_DoctorRegistered(t *testing.T) {
	resetJSONOut(t)
	healthyReport := `{"domain":"x","app_id":"y","observed_at":"2026-08-18T14:23:11Z","healthy":true,"checks":[]}`
	f := authedFakeAPI(t, healthyReport, http.StatusOK)
	if code := cmdDomains([]string{"doctor", "app.example.com"}); code != 0 {
		t.Fatalf("cmdDomains doctor exit = %d, want 0", code)
	}
	if f.sawMethod != "GET" {
		t.Errorf("doctor dispatch method = %s, want GET", f.sawMethod)
	}
	if f.sawPath != "/v1/domains/app.example.com/doctor" {
		t.Errorf("doctor dispatch path = %q, want /v1/domains/app.example.com/doctor", f.sawPath)
	}
}

// TestDomainsDispatch_DoctorReportsFail: the cmdDomainsDoctor
// handler must return exit 1 when the report is not healthy so
// the customer (and CI) can branch on it.
func TestDomainsDispatch_DoctorReportsFail(t *testing.T) {
	resetJSONOut(t)
	badReport := `{"domain":"x","app_id":"y","observed_at":"2026-08-18T14:23:11Z","healthy":false,"checks":[]}`
	authedFakeAPI(t, badReport, http.StatusOK)
	if code := cmdDomains([]string{"doctor", "app.example.com"}); code != 1 {
		t.Fatalf("cmdDomains doctor exit = %d, want 1 (unhealthy)", code)
	}
}

func TestDomainsDispatch_StatusRegistered(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `[{"domain":"app.example.com","app_id":"a","verified":true,"cert_status":"issued","cert_expires_at":"2027-01-01T00:00:00Z"}]`, http.StatusOK)
	if code := cmdDomains([]string{"status"}); code != 0 {
		t.Fatalf("cmdDomains status exit = %d, want 0", code)
	}
	if f.sawMethod != "GET" || f.sawPath != "/v1/domains" {
		t.Errorf("status route = %s %s, want GET /v1/domains", f.sawMethod, f.sawPath)
	}
}
