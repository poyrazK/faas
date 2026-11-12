// adr: 20261103163954
package edgedns_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/libdns/libdns"

	"github.com/onebox-faas/faas/pkg/edgedns"
)

// The challenge record goes to the configured zone under its absolute name,
// DNS-only, and is deleted by the id Cloudflare gave it.
func TestDNS01CreatesAndDeletesChallengeRecords(t *testing.T) {
	var calls []string
	var created map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPost {
			_ = json.NewDecoder(r.Body).Decode(&created)
			_, _ = io.WriteString(w, `{"success":true,"result":{"id":"txt-1"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"success":true,"result":{}}`)
	}))
	defer srv.Close()
	cf, err := edgedns.NewCloudflare(srv.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	dns, err := cf.DNS01("zone-1")
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	appended, err := dns.AppendRecords(ctx, "gregale.dev.", []libdns.Record{libdns.TXT{Name: "_acme-challenge", TTL: time.Minute, Text: "token"}})
	if err != nil || len(appended) != 1 {
		t.Fatalf("AppendRecords = %v, %v", appended, err)
	}
	if created["type"] != "TXT" || created["name"] != "_acme-challenge.gregale.dev" || created["content"] != "token" || created["ttl"] != float64(edgedns.DNSOnlyTTL) {
		t.Fatalf("create body = %v", created)
	}
	if _, ok := created["proxied"]; ok {
		t.Fatalf("a TXT record carries no proxy mode: %v", created)
	}
	deleted, err := dns.DeleteRecords(ctx, "gregale.dev.", append(appended, libdns.TXT{Name: "_acme-challenge", Text: "not ours"}))
	if err != nil || len(deleted) != 1 {
		t.Fatalf("DeleteRecords = %v, %v", deleted, err)
	}
	want := []string{"POST /zones/zone-1/dns_records", "DELETE /zones/zone-1/dns_records/txt-1"}
	if len(calls) != len(want) || calls[0] != want[0] || calls[1] != want[1] {
		t.Fatalf("calls = %q, want %q", calls, want)
	}
}

func TestDNS01RefusesOtherRecordTypes(t *testing.T) {
	cf, err := edgedns.NewCloudflare("http://127.0.0.1:0", "tok")
	if err != nil {
		t.Fatal(err)
	}
	dns, err := cf.DNS01("zone-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dns.AppendRecords(t.Context(), "gregale.dev.", []libdns.Record{libdns.RR{Name: "x", Type: "A", Data: "192.0.2.1"}}); err == nil {
		t.Fatal("AppendRecords wrote an A record")
	}
	if _, err := cf.DNS01(" "); err == nil {
		t.Fatal("DNS01 accepted an empty zone id")
	}
}
