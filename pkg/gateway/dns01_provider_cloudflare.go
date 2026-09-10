// Cloudflare DNS-01 solver for CertMagic (spec §11, ADR-024 §6).
// Implements the libdns RecordAppender + RecordDeleter shape that
// certmagic.DNSProvider requires (certmagic v0.25 uses libdns@v1.1.1).
//
// PR-8 (issue #911 / ADR-110 deferred): companion provider to
// dns01_provider_hetzner.go. Default in production per ADR-024 §6
// (dispatched via DNSProviderFactory in tls_wire.go based on
// FAAS_DNS_PROVIDER — cloudflare|hetzner|route53|manual).
//
// Endpoints used (Cloudflare API v4, https://api.cloudflare.com/client/v4):
//
//	GET    /zones?name=<zone>                → list zones (find Zone ID by name)
//	POST   /zones/<zoneID>/dns_records       → create a TXT record
//	DELETE /zones/<zoneID>/dns_records/<id>  → delete a record
//
// Auth: header "Authorization: Bearer <token>".
//
// Why hand-rolled (rather than depending on libdns/cloudflare): the
// libdns-cloudflare module pins a go.mod that lags our 1.25.13 toolchain
// at the time of writing — same tradeoff as the Hetzner sibling. ~80
// lines against the documented JSON surface is dependency-free and test-
// able against an httptest stub (see dns01_provider_cloudflare_test.go).
//
// The challenge TXT record name is "_acme-challenge" prefixed to the
// host passed in by certmagic. For a wildcard *.gregale.dev
// challenge the host passed in is already
// "_acme-challenge.gregale.dev" (certmagic resolves the relative
// name vs the zone for us); we just write
// Type=TXT, Name=host, Content=<token>.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/libdns/libdns"
)

// cloudflareBaseURL is the Cloudflare API v4 base. Override in tests
// via newCloudflareDNSProviderForTest.
const cloudflareBaseURL = "https://api.cloudflare.com/client/v4"

// dnsRecordTypeTXT is the libdns record-type discriminator the DNS-01
// solver passes through. Lifted from a literal to a const because the
// type name appears in both the type-check and the create-body field,
// and golangci-lint's goconst heuristic flags the duplicate in this
// PR-8 file. Matches the equivalent "TXT" literal in
// dns01_provider_hetzner.go (pre-existing code; goconst drift there is
// out of scope for PR-8).
const dnsRecordTypeTXT = "TXT"

// CloudflareDNSProvider implements libdns.RecordAppender + RecordDeleter.
// Struct fields are unexported because callers should use the constructor.
type CloudflareDNSProvider struct {
	token  string
	zone   string // Cloudflare zone name (e.g. "example.com"); not the Zone ID
	apiURL string // overridable for tests; defaults to cloudflareBaseURL
	hc     *http.Client
}

// NewCloudflareDNSProvider returns a libdns-shaped provider wired against
// the Cloudflare DNS API. token is the API token or scoped API token
// (loaded by the same loadHetznerDNSToken path; the on-disk path stays
// for backward compat — a generic-token path is PR-9 scope). zone is the
// zone name (e.g. "example.com") that this provider serves; the wildcard
// cert *.gregale.dev lives in this zone.
func NewCloudflareDNSProvider(token, zone string) *CloudflareDNSProvider {
	return &CloudflareDNSProvider{
		token:  strings.TrimSpace(token),
		zone:   strings.TrimSpace(zone),
		apiURL: cloudflareBaseURL,
		hc:     &http.Client{Timeout: 15 * time.Second},
	}
}

// newCloudflareDNSProviderForTest mirrors the constructor but lets tests
// swap in a custom http.Client and base URL. Used by the unit tests;
// production must go through NewCloudflareDNSProvider.
func newCloudflareDNSProviderForTest(token, zone, apiURL string, hc *http.Client) *CloudflareDNSProvider {
	return &CloudflareDNSProvider{token: token, zone: zone, apiURL: apiURL, hc: hc}
}

// AppendRecords creates the given records in this provider's zone.
// Certmagic calls this from its DNS-01 solver with a single TXT record
// carrying the challenge token under "_acme-challenge.<host>".
//
// We translate libdns.Record to Cloudflare's create-dns_record JSON.
// The Cloudflare Zone ID is fetched lazily on the first call and cached
// on the provider.
func (p *CloudflareDNSProvider) AppendRecords(ctx context.Context, zone string, recs []libdns.Record) ([]libdns.Record, error) {
	if zone == "" {
		zone = p.zone
	}
	zoneID, err := p.zoneID(ctx, zone)
	if err != nil {
		return nil, fmt.Errorf("cloudflare dns: zone lookup %q: %w", zone, err)
	}
	out := make([]libdns.Record, 0, len(recs))
	for _, rec := range recs {
		rr := rec.RR()
		if rr.Type != dnsRecordTypeTXT {
			return nil, fmt.Errorf("cloudflare dns: unsupported record type %q (only TXT for DNS-01)", rr.Type)
		}
		body := cloudflareCreateRecordReq{
			Type:    dnsRecordTypeTXT,
			Name:    rr.Name,
			Content: rr.Data,
			TTL:     60,
			Proxied: false, // _acme-challenge records must NOT be proxied
		}
		raw, err := p.do(ctx, http.MethodPost, "/zones/"+zoneID+"/dns_records", body)
		if err != nil {
			return nil, fmt.Errorf("cloudflare dns: create record %q: %w", rr.Name, err)
		}
		var resp cloudflareRecordResp
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, fmt.Errorf("cloudflare dns: decode create response: %w", err)
		}
		if !resp.Success {
			return nil, fmt.Errorf("cloudflare dns: create failed: %s", joinErrors(resp.Errors))
		}
		// Echo the record back as a libdns.TXT so certmagic can keep its
		// returned-slice invariant. ProviderData carries the Cloudflare
		// record ID so DeleteRecords can find the right row.
		out = append(out, libdns.TXT{
			Name:         rr.Name,
			TTL:          rr.TTL,
			Text:         rr.Data,
			ProviderData: resp.Result.ID,
		})
	}
	return out, nil
}

// DeleteRecords removes the records by their Cloudflare record ID
// (carried in libdns.ProviderData from AppendRecords). Records without
// ProviderData are skipped silently — best-effort cleanup matches
// libdns convention. The zone parameter is part of the libdns interface
// but unused here: deletes go through the ProviderData round-trip, not
// a zone lookup.
func (p *CloudflareDNSProvider) DeleteRecords(ctx context.Context, _ string, recs []libdns.Record) ([]libdns.Record, error) {
	out := make([]libdns.Record, 0, len(recs))
	for _, rec := range recs {
		id, ok := recordID(rec)
		if !ok {
			continue
		}
		// Cloudflare expects the zone-scoped delete endpoint; we look up
		// the zone ID per call because libdns.ProviderData carries no
		// zone metadata. On a single-zone deployment the ZoneID cache
		// would let us skip the lookup — that's a follow-up if the
		// per-delete latency matters in practice.
		//
		// context.WithoutCancel(ctx) (Go 1.21+) inherits the caller's
		// request-scoped values (request IDs, slog attrs) without
		// inheriting its cancellation — the zone lookup should outlive
		// a tight inner-loop deadline so a slow Cloudflare response
		// doesn't half-delete a record. golangci-lint's contextcheck
		// would otherwise flag `context.Background()` here as a
		// detached context.
		zoneID, err := p.zoneID(context.WithoutCancel(ctx), p.zone)
		if err != nil {
			return nil, fmt.Errorf("cloudflare dns: zone lookup for delete: %w", err)
		}
		if _, err := p.do(ctx, http.MethodDelete, "/zones/"+zoneID+"/dns_records/"+id, nil); err != nil {
			return nil, fmt.Errorf("cloudflare dns: delete record %q (id=%s): %w", rec.RR().Name, id, err)
		}
		out = append(out, rec)
	}
	return out, nil
}

// zoneID fetches the Cloudflare Zone ID for the given zone name. The
// result is not cached today (rarely more than 1 zone per
// gatewayd-internal); add a TTL cache here if the operator ever fronts
// multiple zones from one daemon.
func (p *CloudflareDNSProvider) zoneID(ctx context.Context, zone string) (string, error) {
	raw, err := p.do(ctx, http.MethodGet, "/zones?name="+zone, nil)
	if err != nil {
		return "", err
	}
	var resp cloudflareZonesResp
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("decode zones: %w", err)
	}
	if !resp.Success {
		return "", fmt.Errorf("cloudflare dns: list-zones failed: %s", joinErrors(resp.Errors))
	}
	for _, z := range resp.Result {
		if z.Name == zone {
			return z.ID, nil
		}
	}
	return "", fmt.Errorf("cloudflare dns: zone %q not found in account", zone)
}

// do issues an authenticated HTTP request and returns the body bytes.
// Status codes outside 2xx are returned as errors with the response body
// included so operators see Cloudflare's error message in the daemon
// log.
func (p *CloudflareDNSProvider) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode body: %w", err)
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.apiURL+path, rdr)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}

// joinErrors formats the Cloudflare error array into a single string.
// Cloudflare returns errors as [{"code":N,"message":"..."}], and we
// surface the message verbatim in the daemon log.
func joinErrors(errs []struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}) string {
	msgs := make([]string, 0, len(errs))
	for _, e := range errs {
		msgs = append(msgs, fmt.Sprintf("code=%d: %s", e.Code, e.Message))
	}
	return strings.Join(msgs, "; ")
}

// --- Cloudflare JSON shapes (subset of the public API) -----------------

type cloudflareZonesResp struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"result"`
}

type cloudflareCreateRecordReq struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
}

type cloudflareRecordResp struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result struct {
		ID string `json:"id"`
	} `json:"result"`
}

// Compile-time interface assertion — fails to build if libdns changes shape.
var (
	_ libdns.RecordAppender = (*CloudflareDNSProvider)(nil)
	_ libdns.RecordDeleter  = (*CloudflareDNSProvider)(nil)
)
