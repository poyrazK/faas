package edgedns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// RecordComment marks the records edgednsd creates, so an operator reading
// the zone knows which ones it owns.
const RecordComment = "edgednsd (ADR-20261102081618)"

// CloudflareAPI is Cloudflare's v4 API.
const CloudflareAPI = "https://api.cloudflare.com/client/v4"

// DNSOnlyTTL is a DNS-only record's TTL in seconds, the shortest Cloudflare
// allows below Enterprise (ADR-20261103163954 §1). Proxied records take TTL
// 1, Cloudflare's automatic TTL.
const DNSOnlyTTL = 60

// Cloudflare is DNS over Cloudflare's API, with a token that holds
// Zone > DNS: Edit on the managed zones and nothing else.
type Cloudflare struct {
	baseURL string
	token   string
	client  *http.Client
}

// NewCloudflare returns a client for baseURL (CloudflareAPI in production).
func NewCloudflare(baseURL, token string) (*Cloudflare, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("edgedns: empty Cloudflare token")
	}
	return &Cloudflare{baseURL: strings.TrimRight(baseURL, "/"), token: token, client: &http.Client{Timeout: 10 * time.Second}}, nil
}

type cfRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
}

// Records returns name's A and AAAA records, and whether a CNAME holds it.
func (c *Cloudflare) Records(ctx context.Context, name Name) ([]Record, bool, error) {
	q := url.Values{"name": {name.Host}, "per_page": {"100"}}
	var found []cfRecord
	if err := c.do(ctx, http.MethodGet, "/zones/"+url.PathEscape(name.ZoneID)+"/dns_records?"+q.Encode(), nil, &found); err != nil {
		return nil, false, err
	}
	var records []Record
	cname := false
	for _, r := range found {
		switch r.Type {
		case "A", "AAAA":
			addr, err := netip.ParseAddr(r.Content)
			if err != nil {
				return nil, false, fmt.Errorf("edgedns: record %s has content %q: %w", r.ID, r.Content, err)
			}
			records = append(records, Record{ID: r.ID, Addr: addr, DNSOnly: !r.Proxied})
		case "CNAME":
			cname = true
		}
	}
	return records, cname, nil
}

// Create adds an address record for addr in name's mode.
func (c *Cloudflare) Create(ctx context.Context, name Name, addr netip.Addr) error {
	kind := "A"
	if addr.Is6() {
		kind = "AAAA"
	}
	body := map[string]any{"type": kind, "name": name.Host, "content": addr.String(), "comment": RecordComment}
	setMode(body, name)
	return c.do(ctx, http.MethodPost, "/zones/"+url.PathEscape(name.ZoneID)+"/dns_records", body, nil)
}

// SetMode switches one record to name's mode, with that mode's TTL.
func (c *Cloudflare) SetMode(ctx context.Context, name Name, id string) error {
	body := map[string]any{}
	setMode(body, name)
	return c.do(ctx, http.MethodPatch, "/zones/"+url.PathEscape(name.ZoneID)+"/dns_records/"+url.PathEscape(id), body, nil)
}

func setMode(body map[string]any, name Name) {
	body["proxied"] = !name.DNSOnly
	body["ttl"] = 1
	if name.DNSOnly {
		body["ttl"] = DNSOnlyTTL
	}
}

// Delete removes one record.
func (c *Cloudflare) Delete(ctx context.Context, name Name, id string) error {
	return c.do(ctx, http.MethodDelete, "/zones/"+url.PathEscape(name.ZoneID)+"/dns_records/"+url.PathEscape(id), nil, nil)
}

type cfEnvelope struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result json.RawMessage `json:"result"`
}

// do sends one API call and decodes its result into out. Errors carry the
// status and Cloudflare's messages, never the token.
func (c *Cloudflare) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("edgedns: %s %s: %w", method, req.URL.Path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("edgedns: %s %s: read: %w", method, req.URL.Path, err)
	}
	var env cfEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("edgedns: %s %s: status %d, unreadable body", method, req.URL.Path, resp.StatusCode)
	}
	if resp.StatusCode/100 != 2 || !env.Success {
		var msgs []string
		for _, e := range env.Errors {
			msgs = append(msgs, fmt.Sprintf("%d %s", e.Code, e.Message))
		}
		return fmt.Errorf("edgedns: %s %s: status %d: %s", method, req.URL.Path, resp.StatusCode, strings.Join(msgs, "; "))
	}
	if out != nil {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("edgedns: %s %s: decode result: %w", method, req.URL.Path, err)
		}
	}
	return nil
}
