// adr: 568
package reposcan

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestParseExpose(t *testing.T) {
	tests := []struct {
		name      string
		items     []any
		wantPorts []int
		warnings  int
		wantNil   bool
	}{
		{name: "absent", wantNil: true},
		{name: "explicitly empty", items: []any{}, wantPorts: []int{}},
		{name: "int and string forms", items: []any{6379, "5432", "9000/tcp", "9000"}, wantPorts: []int{6379, 5432, 9000}},
		{name: "udp dropped", items: []any{"53/udp", 6379}, wantPorts: []int{6379}, warnings: 1},
		{name: "range dropped", items: []any{"8000-8010"}, wantPorts: []int{}, warnings: 1},
		{name: "out of range dropped", items: []any{0, 70000}, wantPorts: []int{}, warnings: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ports, warnings := parseExpose(tt.items)
			if tt.wantNil {
				if ports != nil {
					t.Fatalf("ports = %v, want nil for an absent expose:", ports)
				}
				return
			}
			if ports == nil {
				t.Fatal("declared expose: returned nil")
			}
			var got []int
			for _, port := range ports {
				if !port.Internal || port.Protocol != api.WorkloadPortTCP {
					t.Fatalf("port %+v is not an internal TCP listener", port)
				}
				got = append(got, port.Port)
			}
			if !slices.Equal(got, tt.wantPorts) && !(len(got) == 0 && len(tt.wantPorts) == 0) {
				t.Fatalf("ports = %v, want %v", got, tt.wantPorts)
			}
			if len(warnings) != tt.warnings {
				t.Fatalf("warnings = %v, want %d", warnings, tt.warnings)
			}
		})
	}
	many := make([]any, 0, api.WorkloadPortCapMax+2)
	for port := 7000; port < 7000+api.WorkloadPortCapMax+2; port++ {
		many = append(many, port)
	}
	ports, warnings := parseExpose(many)
	if len(ports) != api.WorkloadPortCapMax || len(warnings) != 2 {
		t.Fatalf("over the cap: %d ports, %d warnings", len(ports), len(warnings))
	}
}

func TestDetectComposeExposeBecomesInternalPorts(t *testing.T) {
	fsys := fstest.MapFS{
		"compose.yaml": &fstest.MapFile{Data: []byte(`services:
  api:
    build: ./api
    ports: ["8080:8080"]
    depends_on: [cache]
  cache:
    build: ./cache
    expose: ["6379", "53/udp"]
`)},
	}
	seeds, _, warnings, err := detectCompose(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "cache") || !strings.Contains(warnings[0], "53/udp") {
		t.Fatalf("warnings = %v, want one naming the dropped UDP entry", warnings)
	}
	for _, workload := range mergeByKey(seeds) {
		switch workload.Name {
		case "cache":
			if len(workload.InternalPorts) != 1 || workload.InternalPorts[0].Port != 6379 || !workload.InternalPorts[0].Internal {
				t.Fatalf("cache internal ports = %+v, want internal 6379", workload.InternalPorts)
			}
		case "api":
			if workload.InternalPorts != nil {
				t.Fatalf("api without expose: has internal ports %+v", workload.InternalPorts)
			}
		}
	}
}
