package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestAnsibleToolchainPinnedIdenticallyAcrossCIAndCD keeps every path that
// runs deploy/ansible on the toolchain CI lints and tests. cd-compute used
// to run compute rollouts with the fleet runner's apt ansible-core 2.16 and
// collections floored at ">=", while CI tested 2.21.2 with the newest
// collections.
func TestAnsibleToolchainPinnedIdenticallyAcrossCIAndCD(t *testing.T) {
	corePin := regexp.MustCompile(`'ansible-core==([0-9][^']*)'`)
	pins := map[string]string{}
	for _, name := range []string{"ci.yml", "cd-controlplane.yml", "cd-compute.yml"} {
		matches := corePin.FindAllStringSubmatch(readWorkflow(t, name), -1)
		if len(matches) == 0 {
			t.Errorf("%s installs no pinned ansible-core", name)
			continue
		}
		for _, m := range matches {
			pins[name+" "+m[1]] = m[1]
		}
	}
	versions := map[string]bool{}
	for _, v := range pins {
		versions[v] = true
	}
	if len(versions) > 1 {
		t.Errorf("ansible-core pins differ across workflows: %v", pins)
	}

	body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "requirements.yml"))
	if err != nil {
		t.Fatal(err)
	}
	exact := regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	collections := map[string]string{}
	var current string
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		if name, ok := strings.CutPrefix(line, "- name:"); ok {
			current = strings.TrimSpace(name)
		}
		if version, ok := strings.CutPrefix(line, "version:"); ok {
			version = strings.Trim(strings.TrimSpace(version), `"'`)
			if !exact.MatchString(version) {
				t.Errorf("requirements.yml pins %s to %q; want an exact x.y.z version", current, version)
			}
			collections[current] = version
		}
	}
	if len(collections) == 0 {
		t.Fatal("requirements.yml declares no collections")
	}

	ci := readWorkflow(t, "ci.yml")
	for name, version := range collections {
		clone := "--branch " + version + " --single-branch \\\n              https://github.com/ansible-collections/" + name + ".git"
		if !strings.Contains(ci, clone) {
			t.Errorf("ci.yml's Galaxy fallback does not clone %s at the pinned %s", name, version)
		}
	}

	compute := readWorkflow(t, "cd-compute.yml")
	install := strings.Index(compute, "- name: Install pinned Ansible toolchain")
	prereqs := strings.Index(compute, "- name: Check fleet runner prerequisites")
	if install < 0 || prereqs < 0 || install > prereqs {
		t.Fatal("cd-compute must install the pinned Ansible toolchain before checking runner prerequisites")
	}
	step := compute[install:prereqs]
	for _, required := range []string{
		"-r deploy/ansible/requirements.yml",
		`echo "ANSIBLE_COLLECTIONS_PATH=$collections" >> "$GITHUB_ENV"`,
		`echo "$RUNNER_TEMP/ansible-venv/bin" >> "$GITHUB_PATH"`,
	} {
		if !strings.Contains(step, required) {
			t.Errorf("cd-compute toolchain step is missing %q", required)
		}
	}
}
