package daemonunitspec

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestLokiStartsInsideItsSandbox pins what the 2026-10-24 smoke on a real
// host found (ADR-20261023124955 §7): with AF_NETLINK outside the unit's
// RestrictAddressFamilies, Loki cannot list interfaces for a ring address and
// exits at startup unless the config names one.
func TestLokiStartsInsideItsSandbox(t *testing.T) {
	roles := filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "loki")
	read := func(rel string) string {
		body, err := os.ReadFile(filepath.Join(roles, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	unit := read("templates/loki.service.j2")
	families := regexp.MustCompile(`(?m)^RestrictAddressFamilies=(.*)$`).FindStringSubmatch(unit)
	if families == nil || strings.Contains(families[1], "AF_NETLINK") {
		return
	}
	config := read("templates/config.yml.j2")
	common := config[strings.Index(config, "\ncommon:"):]
	if end := regexp.MustCompile(`\n\S`).FindStringIndex(common[1:]); end != nil {
		common = common[:end[0]+1]
	}
	if !regexp.MustCompile(`(?m)^  instance_addr: \S`).MatchString(common) {
		t.Fatalf("loki.service.j2 restricts address families to %q, so config.yml.j2 must set common.instance_addr", families[1])
	}
}

// TestLokiPortsAreFree pins that Loki's listeners are not a port another
// control-plane daemon holds. Loki's gRPC port was 9096, which is
// s3-gatewayd's control listener on production's control plane (found
// 2026-10-25), so enabling logs would have crash-looped Loki.
func TestLokiPortsAreFree(t *testing.T) {
	roles := filepath.Join(repoRoot(t), "deploy", "ansible", "roles")
	read := func(rel string) string {
		body, err := os.ReadFile(filepath.Join(roles, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	taken := map[string]string{}
	// Every daemon Prometheus scrapes on the control plane listens on its target port.
	prom := read("prometheus/defaults/main.yml")
	targets := regexp.MustCompile(`(?m)^  - job: (\S+)\n(?:    .*\n)*?    port: (\d+)`).FindAllStringSubmatch(prom, -1)
	if len(targets) < 10 {
		t.Fatalf("parsed %d prom_targets, want the full list", len(targets))
	}
	for _, m := range targets {
		taken[m[2]] = "prom_targets job " + m[1]
	}
	// Loopback listeners set in shipped unit files.
	units, err := filepath.Glob(filepath.Join(roles, "*", "files", "*.service"))
	if err != nil {
		t.Fatal(err)
	}
	for _, unit := range units {
		body, err := os.ReadFile(unit)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range regexp.MustCompile(`127\.0\.0\.1:(\d+)`).FindAllStringSubmatch(string(body), -1) {
			taken[m[1]] = filepath.Base(unit)
		}
	}
	defaults := read("loki/defaults/main.yml")
	for _, key := range []string{"loki_http_listen_port", "loki_grpc_listen_port"} {
		m := regexp.MustCompile(`(?m)^` + key + `: (\d+)$`).FindStringSubmatch(defaults)
		if m == nil {
			t.Fatalf("loki defaults lack %s", key)
		}
		if owner, ok := taken[m[1]]; ok {
			t.Errorf("%s=%s is already held by %s", key, m[1], owner)
		}
	}
}
