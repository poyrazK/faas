package geoip

import (
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// ADR-966: ASN lookups are fail-open like country lookups, and the ASN
// watcher downloads the ASN file, not the country one.
func TestLookupASN_EmptyAndNil(t *testing.T) {
	var nilReader *Reader
	if asn, org, ok, err := nilReader.LookupASN(net.ParseIP("203.0.113.9")); asn != 0 || org != "" || ok || err != nil {
		t.Fatalf("nil reader = (%d, %q, %v, %v)", asn, org, ok, err)
	}
	if _, _, ok, err := (&Reader{}).LookupASN(net.ParseIP("203.0.113.9")); ok || err != nil {
		t.Fatalf("empty reader = (%v, %v)", ok, err)
	}
	if _, _, ok, _ := (&Reader{}).LookupASN(nil); ok {
		t.Fatal("nil IP matched")
	}
}

func TestNewWatcherForURL(t *testing.T) {
	w, err := NewWatcherForURL(&Reader{}, time.Hour, DBIPASNDownloadURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	url := w.urlFor(time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC))
	if !strings.Contains(url, "dbip-asn-lite-2026-10.mmdb.gz") {
		t.Fatalf("url = %q", url)
	}
	if _, err := NewWatcherForURL(nil, time.Hour, DBIPASNDownloadURL, nil); err == nil {
		t.Fatal("nil reader accepted")
	}
}

// TestLookupASN_RealDatabase runs against a downloaded DB-IP ASN Lite file
// when FAAS_GEOIP_ASN_TEST_DB points at one.
func TestLookupASN_RealDatabase(t *testing.T) {
	path := os.Getenv("FAAS_GEOIP_ASN_TEST_DB")
	if path == "" {
		t.Skip("FAAS_GEOIP_ASN_TEST_DB not set")
	}
	r, err := Open(path, SourceDBIP, DBIPAttribution, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	for ip, want := range map[string]uint32{"1.1.1.1": 13335, "8.8.8.8": 15169, "2606:4700:4700::1111": 13335} {
		asn, org, ok, err := r.LookupASN(net.ParseIP(ip))
		if err != nil || !ok || asn != want {
			t.Errorf("%s = (%d, %q, %v, %v), want %d", ip, asn, org, ok, err, want)
		}
	}
	if _, _, ok, _ := r.LookupASN(net.ParseIP("10.0.0.1")); ok {
		t.Error("RFC 1918 address resolved to an ASN")
	}
}
