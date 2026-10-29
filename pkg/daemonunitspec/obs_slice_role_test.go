package daemonunitspec

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestObservabilityUnitsRunInTheCappedSlice pins ADR-20261023124955 §1.
// Prometheus, Alertmanager and Grafana used to run in system.slice with no
// memory limit, beside PostgreSQL on a 2-vCPU control plane, and inside a
// 2,048 MB budget meant for the OS. They must run in faas-obs.slice, whose
// limits come from operator variables, and each must carry its own memory
// bound so one of them cannot starve the others inside the slice.
func TestObservabilityUnitsRunInTheCappedSlice(t *testing.T) {
	roles := filepath.Join(repoRoot(t), "deploy", "ansible", "roles")

	sliceDefaults := readRoleDefaults(t, filepath.Join(roles, "systemd_slices"))
	for key, want := range map[string]string{
		"faas_obs_slice_memory_max": "3584M",
		"faas_obs_slice_cpu_weight": "20",
		"faas_obs_slice_cpu_quota":  "100%",
		"faas_obs_slice_io_weight":  "20",
	} {
		if got := yamlScalar(sliceDefaults[key]); got != want {
			t.Errorf("systemd_slices default %s = %q, want %q", key, got, want)
		}
	}

	var sliceContent string
	var validated bool
	tasksPath := filepath.Join(roles, "systemd_slices", "tasks", "main.yml")
	for _, task := range flattenRoleTasks("tasks/main.yml", loadYAMLList(t, tasksPath)) {
		name, _ := task.body["name"].(string)
		switch name {
		case "systemd_slices — write faas-obs.slice":
			args, _ := task.body["ansible.builtin.copy"].(map[string]any)
			sliceContent, _ = args["content"].(string)
		case "systemd_slices — validate faas-obs.slice limits":
			validated = true
		}
	}
	if !validated {
		t.Error("systemd_slices has no task validating the faas-obs.slice variables")
	}
	for _, want := range []string{
		"MemoryMax={{ faas_obs_slice_memory_max }}",
		"CPUWeight={{ faas_obs_slice_cpu_weight }}",
		"CPUQuota={{ faas_obs_slice_cpu_quota }}",
		"IOWeight={{ faas_obs_slice_io_weight }}",
	} {
		if !strings.Contains(sliceContent, want) {
			t.Errorf("faas-obs.slice content missing %q:\n%s", want, sliceContent)
		}
	}

	units := []struct {
		path string
		want []string
	}{
		{filepath.Join(roles, "prometheus", "templates", "prometheus.service.j2"), []string{
			"MemoryHigh={{ prom_memory_high }}",
			"Environment=GOMEMLIMIT={{ prom_gomemlimit }}",
		}},
		{filepath.Join(roles, "grafana", "templates", "grafana-server.service.j2"), []string{
			"MemoryHigh={{ gv_memory_high }}",
			"MemoryMax={{ gv_memory_max }}",
		}},
		{filepath.Join(roles, "alertmanager", "files", "alertmanager.service"), []string{
			"MemoryMax=128M",
		}},
	}
	for _, u := range units {
		body, err := os.ReadFile(u.path)
		if err != nil {
			t.Fatal(err)
		}
		service := serviceSection(string(body))
		for _, want := range append([]string{"Slice=faas-obs.slice"}, u.want...) {
			if !regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(want) + `$`).MatchString(service) {
				t.Errorf("%s [Service] missing %q", filepath.Base(u.path), want)
			}
		}
	}

	// GOMEMLIMIT must sit below MemoryHigh, or the Go collector only starts
	// working hard after the cgroup is already throttling Prometheus.
	prom := readRoleDefaults(t, filepath.Join(roles, "prometheus"))
	high := byteSize(t, yamlScalar(prom["prom_memory_high"]))
	limit := byteSize(t, yamlScalar(prom["prom_gomemlimit"]))
	if limit >= high {
		t.Errorf("prom_gomemlimit %s >= prom_memory_high %s", yamlScalar(prom["prom_gomemlimit"]), yamlScalar(prom["prom_memory_high"]))
	}
	// Every member's own bound must fit inside the slice's hard fence.
	grafana := readRoleDefaults(t, filepath.Join(roles, "grafana"))
	fence := byteSize(t, yamlScalar(sliceDefaults["faas_obs_slice_memory_max"]))
	for name, v := range map[string]string{
		"prom_memory_high": yamlScalar(prom["prom_memory_high"]),
		"gv_memory_max":    yamlScalar(grafana["gv_memory_max"]),
	} {
		if byteSize(t, v) > fence {
			t.Errorf("%s %s exceeds faas_obs_slice_memory_max", name, v)
		}
	}
}

func readRoleDefaults(t *testing.T, roleDir string) map[string]any {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(roleDir, "defaults", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var defaults map[string]any
	if err := yaml.Unmarshal(body, &defaults); err != nil {
		t.Fatalf("%s defaults: %v", filepath.Base(roleDir), err)
	}
	return defaults
}

func yamlScalar(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case int:
		return strconv.Itoa(x)
	case nil:
		return ""
	default:
		return "?"
	}
}

// serviceSection returns the [Service] section of a unit file, so a
// directive in [Unit] or [Install] cannot satisfy an assertion.
func serviceSection(unit string) string {
	_, rest, ok := strings.Cut(unit, "[Service]")
	if !ok {
		return ""
	}
	if i := strings.Index(rest, "\n["); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

// byteSize parses the systemd (K/M/G, base 1024) and Go (KiB/MiB/GiB) sizes
// the roles use.
func byteSize(t *testing.T, s string) int64 {
	t.Helper()
	m := regexp.MustCompile(`^([0-9]+)(K|M|G|KiB|MiB|GiB)?$`).FindStringSubmatch(s)
	if m == nil {
		t.Fatalf("unparseable size %q", s)
	}
	n, _ := strconv.ParseInt(m[1], 10, 64)
	switch strings.TrimSuffix(m[2], "iB") {
	case "K":
		n <<= 10
	case "M":
		n <<= 20
	case "G":
		n <<= 30
	}
	return n
}
