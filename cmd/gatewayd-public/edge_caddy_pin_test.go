package main

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// minAbortPropagatingCaddy is the oldest Caddy verified to end a client
// response with an error when the upstream aborts mid-body. Caddy 2.6.2, the
// Ubuntu 24.04 package, sends a clean, truncated 200 instead, so a gatewayd
// stream abort reached clients as a complete-looking response (Hunt 14 H14-7).
var minAbortPropagatingCaddy = [3]int{2, 11, 7}

// TestPublicEdgeInstallsAnAbortPropagatingCaddy pins the public_edge role to
// the official Caddy package at a version that propagates upstream aborts.
func TestPublicEdgeInstallsAnAbortPropagatingCaddy(t *testing.T) {
	const role = "../../deploy/ansible/roles/public_edge"
	raw, err := os.ReadFile(role + "/defaults/main.yml")
	if err != nil {
		t.Fatalf("read public_edge defaults: %v", err)
	}
	var defaults struct {
		Version string `yaml:"faas_public_edge_caddy_version"`
	}
	if err := yaml.Unmarshal(raw, &defaults); err != nil {
		t.Fatalf("parse public_edge defaults: %v", err)
	}
	got, ok := parseCaddyVersion(defaults.Version)
	if !ok {
		t.Fatalf("faas_public_edge_caddy_version = %q, want an exact x.y.z version", defaults.Version)
	}
	if compareCaddyVersions(got, minAbortPropagatingCaddy) < 0 {
		t.Fatalf("faas_public_edge_caddy_version = %q, older than %d.%d.%d, which turns upstream aborts into clean truncated responses",
			defaults.Version, minAbortPropagatingCaddy[0], minAbortPropagatingCaddy[1], minAbortPropagatingCaddy[2])
	}

	tasks, err := os.ReadFile(role + "/tasks/main.yml")
	if err != nil {
		t.Fatalf("read public_edge tasks: %v", err)
	}
	for _, want := range []string{
		`name: "caddy={{ faas_public_edge_caddy_version }}"`,
		"Pin: version {{ faas_public_edge_caddy_version }}",
		"Pin-Priority: 1001",
	} {
		if !strings.Contains(string(tasks), want) {
			t.Errorf("public_edge tasks do not contain %q; the distro Caddy could be installed instead of the pinned one", want)
		}
	}
}

func TestCompareCaddyVersions(t *testing.T) {
	for _, tc := range []struct {
		version string
		want    int
	}{
		{"2.6.2", -1},
		{"2.11.6", -1},
		{"2.11.7", 0},
		{"2.12.0", 1},
		{"3.0.0", 1},
	} {
		got, ok := parseCaddyVersion(tc.version)
		if !ok {
			t.Fatalf("parseCaddyVersion(%q) failed", tc.version)
		}
		if c := compareCaddyVersions(got, minAbortPropagatingCaddy); c != tc.want {
			t.Errorf("compare(%s, minimum) = %d, want %d", tc.version, c, tc.want)
		}
	}
	for _, bad := range []string{"", "2.11", "2.11.x", "latest", "2.11.7.1"} {
		if _, ok := parseCaddyVersion(bad); ok {
			t.Errorf("parseCaddyVersion(%q) accepted a non x.y.z version", bad)
		}
	}
}

func parseCaddyVersion(s string) ([3]int, bool) {
	var v [3]int
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func compareCaddyVersions(a, b [3]int) int {
	for i := range a {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}
