package daemonunitspec

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// daemonsYAML mirrors deploy/ansible/vars/daemons.yml.
type daemonsYAML struct {
	ControlPlaneUnits []string `yaml:"faas_control_plane_units"`
	ComputeOnlyUnits  []string `yaml:"faas_compute_only_units"`
	LegacyUnits       []string `yaml:"faas_legacy_units"`
	Probes            map[string]struct {
		Probe     string `yaml:"probe"`
		Target    string `yaml:"target"`
		ReadyzURL string `yaml:"readyz_url"`
	} `yaml:"faas_daemon_probes"`
}

// TestDaemonsYAML_LockstepWithRegistry pins deploy/ansible/vars/daemons.yml
// (consumed by role_convergence + fleet_verify) to the Registry: the
// per-role unit sets and the readiness probes must match exactly. Adding a
// daemon, moving it between roles, or changing its probe in Go without
// updating the YAML fails here (ADR-143).
func TestDaemonsYAML_LockstepWithRegistry(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "deploy", "ansible", "vars", "daemons.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var got daemonsYAML
	if err := yaml.Unmarshal(body, &got); err != nil {
		t.Fatalf("parse daemons.yml: %v", err)
	}

	units := func(role Role) []string {
		var out []string
		for _, n := range DaemonsForRole(role) {
			out = append(out, "faas-"+n+".service")
		}
		sort.Strings(out)
		return out
	}
	sortedCopy := func(in []string) []string {
		out := append([]string(nil), in...)
		sort.Strings(out)
		return out
	}
	if want := units(RoleControlPlane); !reflect.DeepEqual(sortedCopy(got.ControlPlaneUnits), want) {
		t.Errorf("faas_control_plane_units = %v, Registry says %v", got.ControlPlaneUnits, want)
	}
	if want := units(RoleComputeOnly); !reflect.DeepEqual(sortedCopy(got.ComputeOnlyUnits), want) {
		t.Errorf("faas_compute_only_units = %v, Registry says %v", got.ComputeOnlyUnits, want)
	}
	if len(got.LegacyUnits) == 0 {
		t.Errorf("faas_legacy_units must list the retired one-box units role_convergence masks")
	}

	for _, e := range Registry {
		unit := "faas-" + e.Name + ".service"
		p, ok := got.Probes[unit]
		if !ok {
			t.Errorf("faas_daemon_probes missing %s", unit)
			continue
		}
		if p.Probe != string(e.Lifecycle.Probe) || p.Target != e.Lifecycle.ProbeTarget || p.ReadyzURL != e.Lifecycle.ReadyzURL {
			t.Errorf("%s probe = %s %q readyz=%q, Registry says %s %q readyz=%q", unit, p.Probe, p.Target, p.ReadyzURL, e.Lifecycle.Probe, e.Lifecycle.ProbeTarget, e.Lifecycle.ReadyzURL)
		}
		if e.Role != RoleControlPlane && e.Role != RoleComputeOnly {
			t.Errorf("%s: Registry entry has no Role", e.Name)
		}
	}
	for unit := range got.Probes {
		found := false
		for _, e := range Registry {
			if unit == "faas-"+e.Name+".service" {
				found = true
			}
		}
		if !found {
			t.Errorf("faas_daemon_probes lists %s which is not a Registry daemon", unit)
		}
	}
}

// TestComputeOnlyScheddUsesRemoteDatabaseUnit protects the split-box boot
// contract. The control-plane schedd unit is intentionally different: it may
// order after the local PostgreSQL service, while a compute node has no local
// PostgreSQL and must start from compute-db.env instead.
func TestComputeOnlyScheddUsesRemoteDatabaseUnit(t *testing.T) {
	root := repoRoot(t)
	unitPath := filepath.Join(root, "deploy", "ansible", "roles", "compute_only_service", "files", "faas-schedd.service")
	unit, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatal(err)
	}
	unitText := string(unit)
	if strings.Contains(unitText, "postgresql.service") {
		t.Fatalf("compute-only schedd must not depend on local PostgreSQL: %s", unitPath)
	}
	if !strings.Contains(unitText, "EnvironmentFile=-/etc/faas/compute-db.env") {
		t.Fatalf("compute-only schedd must load the remote database environment")
	}

	tasksPath := filepath.Join(root, "deploy", "ansible", "roles", "compute_only_service", "tasks", "main.yml")
	tasks, err := os.ReadFile(tasksPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(tasks), "src: faas-schedd.service") {
		t.Fatalf("compute-only role must install its local remote-DB schedd unit")
	}
	if !strings.Contains(string(tasks), "stop inherited local PostgreSQL services") ||
		!strings.Contains(string(tasks), "mask inherited PostgreSQL units") ||
		!strings.Contains(string(tasks), "/var/lib/postgresql.disabled") {
		t.Fatalf("compute-only role must stop, mask, and archive inherited local PostgreSQL before daemon activation")
	}
}

func TestComputeBootstrapAcceptsEmptyPostgresAndRequiresFastOCICache(t *testing.T) {
	root := repoRoot(t)
	joinPath := filepath.Join(root, "deploy", "ansible", "node_join.yml")
	body, err := os.ReadFile(joinPath)
	if err != nil {
		t.Fatal(err)
	}
	join := string(body)
	for _, want := range []string{
		`awk '{print $1}' || true`,
		"mountpoint -q /var/lib/faas/cache",
		`stat -c %d /var/lib/faas/cache`,
		`findmnt -n -o FSTYPE --mountpoint /var/lib/faas/cache`,
		"xfs_info /srv/fc",
		"reflink=1",
		"Refuse activation when the OCI cache is outside fast storage",
	} {
		if !strings.Contains(join, want) {
			t.Errorf("node_join compute contract is missing %q", want)
		}
	}

	storagePath := filepath.Join(root, "deploy", "ansible", "roles", "storage", "tasks", "main.yml")
	storage, err := os.ReadFile(storagePath)
	if err != nil {
		t.Fatal(err)
	}
	storageText := string(storage)
	for _, want := range []string{
		"stop compute cache writers before migration",
		"rsync",
		"bind,x-systemd.requires-mounts-for={{ faas_storage_mount }}/cache",
		`stat -c %d "{{ faas_storage_cache_mount }}"`,
	} {
		if !strings.Contains(storageText, want) {
			t.Errorf("storage role is missing fast-cache migration contract %q", want)
		}
	}
}

func TestComputePostgresDiscoveryHandlesEmptyAndPopulatedUnitSets(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "deploy", "ansible", "node_join.yml"))
	if err != nil {
		t.Fatal(err)
	}
	shell := ansibleTaskShell(t, body, "Stop inherited local PostgreSQL services before compute adoption")

	for _, tc := range []struct {
		name string
		mode string
		want []string
	}{
		{name: "empty", mode: "empty"},
		{name: "populated", mode: "populated", want: []string{"postgresql.service", "postgresql@.service"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binDir := t.TempDir()
			logPath := filepath.Join(t.TempDir(), "systemctl.log")
			fake := `#!/bin/sh
case "$1" in
  list-unit-files)
    if [ "$POSTGRES_TEST_MODE" = empty ]; then exit 1; fi
    printf 'postgresql.service enabled\npostgresql@.service disabled\n'
    ;;
  disable)
    printf '%s\n' "$3" >> "$SYSTEMCTL_LOG"
    ;;
  *) exit 64 ;;
esac
`
			if err := os.WriteFile(filepath.Join(binDir, "systemctl"), []byte(fake), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", "-c", shell)
			cmd.Env = append(os.Environ(),
				"PATH="+binDir+":"+os.Getenv("PATH"),
				"POSTGRES_TEST_MODE="+tc.mode,
				"SYSTEMCTL_LOG="+logPath,
			)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("discovery shell failed: %v\n%s", err, out)
			}
			got, err := os.ReadFile(logPath)
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if fields := strings.Fields(string(got)); !slices.Equal(fields, tc.want) {
				t.Fatalf("disabled units = %v, want %v", fields, tc.want)
			}
		})
	}
}

func ansibleTaskShell(t *testing.T, body []byte, taskName string) string {
	t.Helper()
	decoder := yaml.NewDecoder(strings.NewReader(string(body)))
	for {
		var doc yaml.Node
		if err := decoder.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatal(err)
		}
		if shell := findAnsibleTaskShell(&doc, taskName); shell != "" {
			return shell
		}
	}
	t.Fatalf("Ansible task %q has no shell body", taskName)
	return ""
}

func findAnsibleTaskShell(node *yaml.Node, taskName string) string {
	if node.Kind == yaml.MappingNode {
		name := ""
		shell := ""
		for i := 0; i+1 < len(node.Content); i += 2 {
			switch node.Content[i].Value {
			case "name":
				name = node.Content[i+1].Value
			case "ansible.builtin.shell":
				shell = node.Content[i+1].Value
			}
		}
		if name == taskName {
			return shell
		}
	}
	for _, child := range node.Content {
		if shell := findAnsibleTaskShell(child, taskName); shell != "" {
			return shell
		}
	}
	return ""
}

func TestHostHardeningAcceptsOnlyProvenLocalOrGCPOSLoginKeys(t *testing.T) {
	root := repoRoot(t)
	tasksPath := filepath.Join(root, "deploy", "ansible", "roles", "host_hardening", "tasks", "main.yml")
	body, err := os.ReadFile(tasksPath)
	if err != nil {
		t.Fatal(err)
	}
	tasks := string(body)
	for _, want := range []string{
		"/usr/bin/google_authorized_keys",
		`keys="$($command "$user")"`,
		"faas_hardening_oslogin_key.rc == 0",
		"faas_hardening_authkeys.stat.size > 0",
	} {
		if !strings.Contains(tasks, want) {
			t.Errorf("host hardening lockout guard is missing %q", want)
		}
	}
}
