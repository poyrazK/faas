package daemonunitspec

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// adr: 20261027090010 — agentd is an operator-configured add-on: the generated unit
// leaves it idle and carries no secret. The agentd_service role's drop-in
// turns agent runs on, and its database URL arrives in a per-daemon secret
// env file that systemd, not the faas user, reads.
func TestAgentdUnit(t *testing.T) {
	u := UnitAgentd()
	if u.User != "faas" || u.Group != "faas" || u.Slice != FaasCPSlice || u.Type != "simple" {
		t.Errorf("agentd runs as %s:%s (%s) in %s, want faas:faas simple in %s", u.User, u.Group, u.Type, u.Slice, FaasCPSlice)
	}
	if u.ExecStart != "/opt/faas/current/bin/agentd" {
		t.Errorf("ExecStart = %q", u.ExecStart)
	}
	if len(u.Environment) != 0 || len(u.LoadCredential) != 0 {
		t.Error("the generated unit must not switch agent runs on or name a secret; the role does")
	}
	files := strings.Fields(u.EnvironmentFile)
	if !slices.Equal(files, []string{"-/etc/faas/secrets/agentd/agentd.env", "-/etc/faas/storage.env", "-/etc/faas/otel.env"}) {
		t.Errorf("EnvironmentFile = %v: agentd reads its own secret file, never sealed.env", files)
	}
	if !slices.Contains(u.After, "faas-schedd.service") {
		t.Errorf("After = %v: agentd dials schedd", u.After)
	}
	for _, entry := range Registry {
		if entry.Name == "agentd" {
			t.Fatal("agentd must stay out of the always-on registry")
		}
	}
}

// adr: 20261027090010 — every switch the role's drop-in writes is one agentd reads
// and the env contract declares as drop-in delivered.
func TestAgentdDropInSwitches(t *testing.T) {
	role := filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "agentd_service")
	tmpl, err := os.ReadFile(filepath.Join(role, "templates", "50-faas-agentd.conf.j2"))
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(repoRoot(t), "cmd", "agentd", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string]EnvSource{}
	for _, v := range EnvContract {
		declared[v.Name] = v.Source
	}
	written := regexp.MustCompile(`(?m)^Environment=(FAAS_[A-Z0-9_]+)=`).FindAllStringSubmatch(string(tmpl), -1)
	if len(written) < 6 {
		t.Fatalf("the drop-in writes %d switches", len(written))
	}
	for _, m := range written {
		name := m[1]
		if !strings.Contains(string(src), `"`+name+`"`) {
			t.Errorf("the drop-in writes %s, which cmd/agentd does not read", name)
		}
		if declared[name] != EnvSourceDropin {
			t.Errorf("%s: env contract source %q, want %q", name, declared[name], EnvSourceDropin)
		}
	}
	raw, err := os.ReadFile(filepath.Join(role, "defaults", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]any{}
	if err := yaml.Unmarshal(raw, &vars); err != nil {
		t.Fatal(err)
	}
	vars["faas_agentd_s3_endpoint"] = "https://s3.example.test"
	vars["faas_agentd_checkouts"] = false
	text := renderPublicEdgeBlock(t, string(tmpl), vars)
	for _, want := range []string{"Environment=FAAS_AGENTD_AGENT_RUNS=1\n", "Environment=FAAS_AGENTD_CHECKOUTS=0\n",
		"Environment=FAAS_AGENTD_DELIVERIES=1\n", "Environment=FAAS_AGENTD_S3_ENDPOINT=https://s3.example.test\n",
		"Environment=FAAS_AGENTD_METRICS_ADDR=127.0.0.1:9109\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("rendered drop-in lacks %q:\n%s", want, text)
		}
	}
}
