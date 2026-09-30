package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The fleet runner SSHes into the control plane on every rollout, and its
// ssh-keyscan offered security-key host key types the server rejects. Each
// run logged several "Unable to negotiate ... [preauth]" lines, and the
// control plane's aggressive sshd jail banned the runner: the production-us
// rollout died with "Connection closed by 10.10.0.4 port 22". The jail
// allowlisted the fleet CIDR on compute nodes but not on the control plane.
func TestFleetRunnerIsNotBannedByTheControlPlaneJail(t *testing.T) {
	keyscan := regexp.MustCompile(`(?m)^\s*(?:if !\s*)?ssh-keyscan\b.*$`)
	files, err := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range keyscan.FindAllString(string(body), -1) {
			if !strings.Contains(line, " -t ") {
				t.Errorf("%s: ssh-keyscan without pinned key types triggers fail2ban: %s", filepath.Base(f), strings.TrimSpace(line))
			}
		}
	}

	playbookBin, err := exec.LookPath("ansible-playbook")
	if err != nil {
		t.Skip("ansible-playbook not installed; template check skipped")
	}
	template, err := filepath.Abs(filepath.Join("..", "..", "deploy", "ansible", "roles", "host_hardening", "templates", "fail2ban-faas-sshd.local.j2"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		vars map[string]any
	}{
		// What `gregalectl manifest ansible` renders for each role.
		{"control plane", map[string]any{"overlay_cidrs": []string{}, "faas_compute_allowed_cidrs": []string{"10.10.0.0/20"}}},
		{"compute node", map[string]any{"overlay_cidrs": []string{"10.10.0.0/20"}, "faas_control_plane_allowed_cidrs": []string{"10.10.0.0/20"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			out := filepath.Join(dir, "jail.local")
			vars := map[string]any{
				"ansible_managed":                  "test",
				"faas_hardening_fail2ban_ignoreip": []string{"127.0.0.0/8", "::1"},
				"faas_hardening_fail2ban_bantime":  "1h",
				"faas_hardening_fail2ban_findtime": "10m",
				"faas_hardening_fail2ban_maxretry": 5,
			}
			for k, v := range tc.vars {
				vars[k] = v
			}
			play := []map[string]any{{
				"hosts": "localhost", "gather_facts": false, "vars": vars,
				"tasks": []map[string]any{{"ansible.builtin.template": map[string]any{"src": template, "dest": out}}},
			}}
			body, err := yaml.Marshal(play)
			if err != nil {
				t.Fatal(err)
			}
			pb := filepath.Join(dir, "play.yml")
			if err := os.WriteFile(pb, body, 0o600); err != nil {
				t.Fatal(err)
			}
			cfg := filepath.Join(dir, "ansible.cfg")
			if err := os.WriteFile(cfg, []byte("[defaults]\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(playbookBin, "-i", "localhost,", "-c", "local", pb)
			cmd.Env = append(os.Environ(), "ANSIBLE_CONFIG="+cfg, "ANSIBLE_NOCOLOR=1")
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("ansible-playbook: %v\n%s", err, output)
			}
			rendered, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			m := regexp.MustCompile(`(?m)^ignoreip = (.*)$`).FindStringSubmatch(string(rendered))
			if m == nil || !strings.Contains(" "+m[1]+" ", " 10.10.0.0/20 ") {
				t.Fatalf("%s jail does not allowlist the fleet CIDR: %q", tc.name, m)
			}
		})
	}
}
