package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/manifest"
)

// A compute host's builder_reserved_slots reaches node_join as
// faas_builder_reserved_slots, and node_join sizes the build slices from the
// sizing report and hands builderd and vmmd the count
// (ADR-20261118154632 amendment 1).
//
// adr: 20261118154632
func TestBuilderReservedSlotsReachNodeJoin(t *testing.T) {
	yaml := strings.Replace(validManifestYAML,
		"    - name: fsn-1\n      role: control-plane\n",
		"    - name: fsn-1\n      role: control-plane\n      address: 10.42.0.1:7100\n    - name: fsn-2\n      role: compute-only\n      address: 10.42.0.2:50051\n      builder_reserved_slots: 2\n    - name: fsn-3\n      role: compute-only\n      address: 10.42.0.3:50051\n", 1)
	m, err := manifest.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("manifest.Parse: %v", err)
	}
	if errs := m.Validate(); errs != nil {
		t.Fatalf("manifest.Validate: %v", errs)
	}
	files, err := renderManifestAnsibleFiles(m, t.TempDir())
	if err != nil {
		t.Fatalf("renderManifestAnsibleFiles: %v", err)
	}
	vars := map[string]string{}
	for _, file := range files {
		vars[filepath.Base(file.Path)] = string(file.Body)
	}
	if !strings.Contains(vars["fsn-2.yml"], "faas_builder_reserved_slots: 2\n") {
		t.Errorf("fsn-2 host vars miss its reserved builder slots:\n%s", vars["fsn-2.yml"])
	}
	for _, host := range []string{"fsn-1.yml", "fsn-3.yml"} {
		if strings.Contains(vars[host], "faas_builder_reserved_slots") {
			t.Errorf("%s declares no builder slots but renders them:\n%s", host, vars[host])
		}
	}

	body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "node_join.yml"))
	if err != nil {
		t.Fatal(err)
	}
	join := string(body)
	for _, want := range []string{
		"'--builder-reserved-slots', faas_builder_reserved_slots | string",
		"/etc/systemd/system/faas-cp-build.slice.d/90-builder-slots.conf",
		"MemoryMax={{ faas_node_sizing.build_slice_memory_max_mb }}M",
		"CPUQuota={{ faas_node_sizing.build_slice_cpu_quota_pct }}%",
		"/etc/systemd/system/faas-cp.slice.d/90-builder-slots.conf",
		"MemoryMax={{ faas_node_sizing.cp_slice_memory_max_mb }}M",
		"/etc/systemd/system/faas-builderd.service.d/zz-faas-builder-slots.conf",
		"Environment=FAAS_BUILDER_RESERVED_SLOTS={{ faas_builder_reserved_slots | default(1) | int }}",
	} {
		if !strings.Contains(join, want) {
			t.Errorf("node_join.yml lacks %q", want)
		}
	}
	if strings.Count(join, "Environment=FAAS_BUILDER_RESERVED_SLOTS=") != 2 {
		t.Errorf("node_join.yml must hand the count to both builderd and vmmd")
	}
	// The slice drop-ins must be in place before the reload that applies
	// the tenant fence, so the build slice is resized in the same rollout.
	if strings.Index(join, "90-builder-slots.conf") > strings.Index(join, "Reload systemd so the re-fenced tenant slice takes effect") {
		t.Errorf("node_join.yml writes the builder slice drop-ins after the systemd reload")
	}
}
