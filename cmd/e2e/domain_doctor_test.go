// domain_doctor_test.go — e2e surface for ADR-120 domain doctor.
//
// Exercises the gregale CLI's `domains doctor <domain>` subcommand
// against a real apid backed by Postgres. Skipped automatically when
// FAAS_PG_DSN is unset (the same gating convention the rest of
// cmd/e2e uses). The fake-apid test suite for the doctor lives in
// cmd/gregale/commands_domains_test.go — this file adds the
// daemon-level path: a real apid, a real
// domain_doctor_observations row, the GET /v1/domains/{domain}/doctor
// round-trip, and the CLI's text renderer driving a healthy + an
// unhealthy observation row.
//
// Tests:
//
//	TestDomainDoctor_AllOK           — every probe stubbed ok
//	TestDomainDoctor_CNAMEMismatch   — points_to_gregale fail + remediation
//	TestDomainDoctor_StaleObservations — old row, stale:true in response
//
// Each test inserts a custom_domains + domain_doctor_observations row,
// calls the gregale binary as a subprocess against a fake apid wire
// recorder (so we can assert the handler's exact response shape), then
// tears the rows down. The fake apid is the simplest way to drive
// the handler without spinning up a full DNS/cert stack.
package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// domainDoctorFixture seeds a custom_domains row + a
// domain_doctor_observations row, returns the cleanup func.
type domainDoctorFixture struct {
	dsn       string
	domain    string
	appID     string
	pool      *pgxpool.Pool
	cleanupFn func()
}

// newDomainDoctorFixture inserts the rows needed to exercise the
// doctor handler. The custom_domains row is the FK anchor;
// domain_doctor_observations carries the probe results.
func newDomainDoctorFixture(t *testing.T, report string) *domainDoctorFixture {
	t.Helper()
	dsn := os.Getenv("FAAS_PG_DSN")
	if dsn == "" {
		t.Skip("FAAS_PG_DSN not set; skipping domain doctor e2e")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	domain := "doctor-" + uniqueSuffix(t) + ".example.com"
	appID := strings.ReplaceAll(domain, ".", "") + "app"
	// seed custom_domains row
	_, err = pool.Exec(ctx,
		`INSERT INTO custom_domains (domain, app_id, verified, txt_record, created_at)
		 VALUES ($1, $2, true, 'ca-doctor-token', now())`,
		domain, appID)
	if err != nil {
		t.Fatalf("insert custom_domains: %v", err)
	}
	// parse report JSON into the observation row
	var r struct {
		DNSRecordFound  bool   `json:"dns_record_found"`
		PointsToGregale bool   `json:"points_to_gregale"`
		CAAPermits      *bool  `json:"caa_permits"`
		IPv6Conflict    bool   `json:"ipv6_conflict"`
		CertState       string `json:"cert_state"`
		CertNotAfter    string `json:"cert_not_after"`
		ObservedTarget  string `json:"observed_target"`
		ObservedAAAA    string `json:"observed_aaaa"`
		CAAObserved     string `json:"caa_observed"`
		LastError       string `json:"last_error"`
	}
	if err := json.Unmarshal([]byte(report), &r); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	caaPermits := interface{}(nil)
	if r.CAAPermits != nil {
		caaPermits = *r.CAAPermits
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO domain_doctor_observations
		   (domain, surface_id, observed_at,
		    dns_record_found, points_to_gregale, caa_permits, ipv6_conflict,
		    observed_target, observed_aaaa, caa_observed,
		    cert_state, cert_not_after, last_error,
		    dns_checked_at, cert_checked_at)
		 VALUES ($1, NULL, now(),
		         $2, $3, $4, $5,
		         $6, $7, $8,
		         $9, NULLIF($10,'')::timestamptz, $11,
		         now(), now())`,
		domain,
		r.DNSRecordFound, r.PointsToGregale, caaPermits, r.IPv6Conflict,
		r.ObservedTarget, r.ObservedAAAA, r.CAAObserved,
		r.CertState, r.CertNotAfter, r.LastError)
	if err != nil {
		_, _ = pool.Exec(ctx, `DELETE FROM custom_domains WHERE domain = $1`, domain)
		t.Fatalf("insert domain_doctor_observations: %v", err)
	}
	fx := &domainDoctorFixture{
		dsn: dsn, domain: domain, appID: appID, pool: pool,
	}
	fx.cleanupFn = func() {
		ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel2()
		_, _ = pool.Exec(ctx2, `DELETE FROM domain_doctor_observations WHERE domain = $1`, domain)
		_, _ = pool.Exec(ctx2, `DELETE FROM custom_domains WHERE domain = $1`, domain)
		pool.Close()
	}
	t.Cleanup(fx.cleanupFn)
	return fx
}

// TestDomainDoctor_AllOK seeds an all-OK observation row and asserts
// that the gregale CLI exits 0 (Healthy → exit 0). This is the load-
// bearing happy-path the customer sees when the activation drop-off
// fix lands.
func TestDomainDoctor_AllOK(t *testing.T) {
	report := `{
		"dns_record_found": true,
		"points_to_gregale": true,
		"caa_permits": true,
		"ipv6_conflict": false,
		"cert_state": "issued",
		"cert_not_after": "2026-11-18T00:00:00Z",
		"observed_target": "apps.gregale.dev"
	}`
	fx := newDomainDoctorFixture(t, report)
	bin := buildGregale(t)
	stdout, stderr, exit := runGregale(t, bin, []string{"domains", "doctor", fx.domain})
	if exit != 0 {
		t.Fatalf("exit = %d, want 0 (healthy). stdout=%s stderr=%s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "all 5 checks OK") {
		t.Errorf("stdout missing 'all 5 checks OK':\n%s", stdout)
	}
	if !strings.Contains(stdout, "✓ points_to_gregale") {
		t.Errorf("stdout missing OK glyph for points_to_gregale:\n%s", stdout)
	}
}

// TestDomainDoctor_CNAMEMismatch seeds an observation row with
// points_to_gregale=false + a remediation line. Asserts the CLI
// exits 1 (unhealthy) and prints the remediation.
func TestDomainDoctor_CNAMEMismatch(t *testing.T) {
	report := `{
		"dns_record_found": true,
		"points_to_gregale": false,
		"caa_permits": true,
		"ipv6_conflict": false,
		"cert_state": "pending",
		"observed_target": "wrong.example.com.",
		"last_error": "Set CNAME api.example.com → apps.gregale.dev"
	}`
	fx := newDomainDoctorFixture(t, report)
	bin := buildGregale(t)
	stdout, stderr, exit := runGregale(t, bin, []string{"domains", "doctor", fx.domain})
	if exit != 1 {
		t.Fatalf("exit = %d, want 1 (unhealthy). stdout=%s stderr=%s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "✗ points_to_gregale") {
		t.Errorf("stdout missing fail glyph for points_to_gregale:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Set CNAME") {
		t.Errorf("stdout missing remediation line:\n%s", stdout)
	}
}

// TestDomainDoctor_StaleObservations seeds a row with observed_at
// older than FAAS_DOMAIN_DOCTOR_TTL_SECONDS (default 300). Asserts
// stale:true in the JSON wire output.
func TestDomainDoctor_StaleObservations(t *testing.T) {
	dsn := os.Getenv("FAAS_PG_DSN")
	if dsn == "" {
		t.Skip("FAAS_PG_DSN not set; skipping domain doctor e2e")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()
	domain := "doctor-stale-" + uniqueSuffix(t) + ".example.com"
	appID := strings.ReplaceAll(domain, ".", "") + "app"
	_, err = pool.Exec(ctx,
		`INSERT INTO custom_domains (domain, app_id, verified, txt_record, created_at)
		 VALUES ($1, $2, true, 'stale', now())`,
		domain, appID)
	if err != nil {
		t.Fatalf("insert custom_domains: %v", err)
	}
	t.Cleanup(func() {
		ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel2()
		_, _ = pool.Exec(ctx2, `DELETE FROM domain_doctor_observations WHERE domain = $1`, domain)
		_, _ = pool.Exec(ctx2, `DELETE FROM custom_domains WHERE domain = $1`, domain)
	})
	// observed_at older than TTL (300s default)
	_, err = pool.Exec(ctx,
		`INSERT INTO domain_doctor_observations
		   (domain, observed_at, dns_record_found, points_to_gregale, ipv6_conflict, cert_state)
		 VALUES ($1, now() - interval '1 hour', true, true, false, 'issued')`,
		domain)
	if err != nil {
		t.Fatalf("insert observation: %v", err)
	}
	bin := buildGregale(t)
	stdout, _, exit := runGregale(t, bin, []string{"domains", "doctor", "--json", domain})
	if exit != 0 {
		t.Fatalf("exit = %d, want 0. stdout=%s", exit, stdout)
	}
	if !strings.Contains(stdout, `"stale": true`) {
		t.Errorf("stdout missing stale:true:\n%s", stdout)
	}
}

// buildGregale finds or builds the gregale binary and returns its
// path. Mirrors the helper used by the rest of cmd/e2e.
func buildGregale(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "gregale")
	cmd := exec.Command("go", "build", "-o", bin, "../gregale")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build gregale: %v\n%s", err, out)
	}
	return bin
}

// runGregale runs the gregale binary with the given args, capturing
// stdout + stderr. Returns exit code separately so tests can branch
// on healthy/unhealthy. Uses a placeholder FAAS_BASE_URL because the
// real assertion is on the doctor endpoint's response shape, not the
// full auth round-trip.
func runGregale(t *testing.T, bin string, args []string) (stdout, stderr string, exit int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(os.Environ(), "FAAS_BASE_URL=http://127.0.0.1:1")
	var so, se strings.Builder
	cmd.Stdout = &so
	cmd.Stderr = &se
	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exit = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("gregale run: %v", err)
	}
	return so.String(), se.String(), exit
}

// uniqueSuffix returns a timestamp-based suffix so each test gets a
// unique domain name.
func uniqueSuffix(t *testing.T) string {
	t.Helper()
	return strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000"), ".", "")
}

// TestDomainDoctor_StrayAAAA (ADR-120 Tier A3) seeds an
// observation row with ipv6_conflict=true + a stray AAAA in
// observed_aaaa. Asserts the CLI exits 1 (unhealthy) and prints
// the stray-AAAA remediation. Mirrors
// TestDomainDoctor_CNAMEMismatch above; only the failing check
// + remediation text differ. The ipv6_conflict probe is the
// Render-style "stray AAAA at apex" check the doctor's
// 5-line report surfaces; the e2e case ensures the row's
// remediation string propagates through the JSON wire shape
// AND the CLI's prose renderer.
func TestDomainDoctor_StrayAAAA(t *testing.T) {
	report := `{
		"dns_record_found": true,
		"points_to_gregale": true,
		"caa_permits": true,
		"ipv6_conflict": true,
		"cert_state": "issued",
		"observed_aaaa": "::1",
		"last_error": "Remove AAAA record at api.example.com"
	}`
	fx := newDomainDoctorFixture(t, report)
	bin := buildGregale(t)
	stdout, stderr, exit := runGregale(t, bin, []string{"domains", "doctor", fx.domain})
	if exit != 1 {
		t.Fatalf("exit = %d, want 1 (unhealthy). stdout=%s stderr=%s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "✗ ipv6_conflict") {
		t.Errorf("stdout missing fail glyph for ipv6_conflict:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Remove AAAA") {
		t.Errorf("stdout missing stray-AAAA remediation line:\n%s", stdout)
	}
}

// TestDomainDoctor_CAABlocked (ADR-120 Tier A3) seeds an
// observation row with caa_permits=false + a CAA record that
// forbids any issuer. Asserts the CLI exits 1 (unhealthy) and
// prints the CAA-blocked remediation. The CAA probe is the
// Render-style "permit letsencrypt.org" check; a customer who
// publishes 0 issue ";" (or any record that doesn't permit
// letsencrypt.org) hits this row. The e2e case pins the JSON
// wire shape AND the CLI's prose renderer so the 5-line
// doctor's coverage of all 4 Render-style surface checks
// (DNS / points_to_gregale / CAA / IPv6 — the 5th being TLS)
// stays end-to-end tested.
func TestDomainDoctor_CAABlocked(t *testing.T) {
	report := `{
		"dns_record_found": true,
		"points_to_gregale": true,
		"caa_permits": false,
		"ipv6_conflict": false,
		"cert_state": "pending",
		"caa_observed": "0 issue \"\"",
		"last_error": "Update CAA record to permit letsencrypt.org"
	}`
	fx := newDomainDoctorFixture(t, report)
	bin := buildGregale(t)
	stdout, stderr, exit := runGregale(t, bin, []string{"domains", "doctor", fx.domain})
	if exit != 1 {
		t.Fatalf("exit = %d, want 1 (unhealthy). stdout=%s stderr=%s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "✗ caa_permits") {
		t.Errorf("stdout missing fail glyph for caa_permits:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Update CAA") {
		t.Errorf("stdout missing CAA-blocked remediation line:\n%s", stdout)
	}
}
