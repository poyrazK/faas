package daemonunitspec

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A control plane rebuilt from scratch had no public TLS edge: the old one
// was hand-built, so nothing listened on 443 and Cloudflare returned 521 for
// api.gregale.dev. public_edge must front gatewayd-public's socket, run in the
// control-plane play, and hand gatewayd-public exactly one client hop (it
// rejects any other X-Forwarded-For shape as a forged caller IP).
func TestPublicEdgeFrontsGatewaydPublicBehindCloudflare(t *testing.T) {
	root := repoRoot(t)
	defaults := map[string]any{}
	body, err := os.ReadFile(filepath.Join(root, "deploy", "ansible", "roles", "public_edge", "defaults", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(body, &defaults); err != nil {
		t.Fatal(err)
	}
	socket, err := os.ReadFile(filepath.Join(root, "deploy", "ansible", "roles", "gatewayd_public_service", "files", "faas-gatewayd-public.socket"))
	if err != nil {
		t.Fatal(err)
	}
	listen := regexp.MustCompile(`(?m)^ListenStream=(\S+)$`).FindStringSubmatch(string(socket))
	if listen == nil || defaults["faas_public_edge_upstream"] != listen[1] {
		t.Fatalf("public edge upstream %v does not match gatewayd-public's socket %v", defaults["faas_public_edge_upstream"], listen)
	}
	if enabled, _ := defaults["faas_public_edge_enabled"].(string); !strings.Contains(enabled, "faas_cloudflare_origin_only") {
		t.Errorf("public edge is not keyed off the manifest's Cloudflare origin mode: %q", enabled)
	}

	playbook, err := os.ReadFile(filepath.Join(root, "deploy", "ansible", "bootstrap.yml"))
	if err != nil {
		t.Fatal(err)
	}
	order := []string{"    - role: gatewayd_public_service\n", "    - role: public_edge\n", "    - role: s3_gateway_service\n"}
	last := -1
	for _, role := range order {
		i := strings.Index(string(playbook), role)
		if i < 0 || i < last {
			t.Fatalf("control-plane play must run %v in that order", order)
		}
		last = i
	}

	tasks := loadYAMLList(t, filepath.Join(root, "deploy", "ansible", "roles", "public_edge", "tasks", "main.yml"))
	var block string
	for _, task := range flattenRoleTasks("", tasks) {
		if args, ok := task.body["ansible.builtin.blockinfile"].(map[string]any); ok {
			block, _ = args["block"].(string)
		}
	}
	if block == "" {
		t.Fatal("public_edge lost its managed Caddy block")
	}

	vars := map[string]any{}
	for k, v := range defaults {
		if s, ok := v.(string); ok && !strings.Contains(s, "{{") {
			vars[k] = v
		}
	}
	vars["faas_public_edge_domain"] = "gregale.dev"
	text := renderPublicEdgeBlock(t, block, vars)
	for _, want := range []string{
		"gregale.dev, *.gregale.dev {",
		"tls /etc/caddy/tls/cloudflare-origin.pem /etc/caddy/tls/cloudflare-origin.key",
		"reverse_proxy " + listen[1] + " {",
		"header_up X-Forwarded-For {http.request.header.CF-Connecting-IP}",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("rendered edge block is missing %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{"on_demand", "https:// {", "abort"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("Cloudflare-only edge renders customer-domain config %q:\n%s", unwanted, text)
		}
	}

	// ADR-520: customer domains open 443 to everyone. The platform site must
	// still answer Cloudflare only, customer hosts must get certificates
	// only through gatewayd-public's ask endpoint, and a client-supplied
	// CF-Connecting-IP must never become the caller IP on customer hosts.
	vars["faas_custom_domain_tls"] = true
	vars["faas_custom_domain_acme_email"] = "ops@gregale.dev"
	vars["faas_public_edge_cloudflare_cidrs"] = []string{"173.245.48.0/20", "2400:cb00::/32"}
	text = renderPublicEdgeBlock(t, block, vars)
	for _, want := range []string{
		"email ops@gregale.dev",
		"ask " + defaults["faas_public_edge_ask_url"].(string),
		"@direct not remote_ip 173.245.48.0/20 2400:cb00::/32",
		"abort @direct",
		"https:// {",
		"on_demand",
		"header_up X-Forwarded-For {remote_host}",
		"redir https://{host}{uri} 308",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("customer-domain edge block is missing %q:\n%s", want, text)
		}
	}
	if !strings.HasPrefix(strings.TrimSpace(text), "{") {
		t.Errorf("Caddy global options must open the Caddyfile block:\n%s", text)
	}
	if askURL, _ := defaults["faas_public_edge_ask_url"].(string); !strings.HasSuffix(askURL, "/v1/internal/tls/ask") {
		t.Errorf("ask URL %q does not point at gatewayd-public's on-demand TLS endpoint", askURL)
	}
	customHost := text[strings.Index(text, "https:// {"):]
	if strings.Contains(customHost, "CF-Connecting-IP") {
		t.Errorf("customer-domain site trusts CF-Connecting-IP:\n%s", customHost)
	}
}

// renderPublicEdgeBlock renders the managed Caddy block with ansible's own
// templating so Jinja behaviour (trim_blocks, filters) matches production.
func renderPublicEdgeBlock(t *testing.T, block string, vars map[string]any) string {
	t.Helper()
	playbookBin, err := exec.LookPath("ansible-playbook")
	if err != nil {
		t.Skip("ansible-playbook not installed; render check skipped")
	}
	dir := t.TempDir()
	tmpl := filepath.Join(dir, "block.j2")
	if err := os.WriteFile(tmpl, []byte(block), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "block")
	play := []map[string]any{{
		"hosts": "localhost", "gather_facts": false, "vars": vars,
		"tasks": []map[string]any{{"ansible.builtin.template": map[string]any{"src": tmpl, "dest": out}}},
	}}
	pb, err := yaml.Marshal(play)
	if err != nil {
		t.Fatal(err)
	}
	pbPath := filepath.Join(dir, "play.yml")
	if err := os.WriteFile(pbPath, pb, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "ansible.cfg")
	if err := os.WriteFile(cfg, []byte("[defaults]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(playbookBin, "-i", "localhost,", "-c", "local", pbPath)
	cmd.Env = append(os.Environ(), "ANSIBLE_CONFIG="+cfg, "ANSIBLE_NOCOLOR=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ansible-playbook: %v\n%s", err, output)
	}
	rendered, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(rendered)
}
