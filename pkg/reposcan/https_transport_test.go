package reposcan

import (
	"strings"
	"testing"
	"testing/fstest"
)

// ADR-20261029230000: where private service HTTPS is off, a service that asks
// for the https transport fails the scan with the reason; elsewhere, and in an
// offline scan, it scans as before.
func TestScanRefusesHTTPSTransportWhereUnavailable(t *testing.T) {
	fsys := fstest.MapFS{"compose.yaml": &fstest.MapFile{Data: []byte(`services:
  web:
    image: ghcr.io/x/web:1
    ports: ["80:3000"]
    depends_on: [api]
    x-gregale-service-transport: https
  api: {image: ghcr.io/x/api:1, expose: ["8000"]}
`)}}
	if _, err := Scan(fsys); err != nil {
		t.Fatalf("offline scan: %v", err)
	}
	if _, err := ScanWithOptions(fsys, ScanOptions{}); err != nil {
		t.Fatalf("scan with private HTTPS: %v", err)
	}
	_, err := ScanWithOptions(fsys, ScanOptions{HTTPSTransportUnavailable: true})
	if err == nil || !strings.Contains(err.Error(), `service "web" sets x-gregale-service-transport: https`) ||
		!strings.Contains(err.Error(), "private-service-https") {
		t.Fatalf("scan without private HTTPS: err = %v, want the refusal naming web and the capability", err)
	}

	plain := fstest.MapFS{"compose.yaml": &fstest.MapFile{Data: []byte(`services:
  web: {image: ghcr.io/x/web:1, ports: ["80:3000"], x-gregale-service-transport: http}
`)}}
	if _, err := ScanWithOptions(plain, ScanOptions{HTTPSTransportUnavailable: true}); err != nil {
		t.Fatalf("http transport without private HTTPS: %v", err)
	}
}
