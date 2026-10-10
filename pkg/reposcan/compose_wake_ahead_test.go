// adr: 950
package reposcan

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestDetectCompose_ExtractsServiceWakeAhead(t *testing.T) {
	t.Parallel()
	seeds, _, _, err := detectCompose(fstest.MapFS{
		"compose.yaml": &fstest.MapFile{Data: []byte(`services:
  api:
    build: ./api
    depends_on: [billing]
    x-gregale-service-wake-ahead: Declared
  billing:
    build: ./billing
    x-gregale-service-wake-ahead: off
  worker:
    build: ./worker
`)},
	})
	if err != nil {
		t.Fatalf("detectCompose: %v", err)
	}
	got := make(map[string]api.ServiceWakeAhead, len(seeds))
	for _, seed := range seeds {
		got[seed.name] = seed.serviceWakeAhead
	}
	want := map[string]api.ServiceWakeAhead{"api": api.ServiceWakeAheadDeclared, "billing": api.ServiceWakeAheadOff, "worker": ""}
	for name, mode := range want {
		if got[name] != mode {
			t.Fatalf("%s wake-ahead = %q, want %q (all: %#v)", name, got[name], mode, got)
		}
	}
}

func TestDetectCompose_RejectsUnknownServiceWakeAhead(t *testing.T) {
	t.Parallel()
	_, _, _, err := detectCompose(fstest.MapFS{
		"compose.yaml": &fstest.MapFile{Data: []byte(`services:
  api:
    build: ./api
    x-gregale-service-wake-ahead: always
`)},
	})
	if err == nil || !strings.Contains(err.Error(), "x-gregale-service-wake-ahead must be off or declared") {
		t.Fatalf("detectCompose error = %v, want closed wake-ahead validation", err)
	}
}

// The extension sits on the caller; a Compose entry with no build or image is
// not a workload and cannot opt in.
func TestDetectCompose_RejectsServiceWakeAheadWithoutWorkload(t *testing.T) {
	t.Parallel()
	_, _, _, err := detectCompose(fstest.MapFS{
		"compose.yaml": &fstest.MapFile{Data: []byte(`services:
  api:
    build: ./api
  sidecar-config:
    x-gregale-service-wake-ahead: declared
`)},
	})
	if err == nil || !strings.Contains(err.Error(), "x-gregale-service-wake-ahead requires a deployable workload") {
		t.Fatalf("detectCompose error = %v, want deployable-workload rejection", err)
	}
}
