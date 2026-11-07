// adr: 20261105095300
package reposcan

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestDetectCompose_ExtractsAppProtocol(t *testing.T) {
	t.Parallel()
	seeds, _, _, err := detectCompose(fstest.MapFS{
		"compose.yaml": &fstest.MapFile{Data: []byte(`services:
  orders:
    build: ./orders
    x-gregale-app-protocol: GRPC
  web:
    build: ./web
`)},
	})
	if err != nil {
		t.Fatalf("detectCompose: %v", err)
	}
	got := map[string]string{}
	for _, seed := range seeds {
		got[seed.name] = seed.appProtocol
	}
	if got["orders"] != "grpc" || got["web"] != "" {
		t.Fatalf("protocols = %v, want orders=grpc web unset", got)
	}
}

func TestDetectCompose_RejectsUnknownAppProtocol(t *testing.T) {
	t.Parallel()
	_, _, _, err := detectCompose(fstest.MapFS{
		"compose.yaml": &fstest.MapFile{Data: []byte(`services:
  orders:
    build: ./orders
    x-gregale-app-protocol: thrift
`)},
	})
	if err == nil || !strings.Contains(err.Error(), "must be http1, http2 or grpc") {
		t.Fatalf("detectCompose error = %v, want protocol validation", err)
	}
}

func TestDetectCompose_AppProtocolNeedsAWorkload(t *testing.T) {
	t.Parallel()
	_, _, _, err := detectCompose(fstest.MapFS{
		"compose.yaml": &fstest.MapFile{Data: []byte(`services:
  cache:
    image: redis:7
    x-gregale-app-protocol: grpc
`)},
	})
	if err == nil || !strings.Contains(err.Error(), "requires a deployable workload") {
		t.Fatalf("detectCompose error = %v, want workload requirement", err)
	}
}

// The protocol reaches the scanned Workload.
func TestScan_ComposeAppProtocolReachesWorkload(t *testing.T) {
	t.Parallel()
	res, err := Scan(fstest.MapFS{
		"compose.yaml": &fstest.MapFile{Data: []byte(`services:
  orders:
    build: ./orders
    x-gregale-app-protocol: grpc
`)},
		"orders/Dockerfile": &fstest.MapFile{Data: []byte("FROM scratch\n")},
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	for _, w := range res.Workloads {
		if w.Name == "orders" {
			if w.AppProtocol != "grpc" {
				t.Fatalf("orders AppProtocol = %q, want grpc", w.AppProtocol)
			}
			return
		}
	}
	t.Fatalf("no orders workload: %+v", res.Workloads)
}
