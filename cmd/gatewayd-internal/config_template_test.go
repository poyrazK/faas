package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

var (
	jinjaComment   = regexp.MustCompile(`\{#.*?#\}`)
	jinjaStatement = regexp.MustCompile(`^\s*\{%.*%\}\s*$`)
	jinjaBoolExpr  = regexp.MustCompile(`\{\{[^}]*\|\s*lower\s*\}\}`)
	jinjaDefault   = regexp.MustCompile(`\{\{[^}]*default\('([^']*)'\)[^}]*\}\}`)
	jinjaExpr      = regexp.MustCompile(`\{\{[^}]*\}\}`)
)

// renderGatewaydTemplate is a minimal stand-in for Ansible: it keeps every
// conditional block (so every optional key is checked), renders boolean
// filters as true, uses a default('...') value where one is given, and
// fills every other expression with "rendered".
func renderGatewaydTemplate(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "deploy", "ansible", "roles", "gatewayd_internal_service", "templates", "gatewayd.toml.j2")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	var out []string
	for _, line := range strings.Split(string(body), "\n") {
		line = jinjaComment.ReplaceAllString(line, "")
		if jinjaStatement.MatchString(line) {
			continue
		}
		line = jinjaBoolExpr.ReplaceAllString(line, "true")
		line = jinjaDefault.ReplaceAllString(line, "$1")
		out = append(out, jinjaExpr.ReplaceAllString(line, "rendered"))
	}
	return strings.Join(out, "\n")
}

// The [ratelimit] header once sat above apid_loopback, so TOML scoped
// apid_loopback and every schedd/vmmd/egress/app_errors mTLS path into the
// ratelimit table. LoadConfig ignores unknown keys, so a host rendered from
// the template silently fell back to defaults for all of them.
func TestGatewaydTemplateHasNoUndecodedKeys(t *testing.T) {
	var cfg Config
	md, err := toml.Decode(renderGatewaydTemplate(t), &cfg)
	if err != nil {
		t.Fatalf("decode rendered template: %v", err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		t.Fatalf("rendered template has keys the daemon ignores (a key below a [table] header?): %v", undecoded)
	}
	if cfg.APIDLoopback != "http://127.0.0.1:8081" || cfg.ScheddTLSCertPath != "rendered" || cfg.VMMDPingTLSCertPath != "rendered" {
		t.Fatalf("top-level keys not decoded: apid_loopback=%q schedd_tls_cert_path=%q vmmd_tls_cert_path=%q",
			cfg.APIDLoopback, cfg.ScheddTLSCertPath, cfg.VMMDPingTLSCertPath)
	}
	if cfg.RateLimit.Mode != "central" {
		t.Fatalf("ratelimit.mode = %q, want the template default central", cfg.RateLimit.Mode)
	}
}
