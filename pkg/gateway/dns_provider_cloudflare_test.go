// Tests for CloudflareRecordProvider (Tier A8 / ADR-083 /
// code-review fix #3 + #7).
// adr: 083

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// decodeJSON reads the request body into the target and
// resets the body for downstream readers. Used in the
// httptest handlers when we want to inspect what the client
// PUT.
func decodeJSON(r io.Reader, v any) error {
	return json.NewDecoder(r).Decode(v)
}

// stubOpenBytes substitutes the openDNSProviderToken seam with
// a deterministic plaintext. Restored via t.Cleanup so a parallel
// test can't poison the package-level var.
func stubOpenBytes(t *testing.T, plaintext []byte) {
	t.Helper()
	prev := openDNSProviderToken
	openDNSProviderToken = func(_ []byte) ([]byte, error) {
		out := make([]byte, len(plaintext))
		copy(out, plaintext)
		return out, nil
	}
	t.Cleanup(func() { openDNSProviderToken = prev })
}

// stubOpenBytesError makes the unseal helper fail. Used to
// verify that token-missing errors propagate from
// NewCloudflareRecordProvider instead of silently returning nil.
func stubOpenBytesError(t *testing.T, err error) {
	t.Helper()
	prev := openDNSProviderToken
	openDNSProviderToken = func(_ []byte) ([]byte, error) { return nil, err }
	t.Cleanup(func() { openDNSProviderToken = prev })
}

// Test: constructor refuses empty zone / empty token.
func TestCloudflare_Constructor_RejectsEmpty(t *testing.T) {
	if _, err := NewCloudflareRecordProvider(DNSProviderConfig{Zone: "", SealedToken: []byte("x")}); err == nil {
		t.Error("empty zone: want error")
	}
	if _, err := NewCloudflareRecordProvider(DNSProviderConfig{Zone: "example.com", SealedToken: nil}); err == nil {
		t.Error("empty token: want error")
	}
}

// Test: missing/unseal-fail token errors propagate from the
// constructor (review finding #3 — production boot with no
// sealed token must NOT silently succeed).
func TestCloudflare_Constructor_PropagatesUnsealError(t *testing.T) {
	stubOpenBytesError(t, errors.New("fake: secretbox not configured"))
	_, err := NewCloudflareRecordProvider(DNSProviderConfig{
		Zone:        "example.com",
		SealedToken: []byte("opaque"),
	})
	if err == nil {
		t.Fatal("constructor returned nil error with sealed-only token; want unseal error to propagate")
	}
	if !strings.Contains(err.Error(), "unseal") {
		t.Errorf("error %q should mention 'unseal' so the operator sees the real cause", err)
	}
}

func TestCloudflare_RejectsOversizedZoneResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"result":"`+strings.Repeat("x", cloudflareResponseMaxBytes)+`"}`)
	}))
	defer srv.Close()
	p := &CloudflareRecordProvider{zone: "example.com", apiURL: srv.URL, hc: srv.Client()}
	if _, err := p.queryZoneID(context.Background()); err == nil || !strings.Contains(err.Error(), "response body too large") {
		t.Fatalf("expected response-size error, got %v", err)
	}
}

// Test: UpsertRecord happy path — record doesn't exist → POST /dns_records.
// Asserts: exactly one POST, no PUT (create vs update split).
func TestCloudflare_UpsertRecord_CreatesNewRecord(t *testing.T) {
	var postCalls, putCalls, getCalls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCalls.Add(1)
			// /zones?name=... → return matching zone
			if r.URL.Path == "/zones" {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"result":[{"id":"zone-1","name":"example.com"}]}`)
				return
			}
			// /zones/<id>/dns_records → empty
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"result":[]}`)
		case http.MethodPost:
			postCalls.Add(1)
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"result":{"id":"rec-1"}}`)
		case http.MethodPut:
			putCalls.Add(1)
		}
	}))
	defer srv.Close()

	stubOpenBytes(t, []byte("cf-token-xyz"))
	p, err := NewCloudflareRecordProvider(DNSProviderConfig{
		Zone:        "example.com",
		SealedToken: []byte("opaque"),
		APIURL:      srv.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.UpsertRecord(context.Background(), "faas-node-a.example.com", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	if postCalls.Load() != 1 {
		t.Errorf("POST calls = %d, want 1 (record didn't exist → create)", postCalls.Load())
	}
	if putCalls.Load() != 0 {
		t.Errorf("PUT calls = %d, want 0 (no existing record to update)", putCalls.Load())
	}
}

// Test: UpsertRecord idempotent — record exists with same value → no PUT.
// Code-review fix #7: when the cached zoneID path fires, GET
// /zones should NOT re-run; this test also asserts the cache
// is honoured on the second UpsertRecord call.
func TestCloudflare_UpsertRecord_UpdatesExistingRecord(t *testing.T) {
	var postCalls, putCalls, getZonesCalls atomic.Int64
	// Track the current record content so a successful PUT
	// is reflected on the next GET (mirrors Cloudflare's real
	// behaviour: GET /dns_records reads what was last PUT).
	var currentContent atomic.Value
	currentContent.Store("9.9.9.9")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if r.URL.Path == "/zones" {
				getZonesCalls.Add(1)
				fmt.Fprint(w, `{"result":[{"id":"zone-1","name":"example.com"}]}`)
				return
			}
			fmt.Fprintf(w, `{"result":[{"id":"rec-1","type":"A","name":"faas-node-a.example.com","content":%q,"ttl":60}]}`,
				currentContent.Load())
		case http.MethodPost:
			postCalls.Add(1)
		case http.MethodPut:
			putCalls.Add(1)
			// Read the body to learn what was PUT so the
			// next GET reflects it.
			var body struct {
				Content string `json:"content"`
			}
			_ = decodeJSON(r.Body, &body)
			currentContent.Store(body.Content)
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"result":{"id":"rec-1"}}`)
		}
	}))
	defer srv.Close()

	stubOpenBytes(t, []byte("cf-token-xyz"))
	p, _ := NewCloudflareRecordProvider(DNSProviderConfig{
		Zone: "example.com", SealedToken: []byte("opaque"), APIURL: srv.URL,
	})
	// First call: zoneID resolve + findRecord (9.9.9.9 ≠ 1.2.3.4) → PUT.
	if err := p.UpsertRecord(context.Background(), "faas-node-a.example.com", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	// Second call: same value → idempotent no-op (no PUT, no extra GET /zones).
	if err := p.UpsertRecord(context.Background(), "faas-node-a.example.com", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	if putCalls.Load() != 1 {
		t.Errorf("PUT calls = %d, want 1 (only the first call updates)", putCalls.Load())
	}
	if postCalls.Load() != 0 {
		t.Errorf("POST calls = %d, want 0 (record already existed)", postCalls.Load())
	}
	if getZonesCalls.Load() != 1 {
		t.Errorf("GET /zones calls = %d, want 1 (zoneID cached after first resolve)", getZonesCalls.Load())
	}
}

// Test: DeleteRecord on a non-existent record → idempotent no-op (no DELETE call).
func TestCloudflare_DeleteRecord_IdempotentOnMissing(t *testing.T) {
	var deleteCalls, getCalls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCalls.Add(1)
			if r.URL.Path == "/zones" {
				fmt.Fprint(w, `{"result":[{"id":"zone-1","name":"example.com"}]}`)
				return
			}
			fmt.Fprint(w, `{"result":[]}`) // no records
		case http.MethodDelete:
			deleteCalls.Add(1)
		}
	}))
	defer srv.Close()
	stubOpenBytes(t, []byte("cf-token-xyz"))
	p, _ := NewCloudflareRecordProvider(DNSProviderConfig{Zone: "example.com", SealedToken: []byte("opaque"), APIURL: srv.URL})
	if err := p.DeleteRecord(context.Background(), "faas-node-a.example.com"); err != nil {
		t.Fatal(err)
	}
	if deleteCalls.Load() != 0 {
		t.Errorf("DELETE calls = %d, want 0 (record didn't exist → no API call)", deleteCalls.Load())
	}
}

// Code-review fix #7: resolveZoneID cache hit / refresh /
// clear-on-error. Three cases:
//
//	(1) Cached on second call: GET /zones fires ONCE across
//	    multiple UpsertRecords on the same provider.
//	(2) Refreshes after TTL: a synthetic stale cache state
//	    triggers another GET /zones call.
//	(3) Clears on error: a 500 response on the first call
//	    means the cache stays empty, and the next call retries.
func TestCloudflare_ResolveZoneID_CachedOnSecondCall(t *testing.T) {
	var getZones atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/zones" {
			getZones.Add(1)
			fmt.Fprint(w, `{"result":[{"id":"zone-1","name":"example.com"}]}`)
			return
		}
		fmt.Fprint(w, `{"result":[]}`)
	}))
	defer srv.Close()

	stubOpenBytes(t, []byte("cf-token-xyz"))
	p, _ := NewCloudflareRecordProvider(DNSProviderConfig{Zone: "example.com", SealedToken: []byte("opaque"), APIURL: srv.URL})

	// First resolve → 1 GET /zones.
	if id, err := p.resolveZoneID(context.Background()); err != nil || id != "zone-1" {
		t.Fatalf("first resolve: id=%q err=%v", id, err)
	}
	// Second resolve → cache hit, NO new GET /zones.
	if id, err := p.resolveZoneID(context.Background()); err != nil || id != "zone-1" {
		t.Fatalf("second resolve: id=%q err=%v", id, err)
	}
	if got := getZones.Load(); got != 1 {
		t.Errorf("GET /zones = %d, want 1 (cache hit on second call)", got)
	}
}

func TestCloudflare_ResolveZoneID_RefreshesAfterTTL(t *testing.T) {
	var getZones atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/zones" {
			getZones.Add(1)
			fmt.Fprint(w, `{"result":[{"id":"zone-1","name":"example.com"}]}`)
			return
		}
		fmt.Fprint(w, `{"result":[]}`)
	}))
	defer srv.Close()

	stubOpenBytes(t, []byte("cf-token-xyz"))
	p, _ := NewCloudflareRecordProvider(DNSProviderConfig{Zone: "example.com", SealedToken: []byte("opaque"), APIURL: srv.URL})

	// Force the cache into a stale state: stamp cachedAt to
	// (now - 2 * TTL). The next resolve should refresh.
	p.zoneIDCached.Store("zone-1")
	p.zoneIDCachedAt.Store(time.Now().Add(-2 * cloudflareZoneIDCacheTTL).UnixNano())

	if id, err := p.resolveZoneID(context.Background()); err != nil || id != "zone-1" {
		t.Fatalf("stale resolve: id=%q err=%v", id, err)
	}
	if got := getZones.Load(); got != 1 {
		t.Errorf("GET /zones = %d, want 1 (stale cache triggered refresh)", got)
	}
}

func TestCloudflare_ResolveZoneID_ClearsOnError(t *testing.T) {
	var getZones atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/zones" {
			getZones.Add(1)
			// First call: 500. Second call: 200.
			if getZones.Load() == 1 {
				http.Error(w, "server is on fire", http.StatusInternalServerError)
				return
			}
			fmt.Fprint(w, `{"result":[{"id":"zone-1","name":"example.com"}]}`)
		}
	}))
	defer srv.Close()

	stubOpenBytes(t, []byte("cf-token-xyz"))
	p, _ := NewCloudflareRecordProvider(DNSProviderConfig{Zone: "example.com", SealedToken: []byte("opaque"), APIURL: srv.URL})

	// First call: 500 → error, cache cleared.
	if _, err := p.resolveZoneID(context.Background()); err == nil {
		t.Fatal("first resolve: want error on 500")
	}
	if v := p.zoneIDCached.Load(); v != nil && v != "" {
		t.Errorf("cache must clear on error; got %v", v)
	}
	// Second call: 200 → succeeds, cache repopulates.
	if id, err := p.resolveZoneID(context.Background()); err != nil || id != "zone-1" {
		t.Fatalf("second resolve after error: id=%q err=%v", id, err)
	}
	if getZones.Load() != 2 {
		t.Errorf("GET /zones = %d, want 2 (cache clear → retry)", getZones.Load())
	}
}

// Test: the empty-arg reject (matches Hetzner shape).
func TestCloudflare_UpsertRecord_RejectsEmpty(t *testing.T) {
	stubOpenBytes(t, []byte("cf-token-xyz"))
	p, _ := NewCloudflareRecordProvider(DNSProviderConfig{Zone: "example.com", SealedToken: []byte("opaque"), APIURL: "http://example.invalid"})
	if err := p.UpsertRecord(context.Background(), "", "1.2.3.4"); err == nil {
		t.Error("empty name: want error")
	}
	if err := p.UpsertRecord(context.Background(), "faas-node-a.example.com", ""); err == nil {
		t.Error("empty value: want error")
	}
}
