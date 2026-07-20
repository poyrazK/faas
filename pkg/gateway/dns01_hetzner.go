// Hetzner DNS-01 solver for CertMagic (spec §11). Implements the libdns
// RecordAppender + RecordDeleter shape that certmagic.DNSProvider requires
// (certmagic v0.25 uses libdns@v1.1.1).
//
// Endpoints used (Hetzner DNS API, https://dns.hetzner.com/api/v1):
//
//	GET    /api/v1/zones?name=<zone>           → list zones (find Zone ID by name)
//	POST   /api/v1/records                    → create a record (TXT, _acme-challenge.<host>)
//	DELETE /api/v1/records/<id>               → delete a record
//
// Auth: header "Auth-API-Token: <token>".
//
// Why hand-rolled (rather than depending on libdns-hetzner): libdns-hetzner
// exists but pulls go modules whose go.mod pins an older Go than our 1.25.7
// toolchain — a Hetzner-specific implementation against the documented JSON
// surface is small, dependency-free, and easy to test against an httptest
// stub.
//
// The challenge TXT record name is "_acme-challenge" prefixed to the host
// passed in by certmagic. For a wildcard *.apps.example.com challenge the
// host passed in is already "_acme-challenge.apps.example.com" (certmagic
// resolves the relative name vs the zone for us); we just write
// Name=host, Type=TXT, Value=<token>.
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

// hetznerBaseURL is the Hetzner DNS API base. Override in tests via
// newHetznerDNSProviderWithHTTP.
const hetznerBaseURL = "https://dns.hetzner.com/api/v1"

// HetznerDNSProvider implements libdns.RecordAppender + RecordDeleter. The
// struct fields are unexported because callers should use the constructor.
type HetznerDNSProvider struct {
	token  string
	zone   string // Hetzner zone name (e.g. "example.com"); not the Zone ID
	apiURL string // overridable for tests; defaults to hetznerBaseURL
	hc     *http.Client
}

// NewHetznerDNSProvider returns a libdns-shaped provider wired against the
// Hetzner DNS API. token is the Auth-API-Token value (loaded by
// loadHetznerDNSToken from /etc/faas/secrets/hetzner-dns.token with a 0400
// perm check). zone is the zone name (e.g. "example.com") that this provider
// serves; the wildcard cert *.apps.example.com lives in this zone.
func NewHetznerDNSProvider(token, zone string) *HetznerDNSProvider {
	return &HetznerDNSProvider{
		token:  strings.TrimSpace(token),
		zone:   strings.TrimSpace(zone),
		apiURL: hetznerBaseURL,
		hc:     &http.Client{Timeout: 15 * time.Second},
	}
}

// newHetznerDNSProviderForTest mirrors the constructor but lets tests swap in
// a custom http.Client and base URL. Used by the unit tests; production must
// go through NewHetznerDNSProvider.
func newHetznerDNSProviderForTest(token, zone, apiURL string, hc *http.Client) *HetznerDNSProvider {
	return &HetznerDNSProvider{token: token, zone: zone, apiURL: apiURL, hc: hc}
}

// AppendRecords creates the given records in this provider's zone. Certmagic
// calls this from its DNS-01 solver with a single TXT record carrying the
// challenge token under "_acme-challenge.<host>".
//
// We translate libdns.Record to Hetzner's create-record JSON. The Hetzner
// zone ID is fetched lazily on the first call and cached on the provider.
func (p *HetznerDNSProvider) AppendRecords(ctx context.Context, zone string, recs []libdns.Record) ([]libdns.Record, error) {
	if zone == "" {
		zone = p.zone
	}
	zoneID, err := p.zoneID(ctx, zone)
	if err != nil {
		return nil, fmt.Errorf("hetzner dns: zone lookup %q: %w", zone, err)
	}
	out := make([]libdns.Record, 0, len(recs))
	for _, rec := range recs {
		rr := rec.RR()
		if rr.Type != "TXT" {
			return nil, fmt.Errorf("hetzner dns: unsupported record type %q (only TXT for DNS-01)", rr.Type)
		}
		body := hetznerCreateRecordReq{
			Value:    rr.Data,
			TTL:      60,
			Type:     "TXT",
			Name:     rr.Name,
			ZoneID:   zoneID,
			ZoneName: zone,
		}
		raw, err := p.do(ctx, http.MethodPost, "/records", body)
		if err != nil {
			return nil, fmt.Errorf("hetzner dns: create record %q: %w", rr.Name, err)
		}
		var resp hetznerRecordResp
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, fmt.Errorf("hetzner dns: decode create response: %w", err)
		}
		// Echo the record back as a libdns.TXT so certmagic can keep its
		// returned-slice invariant. ProviderData carries the Hetzner record ID
		// so DeleteRecords can find the right row.
		out = append(out, libdns.TXT{
			Name:         rr.Name,
			TTL:          rr.TTL,
			Text:         rr.Data,
			ProviderData: resp.Record.ID,
		})
	}
	return out, nil
}

// DeleteRecords removes the records by their Hetzner record ID (carried in
// libdns.ProviderData from AppendRecords). Records without ProviderData are
// skipped silently — best-effort cleanup matches libdns convention.
func (p *HetznerDNSProvider) DeleteRecords(ctx context.Context, zone string, recs []libdns.Record) ([]libdns.Record, error) {
	if zone == "" {
		zone = p.zone
	}
	out := make([]libdns.Record, 0, len(recs))
	for _, rec := range recs {
		id, ok := recordID(rec)
		if !ok {
			continue
		}
		if _, err := p.do(ctx, http.MethodDelete, "/records/"+id, nil); err != nil {
			return nil, fmt.Errorf("hetzner dns: delete record %q (id=%s): %w", rec.RR().Name, id, err)
		}
		out = append(out, rec)
	}
	return out, nil
}

// zoneID fetches the Hetzner Zone ID for the given zone name. The result is
// not cached today (rarely more than 1 zone per gatewayd); add a TTL cache
// here if the operator ever fronts multiple zones from one daemon.
func (p *HetznerDNSProvider) zoneID(ctx context.Context, zone string) (string, error) {
	raw, err := p.do(ctx, http.MethodGet, "/zones?name="+zone, nil)
	if err != nil {
		return "", err
	}
	var resp hetznerZonesResp
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("decode zones: %w", err)
	}
	for _, z := range resp.Zones {
		if z.Name == zone {
			return z.ID, nil
		}
	}
	return "", fmt.Errorf("hetzner dns: zone %q not found in account", zone)
}

// recordID extracts the Hetzner record ID stashed in the concrete record's
// ProviderData field by AppendRecords. ProviderData lives on the concrete
// record type (e.g. libdns.TXT); the libdns.Record interface doesn't expose
// it, so a type-switch is the only way to recover it.
func recordID(rec libdns.Record) (string, bool) {
	switch r := rec.(type) {
	case libdns.TXT:
		id, _ := r.ProviderData.(string)
		return id, id != ""
	case libdns.Address:
		id, _ := r.ProviderData.(string)
		return id, id != ""
	case libdns.CNAME:
		id, _ := r.ProviderData.(string)
		return id, id != ""
	default:
		return "", false
	}
}

// do issues an authenticated HTTP request and returns the body bytes. Status
// codes outside 2xx are returned as errors with the response body included so
// operators see Hetzner's error message in the daemon log.
func (p *HetznerDNSProvider) do(ctx context.Context, method, path string, body any) ([]byte, error) {
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
	req.Header.Set("Auth-API-Token", p.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}

// --- Hetzner JSON shapes (subset of the public API) -------------------

type hetznerZonesResp struct {
	Zones []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"zones"`
}

type hetznerCreateRecordReq struct {
	Value    string `json:"value"`
	TTL      int    `json:"ttl"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	ZoneID   string `json:"zone_id"`
	ZoneName string `json:"zone_name"`
}

type hetznerRecordResp struct {
	Record struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"record"`
}

// Compile-time interface assertion — fails to build if libdns changes shape.
var (
	_ libdns.RecordAppender = (*HetznerDNSProvider)(nil)
	_ libdns.RecordDeleter  = (*HetznerDNSProvider)(nil)
)
