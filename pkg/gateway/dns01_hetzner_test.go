package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/libdns/libdns"
)

// fakeHetzner is an httptest server that mimics the small subset of the
// Hetzner DNS API we use: GET /zones?name=, POST /records, DELETE /records/{id}.
// It records every call so tests can assert the wire contract (auth header,
// path, JSON shape) without depending on the real service.
type fakeHetzner struct {
	mu       sync.Mutex
	records  map[string]string // name → id
	server   *httptest.Server
	gotCalls []fakeHetznerCall
	// nextRecordID controls the record IDs returned by POST /records.
	nextRecordID int
}

type fakeHetznerCall struct {
	Method     string
	Path       string
	AuthHeader string
	Body       []byte
}

func newFakeHetzner(t *testing.T) *fakeHetzner {
	f := &fakeHetzner{records: map[string]string{}, nextRecordID: 1}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/zones", func(w http.ResponseWriter, r *http.Request) {
		f.record(r, nil)
		// List zones — we return a single zone matching the configured name.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"zones": []map[string]string{{"id": "zone-abc", "name": "example.com"}},
		})
	})
	mux.HandleFunc("/api/v1/records", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(strings.NewReader(string(body))) // restore for any later reader
		f.record(r, body)
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		var req hetznerCreateRecordReq
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.records[req.Name] = idFor(f.nextRecordID)
		f.nextRecordID++
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(hetznerRecordResp{
			Record: struct {
				ID   string `json:"id"`
				Name string `json:"name"`
				Type string `json:"type"`
			}{ID: f.records[req.Name], Name: req.Name, Type: "TXT"},
		})
	})
	mux.HandleFunc("/api/v1/records/", func(w http.ResponseWriter, r *http.Request) {
		f.record(r, nil)
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/records/")
		f.mu.Lock()
		for name, rid := range f.records {
			if rid == id {
				delete(f.records, name)
				break
			}
		}
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

// record stores the call. body may be nil when the handler reads it itself
// before calling record (we close the body in that case to avoid leaks); the
// POST handler reads the body first and hands the bytes to record so the
// body doesn't get consumed out from under the handler.
func (f *fakeHetzner) record(r *http.Request, body []byte) {
	if body == nil {
		body, _ = io.ReadAll(r.Body)
		r.Body.Close()
	}
	f.mu.Lock()
	f.gotCalls = append(f.gotCalls, fakeHetznerCall{
		Method:     r.Method,
		Path:       r.URL.Path,
		AuthHeader: r.Header.Get("Auth-API-Token"),
		Body:       body,
	})
	f.mu.Unlock()
}

func idFor(n int) string {
	const digits = "0123456789abcdef"
	if n == 0 {
		return "0"
	}
	out := ""
	for n > 0 {
		out = string(digits[n&0xf]) + out
		n >>= 4
	}
	return out
}

// TestHetznerDNSProvider_AppendCreatesTXTRecord — the wire shape (path,
// auth header, JSON body, response decode) is the contract certmagic relies
// on. If any of these drift, ACME DNS-01 challenges silently fail at
// runtime — these tests are the tripwire.
func TestHetznerDNSProvider_AppendCreatesTXTRecord(t *testing.T) {
	f := newFakeHetzner(t)
	p := newHetznerDNSProviderForTest("tok-1234", "example.com", f.server.URL+"/api/v1", f.server.Client())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	recs := []libdns.Record{libdns.TXT{
		Name: "_acme-challenge",
		TTL:  time.Minute,
		Text: "challenge-token-abc",
	}}
	out, err := p.AppendRecords(ctx, "example.com", recs)
	if err != nil {
		t.Fatalf("AppendRecords: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}
	if id, ok := recordID(out[0]); !ok || id == "" {
		t.Errorf("AppendRecords did not propagate ProviderData record ID; out[0]=%+v", out[0])
	}

	// Wire assertions: zone lookup, auth header, body shape, record-creation
	// path, response decoded. This is what blocks a future refactor from
	// silently breaking the cert-mint path.
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.gotCalls) < 2 {
		t.Fatalf("expected >=2 calls (zone lookup + record create), got %d", len(f.gotCalls))
	}
	for i, c := range f.gotCalls {
		if c.AuthHeader != "tok-1234" {
			t.Errorf("call[%d] auth = %q, want tok-1234", i, c.AuthHeader)
		}
	}
	// First call: zone lookup.
	if f.gotCalls[0].Path != "/api/v1/zones" {
		t.Errorf("zone lookup path = %q, want /api/v1/zones", f.gotCalls[0].Path)
	}
	// Second call: create TXT record.
	create := f.gotCalls[1]
	if create.Path != "/api/v1/records" {
		t.Errorf("create path = %q, want /api/v1/records", create.Path)
	}
	if create.Method != http.MethodPost {
		t.Errorf("create method = %q, want POST", create.Method)
	}
	var body hetznerCreateRecordReq
	if err := json.Unmarshal(create.Body, &body); err != nil {
		t.Fatalf("decode create body: %v", err)
	}
	if body.Value != "challenge-token-abc" || body.Type != "TXT" || body.Name != "_acme-challenge" || body.ZoneID != "zone-abc" {
		t.Errorf("create body = %+v, want TXT _acme-challenge in zone-abc", body)
	}
}

// TestHetznerDNSProvider_DeleteUsesRecordID — DeleteRecords must call DELETE
// /records/{id} using the Hetzner ID returned from AppendRecords. If this
// drifts the ACME solver leaks TXT records between renewals.
func TestHetznerDNSProvider_DeleteUsesRecordID(t *testing.T) {
	f := newFakeHetzner(t)
	p := newHetznerDNSProviderForTest("tok-1234", "example.com", f.server.URL+"/api/v1", f.server.Client())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := p.AppendRecords(ctx, "example.com", []libdns.Record{
		libdns.TXT{Name: "_acme-challenge", TTL: time.Minute, Text: "x"},
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	id, _ := recordID(out[0])
	if id == "" {
		t.Fatal("Append did not propagate record ID")
	}

	if _, err := p.DeleteRecords(ctx, "example.com", out); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	last := f.gotCalls[len(f.gotCalls)-1]
	if last.Method != http.MethodDelete {
		t.Errorf("delete method = %q, want DELETE", last.Method)
	}
	if !strings.Contains(last.Path, "/api/v1/records/"+id) {
		t.Errorf("delete path = %q, want /api/v1/records/%s", last.Path, id)
	}
}

// TestHetznerDNSProvider_DeleteSkipsUnstampedRecords — best-effort cleanup
// matches libdns convention. Records without ProviderData (i.e. records
// Append didn't create) must not produce an HTTP call.
func TestHetznerDNSProvider_DeleteSkipsUnstampedRecords(t *testing.T) {
	f := newFakeHetzner(t)
	p := newHetznerDNSProviderForTest("tok-1234", "example.com", f.server.URL+"/api/v1", f.server.Client())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := p.DeleteRecords(ctx, "example.com", []libdns.Record{
		libdns.TXT{Name: "x", Text: "y"}, // no ProviderData
	}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.gotCalls {
		if c.Path == "/api/v1/records/" {
			t.Errorf("expected no DELETE calls for un-stamped records, got %+v", c)
		}
	}
}
