package daemonunitspec

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestControlPlaneSliceLimitsComeFromOperatorVariables reproduces
// production-us on 2026-10-05. The control plane has 3.9 GB of RAM, so the
// operator set faas-cp.slice to MemoryMax=3072M by hand. The role wrote a
// literal 6144M, and every convergence put the cap back above the host's RAM.
// The limits must come from variables whose defaults keep the spec §13
// budget, and the role must reject a value systemd would misread.
func TestControlPlaneSliceLimitsComeFromOperatorVariables(t *testing.T) {
	roleDir := filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "systemd_slices")

	body, err := os.ReadFile(filepath.Join(roleDir, "defaults", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var defaults map[string]any
	if err := yaml.Unmarshal(body, &defaults); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	// 6144M is the same budget as FaasCPSliceMemoryMax (6G).
	if FaasCPSliceMemoryMax != "6G" || defaults["faas_cp_slice_memory_max"] != "6144M" {
		t.Errorf("faas_cp_slice_memory_max default = %v with FaasCPSliceMemoryMax %q, want 6144M matching 6G",
			defaults["faas_cp_slice_memory_max"], FaasCPSliceMemoryMax)
	}
	if defaults["faas_cp_slice_cpu_quota"] != "400%" {
		t.Errorf("faas_cp_slice_cpu_quota default = %v, want 400%%", defaults["faas_cp_slice_cpu_quota"])
	}

	var content string
	var patterns []string
	for _, task := range flattenRoleTasks("tasks/main.yml", loadYAMLList(t, filepath.Join(roleDir, "tasks", "main.yml"))) {
		name, _ := task.body["name"].(string)
		switch name {
		case "systemd_slices — write faas-cp.slice":
			args, _ := task.body["ansible.builtin.copy"].(map[string]any)
			content, _ = args["content"].(string)
		case "systemd_slices — validate faas-cp.slice limits":
			args, _ := task.body["ansible.builtin.assert"].(map[string]any)
			that, _ := args["that"].([]any)
			for _, cond := range that {
				if m := regexp.MustCompile(`match\('([^']+)'\)`).FindStringSubmatch(cond.(string)); m != nil {
					patterns = append(patterns, m[1])
				}
			}
		}
	}

	for _, want := range []string{
		"MemoryMax={{ faas_cp_slice_memory_max }}",
		"CPUQuota={{ faas_cp_slice_cpu_quota }}",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("faas-cp.slice content missing %q:\n%s", want, content)
		}
	}
	if regexp.MustCompile(`(?m)^(MemoryMax|CPUQuota)=[0-9]`).MatchString(content) {
		t.Errorf("faas-cp.slice still hard-codes a limit:\n%s", content)
	}

	if len(patterns) != 2 {
		t.Fatalf("validate task has %d match() patterns, want 2 (memory, cpu): %v", len(patterns), patterns)
	}
	memory, cpu := regexp.MustCompile(patterns[0]), regexp.MustCompile(patterns[1])
	for _, tc := range []struct {
		re    *regexp.Regexp
		value string
		ok    bool
	}{
		{memory, "6144M", true},
		{memory, "3072M", true},
		{memory, "6G", true},
		{memory, "3221225472", true},
		{memory, "3072MB", false},
		{memory, "3 G", false},
		{memory, "", false},
		{memory, "infinity", false},
		{cpu, "400%", true},
		{cpu, "200%", true},
		{cpu, "400", false},
		{cpu, "", false},
	} {
		if got := tc.re.MatchString(tc.value); got != tc.ok {
			t.Errorf("%s matches %q = %v, want %v", tc.re, tc.value, got, tc.ok)
		}
	}
}
