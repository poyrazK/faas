package daemonunitspec

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// dropInUnits returns, per drop-in file name, the units a role's template
// tasks install it for (from the dest path, expanding a {{ item }} loop).
func dropInUnits(t *testing.T, tasksPath string, names ...string) map[string][]string {
	t.Helper()
	raw, err := os.ReadFile(tasksPath)
	if err != nil {
		t.Fatal(err)
	}
	var tasks []struct {
		Template struct {
			Dest string `yaml:"dest"`
		} `yaml:"ansible.builtin.template"`
		// A loop may be a list or a templated expression; only lists name units.
		Loop yaml.Node `yaml:"loop"`
	}
	if err := yaml.Unmarshal(raw, &tasks); err != nil {
		t.Fatal(err)
	}
	units := map[string][]string{}
	for _, task := range tasks {
		dest := task.Template.Dest
		for _, name := range names {
			if !strings.HasSuffix(dest, "/"+name) {
				continue
			}
			targets := []string{dest}
			if strings.Contains(dest, "{{ item }}") {
				targets = nil
				var items []string
				if task.Loop.Kind == yaml.SequenceNode {
					if err := task.Loop.Decode(&items); err != nil {
						t.Fatal(err)
					}
				}
				for _, item := range items {
					targets = append(targets, strings.ReplaceAll(dest, "{{ item }}", item))
				}
			}
			for _, target := range targets {
				unit := strings.TrimSuffix(filepath.Base(filepath.Dir(target)), ".service.d")
				units[name] = append(units[name], unit)
			}
		}
	}
	return units
}

// A daemon given the domain layout (aliases, platform and deploy domains)
// must also be given the apps domain from the same DNS configuration. apid's
// apps domain otherwise comes from its hand-written apid.toml, which still
// named gregale.dev after the apps-domain move: the layout then listed
// gregale.dev as both apps domain and alias, and apid refused to start
// during the rc.364 converge (2026-11-21).
func TestControlPlaneDomainLayoutDaemonsGetTheAppsDomain(t *testing.T) {
	const layout, apps = "99-faas-domain-layout.conf", "99-faas-apps-domain.conf"
	units := dropInUnits(t, filepath.Join("..", "..", "deploy", "ansible", "roles", "control_plane_service", "tasks", "main.yml"), layout, apps)
	if !slices.Contains(units[layout], "faas-apid") {
		t.Fatalf("expected the control plane to give apid the domain layout; got %v", units[layout])
	}
	for _, unit := range units[layout] {
		if !slices.Contains(units[apps], unit) {
			t.Errorf("%s gets %s but not %s (apps-domain units: %v)", unit, layout, apps, units[apps])
		}
	}
}
