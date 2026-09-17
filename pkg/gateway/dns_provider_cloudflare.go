// Cloudflare DNS A/AAAA record provider for the Tier A8
// active-passive HA topology (ADR-083). Replaces the
// HetznerRecordProvider (deleted in this PR — see ADR-083
// §3 follow-up revision). Caddy + Cloudflare already terminates
// TLS for the same hostname upstream of gatewayd-public, so the
// leader-election A-record naturally lands on the same zone
// Cloudflare serves — no separate DNS-01 plumbing required.
//
// Endpoints used:
//
//	GET    /zones?name=<zone>                  → list zones (find Zone ID by name)
//	GET    /zones/<zoneID>/dns_records?type=A&name=<name>  → list A records (filtered server-side)
//	POST   /zones/<zoneID>/dns_records        → create a record (A, faas-<node>.<zone>)
//	PUT    /zones/<zoneID>/dns_records/<id>   → update an existing record (UpsertRecord idempotency)
//	DELETE /zones/<zoneID>/dns_records/<id>   → delete a record (DeleteRecord)
//
// Auth: header "Authorization: Bearer <token>" (the unsealed
// value from SealedToken via pkg/secretbox.OpenBytes with
// namespace "DNS_PROVIDER"). The token shape matches Cloudflare's
// API Token model — scoped to Zone:DNS:Edit on the zone in
// question.
//
// Caching: the Zone → ZoneID resolution runs once per process
// (resolveZoneID's atomic.Value + time.Time TTL); transient
// errors clear the cache so the next call retries. Code-review
// fix #7 — without the cache, every UpsertRecord/DeleteRecord
// fires GET /zones, against Cloudflare's 1200 req/5min/user
// quota, easily exhausted during a flap.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/httpjson"
)

// cloudflareDNSBaseURL is the Cloudflare API v4 base. Override
// in tests via CloudflareRecordProvider.APIURL.
const cloudflareDNSBaseURL = "https://api.cloudflare.com/client/v4"

// cloudflareZoneIDCacheTTL is the lifetime of a cached zone ID
// before refresh-on-next-call. 5 minutes balances API quota
// against a freshly-provisioned zone (operator creates the zone,
// then runs faas — within seconds). Set low enough that a
// operator-managed zone rotation doesn't take 24h to surface.
const cloudflareZoneIDCacheTTL = 5 * time.Minute

// cloudflareResponseMaxBytes caps successful JSON responses. Zone and record
// lookups are intentionally small; a larger body is an upstream failure, not
// useful provider data.
const cloudflareResponseMaxBytes = 1 << 20

// CloudflareRecordProvider implements DNSProvider against the
// Cloudflare API for the leader-election DNS handoff (Tier A8 /
// ADR-083). Struct fields are unexported because callers should
// use NewCloudflareRecordProvider.
type CloudflareRecordProvider struct {
	token  string
	zone   string
	apiURL string
	hc     *http.Client

	// zoneIDCached is the cached ZoneID; empty if not yet
	// resolved or last resolve failed. Atomic so the resolve
	// path can refresh without taking a mutex on the hot
	// UpsertRecord/DeleteRecord path.
	zoneIDCached atomic.Value // string

	// zoneIDCachedAt is the wall-clock time of the last
	// successful resolve. Read together with zoneIDCached to
	// decide whether to refresh.
	zoneIDCachedAt atomic.Int64 // unix nanos
}

// NewCloudflareRecordProvider builds the Cloudflare-backed
// DNSProvider. token is the unsealed Cloudflare API token
// (callers unseal from SealedToken via pkg/secretbox.OpenBytes
// with namespace "DNS_PROVIDER" before passing in). zone is the
// DNS zone name (e.g. "example.com"); the provider resolves
// Zone → ZoneID lazily on the first request and caches it for
// cloudflareZoneIDCacheTTL.
func NewCloudflareRecordProvider(cfg DNSProviderConfig) (*CloudflareRecordProvider, error) {
	if cfg.Zone == "" {
		return nil, fmt.Errorf("cloudflare dns: Zone required")
	}
	if len(cfg.SealedToken) == 0 {
		return nil, fmt.Errorf("cloudflare dns: SealedToken required")
	}
	// Unseal: the DNS_PROVIDER namespace is a SealedBytes blob
	// (ADR-076 precedent; matches the webhook secretbox shape).
	plaintext, err := openDNSProviderToken(cfg.SealedToken)
	if err != nil {
		return nil, fmt.Errorf("cloudflare dns: unseal token: %w", err)
	}
	apiURL := cfg.APIURL
	if apiURL == "" {
		apiURL = cloudflareDNSBaseURL
	}
	return &CloudflareRecordProvider{
		token:  string(plaintext),
		zone:   cfg.Zone,
		apiURL: apiURL,
		hc: &http.Client{
			Timeout: 10 * time.Second, // bounded — drain protocol is HADNSRecordStaleSeconds (30s)
		},
	}, nil
}

// UpsertRecord implements DNSProvider. Idempotent: a record
// that already exists with the same value is a no-op (the
// provider returns nil without writing); a record with a
// different value is updated via PUT /zones/<id>/dns_records/<id>.
//
// name is the fully-qualified domain (e.g.
// "faas-node-a.example.com"); value is the leader's egress IP.
func (p *CloudflareRecordProvider) UpsertRecord(ctx context.Context, name, value string) error {
	if name == "" || value == "" {
		return fmt.Errorf("cloudflare dns: name and value required (got name=%q value=%q)", name, value)
	}
	zoneID, err := p.resolveZoneID(ctx)
	if err != nil {
		return err
	}
	existing, err := p.findRecord(ctx, zoneID, name)
	if err != nil {
		return err
	}
	if existing != nil && existing.Content == value {
		return nil // idempotent no-op
	}
	if existing != nil {
		return p.updateRecord(ctx, existing.ID, zoneID, name, value)
	}
	return p.createRecord(ctx, zoneID, name, value)
}

// DeleteRecord implements DNSProvider. Idempotent: a record
// that doesn't exist is a no-op (returns nil without an API
// call).
func (p *CloudflareRecordProvider) DeleteRecord(ctx context.Context, name string) error {
	if name == "" {
		return fmt.Errorf("cloudflare dns: name required")
	}
	zoneID, err := p.resolveZoneID(ctx)
	if err != nil {
		return err
	}
	existing, err := p.findRecord(ctx, zoneID, name)
	if err != nil {
		return err
	}
	if existing == nil {
		return nil // idempotent no-op
	}
	return p.deleteRecord(ctx, zoneID, existing.ID)
}

// resolveZoneID resolves Zone → ZoneID via
// GET /zones?name=<zone>. Cached on the receiver (code-review
// fix #7) so a re-election cycle doesn't re-query the Cloudflare
// API — the previous implementation fired GET /zones on every
// UpsertRecord/DeleteRecord, easily exhausting the 1200 req/5min
// quota during a flap. The cache is process-local with a TTL
// (cloudflareZoneIDCacheTTL); transient errors clear the cache
// so the next call retries.
func (p *CloudflareRecordProvider) resolveZoneID(ctx context.Context) (string, error) {
	if p.zone == "" {
		return "", fmt.Errorf("cloudflare dns: empty zone")
	}
	if cached := p.cachedZoneID(); cached != "" {
		return cached, nil
	}
	id, err := p.queryZoneID(ctx)
	if err != nil {
		// Clear the cache on error so the next call retries
		// (a transient 5xx shouldn't permanently poison).
		p.zoneIDCached.Store("")
		p.zoneIDCachedAt.Store(0)
		return "", err
	}
	p.zoneIDCached.Store(id)
	p.zoneIDCachedAt.Store(time.Now().UnixNano())
	return id, nil
}

// cachedZoneID returns the cached ZoneID if it's still fresh,
// or "" if it's stale / missing / never resolved.
func (p *CloudflareRecordProvider) cachedZoneID() string {
	v := p.zoneIDCached.Load()
	id, _ := v.(string)
	if id == "" {
		return ""
	}
	cachedAt := time.Unix(0, p.zoneIDCachedAt.Load())
	if time.Since(cachedAt) > cloudflareZoneIDCacheTTL {
		return ""
	}
	return id
}

// queryZoneID is the one-shot /zones?name=<zone> call that
// backs resolveZoneID's cache.
func (p *CloudflareRecordProvider) queryZoneID(ctx context.Context) (string, error) {
	url := fmt.Sprintf("%s/zones?name=%s", p.apiURL, p.zone)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("cloudflare dns: build zone request: %w", err)
	}
	p.setAuth(req)
	resp, err := p.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("cloudflare dns: zone request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("cloudflare dns: zone %s: status %d: %s", p.zone, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out cfZonesResponse
	if err := httpjson.Decode(resp.Body, cloudflareResponseMaxBytes, &out); err != nil {
		return "", fmt.Errorf("cloudflare dns: decode zone response: %w", err)
	}
	for _, z := range out.Result {
		if z.Name == p.zone {
			return z.ID, nil
		}
	}
	return "", fmt.Errorf("cloudflare dns: zone %q not found", p.zone)
}

// setAuth stamps the Bearer-token Authorization header.
func (p *CloudflareRecordProvider) setAuth(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")
}

// cfZonesResponse mirrors the Cloudflare /zones JSON shape.
type cfZonesResponse struct {
	Result []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"result"`
}

// cfDNSRecord mirrors the Cloudflare /zones/<id>/dns_records
// JSON shape. `Content` is the A-record value (the field is
// called `content`, not `value`, on Cloudflare's API — that's
// the wire-shape tripwire this file's package docstring warns
// about).
type cfDNSRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
}

// findRecord queries GET /zones/<zoneID>/dns_records?type=A&name=<name>.
// The server-side name filter is essential (Cloudflare paginates
// at 100/page; scanning client-side misses records on page 2+,
// exactly the bug that bit Hetzner — same shape, same fix).
func (p *CloudflareRecordProvider) findRecord(ctx context.Context, zoneID, name string) (*cfDNSRecord, error) {
	url := fmt.Sprintf("%s/zones/%s/dns_records?type=A&name=%s", p.apiURL, zoneID, name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("cloudflare dns: build records request: %w", err)
	}
	p.setAuth(req)
	resp, err := p.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cloudflare dns: records request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("cloudflare dns: records list: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		Result []cfDNSRecord `json:"result"`
	}
	if err := httpjson.Decode(resp.Body, cloudflareResponseMaxBytes, &out); err != nil {
		return nil, fmt.Errorf("cloudflare dns: decode records response: %w", err)
	}
	for i := range out.Result {
		// The server-side filter is a substring match on
		// Cloudflare's side; double-check client-side so a
		// same-suffix record can't poison the lookup.
		if out.Result[i].Name == name && out.Result[i].Type == "A" {
			return &out.Result[i], nil
		}
	}
	return nil, nil
}

func (p *CloudflareRecordProvider) createRecord(ctx context.Context, zoneID, name, value string) error {
	body, _ := json.Marshal(map[string]any{
		"type":    "A",
		"name":    name,
		"content": value,
		"ttl":     60,
		"proxied": false,
	})
	url := fmt.Sprintf("%s/zones/%s/dns_records", p.apiURL, zoneID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("cloudflare dns: build create request: %w", err)
	}
	p.setAuth(req)
	resp, err := p.hc.Do(req)
	if err != nil {
		return fmt.Errorf("cloudflare dns: create request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("cloudflare dns: create record %s: status %d: %s", name, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func (p *CloudflareRecordProvider) updateRecord(ctx context.Context, recordID, zoneID, name, value string) error {
	body, _ := json.Marshal(map[string]any{
		"type":    "A",
		"name":    name,
		"content": value,
		"ttl":     60,
		"proxied": false,
	})
	url := fmt.Sprintf("%s/zones/%s/dns_records/%s", p.apiURL, zoneID, recordID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("cloudflare dns: build update request: %w", err)
	}
	p.setAuth(req)
	resp, err := p.hc.Do(req)
	if err != nil {
		return fmt.Errorf("cloudflare dns: update request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("cloudflare dns: update record %s: status %d: %s", recordID, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func (p *CloudflareRecordProvider) deleteRecord(ctx context.Context, zoneID, recordID string) error {
	url := fmt.Sprintf("%s/zones/%s/dns_records/%s", p.apiURL, zoneID, recordID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("cloudflare dns: build delete request: %w", err)
	}
	p.setAuth(req)
	resp, err := p.hc.Do(req)
	if err != nil {
		return fmt.Errorf("cloudflare dns: delete request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Cloudflare returns 200 on delete with body `{"success": true}`,
	// or 404 if the record was already gone (treat as success —
	// the orchestrator wants DeleteRecord idempotent).
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("cloudflare dns: delete record %s: status %d: %s", recordID, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
