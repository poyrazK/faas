package api

import (
	"encoding/json"
	"testing"
)

// TestDomainDoctorEnabledDefaultsOn (ADR-120 Tier A3) asserts
// the flag defaults to on (post-soak dark-launch cutover). A
// test running without FAAS_DOMAIN_DOCTOR_ENABLED set must see
// the gate ON so the dns_poller's runDoctorOnce branch is
// reached and the apid_domain_doctor_oldest_observation_seconds
// gauge surfaces in /metrics. The reverse — the dns_poller
// gate off-by-default — would silently disable the operator's
// visibility into the doctor. Mirrors
// TestCertEngineStagingDefaultsOn's "safe default" semantics.
func TestDomainDoctorEnabledDefaultsOn(t *testing.T) {
	t.Setenv("FAAS_DOMAIN_DOCTOR_ENABLED", "")
	if !DomainDoctorEnabled() {
		t.Fatal("DomainDoctorEnabled default = false; want true (default-on after Tier A3 soak)")
	}
}

// TestDomainDoctorEnabledAcceptsOnTokens covers the 1/true/yes/on
// accept set documented in flags.go. Mirrors
// TestTenantSurfacesEnabledAcceptsOnTokens but inverted — the
// on-tokens are still recognised (defence in depth for ops that
// copy-paste the env from a TenantSurfaces-style config).
func TestDomainDoctorEnabledAcceptsOnTokens(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE", "True", "yes", "YES", "on", "ON"} {
		t.Setenv("FAAS_DOMAIN_DOCTOR_ENABLED", v)
		if !DomainDoctorEnabled() {
			t.Errorf("DomainDoctorEnabled(%q) = false; want true", v)
		}
	}
}

// TestDomainDoctorEnabledAcceptsExplicitOffTokens covers the
// 0/false/no/off reject set documented in flags.go — the
// operator's escape hatch for "I want to disable the doctor
// without code revert" (Tier A3 cutover keeps the override).
// Mirrors TestCertEngineStagingDefaultsOn's prod-opt-in shape
// (FAAS_TLS_STAGING=0 flips to prod).
func TestDomainDoctorEnabledAcceptsExplicitOffTokens(t *testing.T) {
	for _, v := range []string{"0", "false", "FALSE", "False", "no", "NO", "off", "OFF"} {
		t.Setenv("FAAS_DOMAIN_DOCTOR_ENABLED", v)
		if DomainDoctorEnabled() {
			t.Errorf("DomainDoctorEnabled(%q) = true; want false (explicit off)", v)
		}
	}
}

// TestDomainDoctorEnabledIgnoresUnknownTokens pins the
// default-on safety posture: any token outside the
// explicit-off set returns true so a typo (e.g. "disable"
// instead of "disabled", or a stray newline) doesn't
// accidentally turn the doctor off. The dns_poller
// emitDoctorSkip helper still emits its log line on every
// tick when the doctor is off, so a misconfigured-off is
// observable in logs + Alertmanager.
func TestDomainDoctorEnabledIgnoresUnknownTokens(t *testing.T) {
	for _, v := range []string{"enabled", "disable", "offish", "1\n", " yes"} {
		t.Setenv("FAAS_DOMAIN_DOCTOR_ENABLED", v)
		if !DomainDoctorEnabled() {
			t.Errorf("DomainDoctorEnabled(%q) = false; want true (default-on safety)", v)
		}
	}
}

// TestTenantSurfacesEnabledDefaultsOff asserts the flag is opt-in.
// A test running without FAAS_TENANT_SURFACES_ENABLED set must
// see the gate off so the surface routes (PR-C) stay 404/503 in
// CI / staging until the operator deliberately flips the switch.
func TestTenantSurfacesEnabledDefaultsOff(t *testing.T) {
	t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "")
	if TenantSurfacesEnabled() {
		t.Fatal("TenantSurfacesEnabled default = true; want false")
	}
}

// TestTenantSurfacesEnabledAcceptsOnTokens covers the 1/true/yes/on
// accept set documented in flags.go. Anything outside the set
// must keep the gate off.
func TestTenantSurfacesEnabledAcceptsOnTokens(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE", "True", "yes", "YES", "on", "ON"} {
		t.Setenv("FAAS_TENANT_SURFACES_ENABLED", v)
		if !TenantSurfacesEnabled() {
			t.Errorf("TenantSurfacesEnabled(%q) = false; want true", v)
		}
	}
}

// TestTenantSurfacesEnabledRejectsOtherTokens pins the closed
// accept set so a typo (e.g. "enabled", "on " with trailing
// space — wait, we DO trim — so "truthy" or "1\n") doesn't
// silently enable the surface routes.
func TestTenantSurfacesEnabledRejectsOtherTokens(t *testing.T) {
	for _, v := range []string{"enabled", "truthy", "0", "no", "off", "false"} {
		t.Setenv("FAAS_TENANT_SURFACES_ENABLED", v)
		if TenantSurfacesEnabled() {
			t.Errorf("TenantSurfacesEnabled(%q) = true; want false", v)
		}
	}
}

// TestTenantSurfacesDTORoundTrip pins the wire shape: a serialized
// response must round-trip identically so the SDK regen and the
// dashboard can rely on stable field names. Locks the empty
// Hostnames array (we always emit the field; a future PR-C
// handler fills it).
func TestTenantSurfacesDTORoundTrip(t *testing.T) {
	s := TenantSurfaceResponse{
		ID:        "srf-1",
		AccountID: "acc-1",
		AppID:     "app-1",
		Name:      "na-customers",
		CertKind:  "per_host_san",
		Status:    "active",
		CertState: "issued",
		Hostnames: []TenantHostnameResponse{
			{Hostname: "api.customer-a.com", Verified: true, TXTRecord: "_faas-verify.api.customer-a.com"},
		},
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var back TenantSurfaceResponse
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.ID != s.ID || back.Name != s.Name || back.CertKind != s.CertKind {
		t.Fatalf("round-trip mismatch: %+v vs %+v", back, s)
	}
	if len(back.Hostnames) != 1 || back.Hostnames[0].Hostname != "api.customer-a.com" {
		t.Fatalf("hostnames round-trip: %+v", back.Hostnames)
	}
}

// TestCreateTenantSurfaceRequestDefaultsCertKind pins that the
// apid handler can rely on an empty CertKind meaning "default
// per_host_san". We don't fill it in the DTO; the store does
// (state.CreateTenantSurfaceIfUnderQuota). The test asserts the
// wire shape doesn't carry a default — a malformed request that
// sets cert_kind="" must be equivalent to omitting the field.
func TestCreateTenantSurfaceRequestDefaultsCertKind(t *testing.T) {
	var req CreateTenantSurfaceRequest
	raw := []byte(`{"app_id":"app-1","name":"x","hostnames":["a.example"]}`)
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	if req.CertKind != "" {
		t.Fatalf("default cert_kind = %q; want empty (store fills)", req.CertKind)
	}
	if len(req.Hostnames) != 1 || req.Hostnames[0] != "a.example" {
		t.Fatalf("hostnames = %+v", req.Hostnames)
	}
}

// TestCertEngineWiredDefaultsOff pins the dark-launch posture:
// the cert engine is unwired until the operator sets BOTH
// FAAS_TLS_STORAGE_DIR and FAAS_TLS_CONTACT_EMAIL. A misconfigured
// rollout that flips FAAS_TENANT_SURFACES_ENABLED on but leaves
// the cert-engine env blank must NOT crash the daemon; the
// wrapper's nil-issuer degradation surfaces a clear
// "cert engine unwired" last_error.
func TestCertEngineWiredDefaultsOff(t *testing.T) {
	t.Setenv("FAAS_TLS_STORAGE_DIR", "")
	t.Setenv("FAAS_TLS_CONTACT_EMAIL", "")
	if CertEngineWired() {
		t.Fatal("CertEngineWired = true with both env unset; want false")
	}
}

// TestCertEngineWiredRequiresBoth pins the AND-of-two contract:
// setting just one of the two env vars is NOT enough. The
// fail-closed contract from PR-D commit 1 spec demands both.
func TestCertEngineWiredRequiresBoth(t *testing.T) {
	t.Setenv("FAAS_TLS_STORAGE_DIR", "/var/lib/faas/certs")
	t.Setenv("FAAS_TLS_CONTACT_EMAIL", "")
	if CertEngineWired() {
		t.Fatal("CertEngineWired = true with only STORAGE_DIR set; want false")
	}
	t.Setenv("FAAS_TLS_STORAGE_DIR", "")
	t.Setenv("FAAS_TLS_CONTACT_EMAIL", "ops@example.com")
	if CertEngineWired() {
		t.Fatal("CertEngineWired = true with only CONTACT_EMAIL set; want false")
	}
	t.Setenv("FAAS_TLS_STORAGE_DIR", "/var/lib/faas/certs")
	t.Setenv("FAAS_TLS_CONTACT_EMAIL", "ops@example.com")
	if !CertEngineWired() {
		t.Fatal("CertEngineWired = false with both set; want true")
	}
}

// TestCertEngineStagingDefaultsOn pins the safe-default: a
// fresh dev box defaults to LE staging so a misconfigured DNS
// delegation can't burn the production rate limit. Production
// operators opt-in to the prod CA via FAAS_TLS_STAGING=0.
func TestCertEngineStagingDefaultsOn(t *testing.T) {
	t.Setenv("FAAS_TLS_STAGING", "")
	if !CertEngineStaging() {
		t.Fatal("CertEngineStaging default = false; want true (staging is the safe default)")
	}
	for _, v := range []string{"0", "false", "FALSE", "no", "off"} {
		t.Setenv("FAAS_TLS_STAGING", v)
		if CertEngineStaging() {
			t.Errorf("CertEngineStaging(%q) = true; want false", v)
		}
	}
}

// TestCertEngineDNSProviderDefaultsCloudflare pins the default
// per ADR-024 §6. References the DNSProvider* constants (also
// referenced by CertEngineDNSProvider in flags.go) so the
// golangci-lint goconst check doesn't flag the literal-3+ shape.
func TestCertEngineDNSProviderDefaultsCloudflare(t *testing.T) {
	t.Setenv("FAAS_TLS_DNS_PROVIDER", "")
	if got := CertEngineDNSProvider(); got != DNSProviderCloudflare {
		t.Errorf("CertEngineDNSProvider default = %q; want %q", got, DNSProviderCloudflare)
	}
	t.Setenv("FAAS_TLS_DNS_PROVIDER", DNSProviderHetzner)
	if got := CertEngineDNSProvider(); got != DNSProviderHetzner {
		t.Errorf("CertEngineDNSProvider(hetzner) = %q; want %q", got, DNSProviderHetzner)
	}
	// Unknown provider falls back to cloudflare (the documented
	// default) rather than erroring — the cert engine will then
	// fail to construct a DNS provider and the wrapper's
	// nil-issuer degradation handles the visible failure.
	t.Setenv("FAAS_TLS_DNS_PROVIDER", "route53-unimpl")
	if got := CertEngineDNSProvider(); got != DNSProviderCloudflare {
		t.Errorf("CertEngineDNSProvider(unknown) = %q; want %q (safe default)", got, DNSProviderCloudflare)
	}
}

// TestApiContractDiffEnabledDefaultsOff asserts the flag is
// opt-in. The PR-A migration (00314) + capture path (PR-B) +
// gate wiring (PR-C) all ship behind this flag; a default-on
// shape would silently block production PATCHes on staging
// deploys that have no captured snapshot.
func TestApiContractDiffEnabledDefaultsOff(t *testing.T) {
	t.Setenv("FAAS_API_CONTRACT_DIFF_ENABLED", "")
	if ApiContractDiffEnabled() {
		t.Fatal("ApiContractDiffEnabled default = true; want false")
	}
}

// TestApiContractDiffEnabledAcceptsOnTokens covers the
// 1/true/yes/on accept set documented in flags.go. Matches
// the TenantSurfacesEnabled pattern (no trailing-space token —
// the reader trims).
func TestApiContractDiffEnabledAcceptsOnTokens(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE", "True", "yes", "YES", "on", "ON"} {
		t.Setenv("FAAS_API_CONTRACT_DIFF_ENABLED", v)
		if !ApiContractDiffEnabled() {
			t.Errorf("ApiContractDiffEnabled(%q) = false; want true", v)
		}
	}
}

// TestApiContractDiffEnabledRejectsOtherTokens pins the closed
// accept set so a typo (e.g. "enabled", "1\n") doesn't
// silently enable the gate.
func TestApiContractDiffEnabledRejectsOtherTokens(t *testing.T) {
	for _, v := range []string{"enabled", "truthy", "0", "no", "off", "false"} {
		t.Setenv("FAAS_API_CONTRACT_DIFF_ENABLED", v)
		if ApiContractDiffEnabled() {
			t.Errorf("ApiContractDiffEnabled(%q) = true; want false", v)
		}
	}
}
