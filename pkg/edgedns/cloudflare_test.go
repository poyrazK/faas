// adr: 20261102081618
package edgedns_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/edgedns"
)

func TestCloudflareReadsCreatesAndDeletesRecords(t *testing.T) {
	var calls []string
	var created map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		switch r.Method {
		case http.MethodGet:
			_, _ = io.WriteString(w, `{"success":true,"result":[
				{"id":"r1","type":"A","content":"203.0.113.1"},
				{"id":"r2","type":"AAAA","content":"2001:db8::1"},
				{"id":"r3","type":"TXT","content":"hello"}]}`)
		case http.MethodPost:
			_ = json.NewDecoder(r.Body).Decode(&created)
			_, _ = io.WriteString(w, `{"success":true,"result":{}}`)
		default:
			_, _ = io.WriteString(w, `{"success":true,"result":{}}`)
		}
	}))
	defer srv.Close()
	cf, err := edgedns.NewCloudflare(srv.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	records, cname, err := cf.Records(ctx, wildcard)
	if err != nil || cname || len(records) != 2 || records[1].Addr != netip.MustParseAddr("2001:db8::1") {
		t.Fatalf("Records = %+v %v %v", records, cname, err)
	}
	if err := cf.Create(ctx, wildcard, netip.MustParseAddr("2001:db8::2")); err != nil {
		t.Fatal(err)
	}
	if created["type"] != "AAAA" || created["proxied"] != true || created["ttl"] != float64(1) || created["name"] != "*.gregale.dev" || created["comment"] != edgedns.RecordComment {
		t.Fatalf("create body = %v", created)
	}
	if err := cf.Delete(ctx, wildcard, "r1"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"GET /zones/zone-1/dns_records?name=%2A.gregale.dev&per_page=100",
		"POST /zones/zone-1/dns_records",
		"DELETE /zones/zone-1/dns_records/r1",
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(calls, "\n"), strings.Join(want, "\n"))
	}
}

func TestCloudflareReportsACNAMEAndErrorsWithoutTheToken(t *testing.T) {
	cnameOK := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cnameOK {
			_, _ = io.WriteString(w, `{"success":true,"result":[{"id":"c1","type":"CNAME","content":"x.cfargotunnel.com"}]}`)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"success":false,"errors":[{"code":10000,"message":"Authentication error"}]}`)
	}))
	defer srv.Close()
	cf, _ := edgedns.NewCloudflare(srv.URL, "secret-token")
	records, cname, err := cf.Records(context.Background(), wildcard)
	if err != nil || !cname || len(records) != 0 {
		t.Fatalf("Records = %v %v %v, want only the CNAME flag", records, cname, err)
	}
	cnameOK = false
	err = cf.Create(context.Background(), wildcard, addrA)
	if err == nil || !strings.Contains(err.Error(), "10000 Authentication error") || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("err = %v, want Cloudflare's message and no token", err)
	}
	if _, err := edgedns.NewCloudflare(srv.URL, " "); err == nil {
		t.Fatal("an empty token was accepted")
	}
}

func TestHTTPProbe(t *testing.T) {
	ready := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" || !ready {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()
	p := edgedns.HTTPProbe{}
	if !p.Ready(context.Background(), srv.URL+"/readyz") {
		t.Fatal("a ready edge read as unready")
	}
	ready = false
	if p.Ready(context.Background(), srv.URL+"/readyz") {
		t.Fatal("an unready edge read as ready")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := "http://" + ln.Addr().String() + "/readyz"
	_ = ln.Close()
	if p.Ready(context.Background(), closed) {
		t.Fatal("a refused probe read as ready")
	}
}
