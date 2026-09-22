// Manual DNS provider for the Tier A8 active-passive HA
// topology (ADR-083). Operator-managed fallback: instead of
// calling a DNS provider API, prints the required `curl` to
// stderr so a staging operator can flip DNS by hand.
//
// Pattern precedent: FAAS_STORAGE_CACHE_SERVE_STALE (ADR-054
// acceptance PR) — opt-in to operator-managed behaviour via a
// single env var, with a printed-curls path for the human in
// the loop. Tier A8 uses the same shape:
//
//	FAAS_DNS_PROVIDER=manual
//
// → every leader transition prints two curls:
//  1. `curl -X POST <ProviderURL>/zones/<ZONE_ID>/dns_records`
//     with the new A record body (UpsertRecord)
//  2. `curl -X DELETE <ProviderURL>/zones/<ZONE_ID>/dns_records/<id>`
//     with the old A record id (DeleteRecord)
//
// A staging operator can copy-paste the curls into their
// terminal; a CI pipeline can pipe stderr to a sidecar that
// calls the API on the operator's behalf.
//
// Review finding #5 (round 1): the manual path previously
// hard-coded `https://dns.hetzner.com/api/v1/...` regardless of
// cfg.ProviderURL. A Route53 or Cloudflare operator would
// copy-paste a Hetzner-shaped curl, get rejected, and the
// orchestrator would still bump `dns_flipped` — a lie. This
// revision (round 2): the curl shape is now Cloudflare-shaped
// (Bearer token, /zones/<id>/dns_records, JSON `{type,name,content,ttl}`)
// because production runs Cloudflare + Caddy. Operators on a
// different provider can set ProviderURL to that provider's
// base URL and translate the curl body accordingly; the
// ProviderURL field gives the curl a useful link to the
// operator-facing console.
//
// Review finding #14: the manual path previously returned nil
// on UpsertRecord/DeleteRecord regardless of whether the
// operator actually flipped DNS, so the orchestrator bumped
// `dns_flipped` even when nothing happened. Both methods now
// return a sentinel error so the orchestrator's drain path
// enters the dns_stale branch and surfaces "operator must run
// the curl" in the audit log + dashboard.
package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/onebox-faas/faas/pkg/safetext"
)

// errManualDNSRequiresOperator is the sentinel the manual
// provider returns from UpsertRecord / DeleteRecord. The
// orchestrator catches it as a non-retryable failure (the
// operator has to act), increments
// activePassiveFailoversTotal{outcome="dns_stale"} (NOT
// "dns_flipped" — DNS was NOT flipped), and surfaces the curl
// the operator needs to run via the runbook's manual drain
// command.
var errManualDNSRequiresOperator = errors.New("manual dns: operator must run the printed curl to flip DNS")

// ManualDNSProvider implements DNSProvider by printing the
// required `curl` to a configured io.Writer (defaults to
// os.Stderr). The struct fields are unexported because callers
// should use NewManualDNSProvider.
type ManualDNSProvider struct {
	zone        string
	providerURL string
	stderr      io.Writer
	mu          sync.Mutex
}

// NewManualDNSProvider builds the manual fallback. The token
// is intentionally NOT consulted — the operator owns the curl
// execution; passing one in is a misconfiguration but harmless
// (the provider never sends it anywhere).
//
// providerURL is the UI/API base the operator faces. Empty
// string → Cloudflare API default
// (https://api.cloudflare.com/client/v4); a Route53 operator
// would set it to https://console.aws.amazon.com/route53.
func NewManualDNSProvider(cfg DNSProviderConfig) (*ManualDNSProvider, error) {
	if cfg.Zone == "" {
		return nil, fmt.Errorf("manual dns: Zone required (e.g. 'example.com')")
	}
	stderr := cfg.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	providerURL := cfg.ProviderURL
	if providerURL == "" {
		providerURL = "https://api.cloudflare.com/client/v4"
	}
	return &ManualDNSProvider{
		zone:        cfg.Zone,
		providerURL: providerURL,
		stderr:      stderr,
	}, nil
}

// UpsertRecord prints the required `curl` to stderr and
// returns errManualDNSRequiresOperator. The `name` is the
// fully-qualified domain; the `value` is the leader's egress
// IP. Returning the sentinel error (review finding #14)
// prevents the orchestrator from bumping `dns_flipped` —
// DNS was NOT flipped; the operator has to run the curl.
func (m *ManualDNSProvider) UpsertRecord(_ context.Context, name, value string) error {
	if name == "" || value == "" {
		return fmt.Errorf("manual dns: name and value required (got name=%q value=%q)", name, value)
	}
	body := string(safetext.JSONObject(struct {
		Type    string `json:"type"`
		Name    string `json:"name"`
		Content string `json:"content"`
		TTL     int    `json:"ttl"`
		Proxied bool   `json:"proxied"`
	}{Type: "A", Name: name, Content: value, TTL: 60, Proxied: false}))
	// Every interpolation below lands inside a shell-quoted word, so each is
	// quoted with safetext.ShellSingleQuote rather than wrapped in literal
	// quotes in the format string. A value carrying a single quote would
	// otherwise terminate the argument and hand the remainder to the shell of
	// whichever operator pastes this in. JSON encoding does not cover it: '
	// needs no escaping in JSON and passes through json.Marshal untouched.
	curl := fmt.Sprintf(
		"# FAAS_DNS_PROVIDER=manual: UpsertRecord\n"+
			"# ProviderURL: %s\n"+
			"# Find zone id:\n"+
			"#   curl -H 'Authorization: Bearer $CF_API_TOKEN' %s\n"+
			"# Then create the A record (replace <ZONE_ID>):\n"+
			"curl -X POST %s \\\n"+
			"  -H 'Authorization: Bearer $CF_API_TOKEN' \\\n"+
			"  -H 'Content-Type: application/json' \\\n"+
			"  -d %s\n",
		m.providerURL,
		safetext.ShellSingleQuote(m.providerURL+"/zones?name="+m.zone),
		safetext.ShellSingleQuote(m.providerURL+"/zones/<ZONE_ID>/dns_records"),
		safetext.ShellSingleQuote(body))
	m.write(curl)
	return errManualDNSRequiresOperator
}

// DeleteRecord prints the required `curl` to stderr and
// returns errManualDNSRequiresOperator. The operator must
// look up the record id first. Returning the sentinel prevents
// the orchestrator from bumping `dns_flipped` (review
// finding #14).
func (m *ManualDNSProvider) DeleteRecord(_ context.Context, name string) error {
	if name == "" {
		return fmt.Errorf("manual dns: name required")
	}
	// Shell-quoted for the same reason as UpsertRecord above.
	curl := fmt.Sprintf(
		"# FAAS_DNS_PROVIDER=manual: DeleteRecord\n"+
			"# ProviderURL: %s\n"+
			"# Find zone id and record id:\n"+
			"#   curl -H 'Authorization: Bearer $CF_API_TOKEN' %s\n"+
			"#   curl -H 'Authorization: Bearer $CF_API_TOKEN' %s\n"+
			"# Then delete (replace <ZONE_ID> and <RECORD_ID>):\n"+
			"curl -X DELETE %s \\\n"+
			"  -H 'Authorization: Bearer $CF_API_TOKEN'\n"+
			"# (deleting record for name=%q, zone=%s)\n",
		m.providerURL,
		safetext.ShellSingleQuote(m.providerURL+"/zones?name="+m.zone),
		safetext.ShellSingleQuote(m.providerURL+"/zones/<ZONE_ID>/dns_records?type=A&name="+name),
		safetext.ShellSingleQuote(m.providerURL+"/zones/<ZONE_ID>/dns_records/<RECORD_ID>"),
		name, m.zone)
	m.write(curl)
	return errManualDNSRequiresOperator
}

// write serialises concurrent stderr writes so two simultaneous
// UpsertRecord calls don't interleave their curls. The lock
// is non-blocking for the operator (stderr writes are cheap).
func (m *ManualDNSProvider) write(s string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Trailing newline is idempotent if the operator pipes
	// stderr through `tee` (each write is one drain cycle).
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	_, _ = io.WriteString(m.stderr, s)
}

// NewDNSProvider is the constructor the dns_handoff
// orchestrator uses. Today it selects between Cloudflare and
// manual based on the FAAS_DNS_PROVIDER env var (read by the
// caller and passed in via cfg); the package holds no env-var
// surface of its own (mirrors FAAS_STORAGE_CACHE_SERVE_STALE
// pattern — ADR-054). Round 1's Hetzner implementation was
// deleted in this PR — production runs Cloudflare + Caddy, so
// the Hetzner plumbing was dead weight (see ADR-083 §3
// follow-up revision).
func NewDNSProvider(cfg DNSProviderConfig, provider string) (DNSProvider, error) {
	switch provider {
	case "cloudflare":
		return NewCloudflareRecordProvider(cfg)
	case "manual":
		return NewManualDNSProvider(cfg)
	case "":
		return nil, errDNSProviderUnknown
	default:
		return nil, fmt.Errorf("%w (got %q)", errDNSProviderUnknown, provider)
	}
}
