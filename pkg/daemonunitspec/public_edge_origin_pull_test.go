package daemonunitspec

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// ADR-20261103172551: Cloudflare's address ranges carry every Cloudflare
// customer's traffic, so a platform site that admits them must also require
// the zone's own origin-pull client certificate, and must never trust
// Cloudflare's shared origin-pull CA. The role refuses to deploy otherwise
// unless the operator opts out by name.
func TestPublicEdgeRequiresZoneOriginPullsWhereCloudflareIsAdmitted(t *testing.T) {
	root := repoRoot(t)
	defaults := map[string]any{}
	body, err := os.ReadFile(filepath.Join(root, "deploy", "ansible", "roles", "public_edge", "defaults", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(body, &defaults); err != nil {
		t.Fatal(err)
	}
	if v, ok := defaults["faas_public_edge_allow_any_cloudflare_zone"].(bool); !ok || v {
		t.Fatalf("faas_public_edge_allow_any_cloudflare_zone default = %v, want false", defaults["faas_public_edge_allow_any_cloudflare_zone"])
	}

	tasks := flattenRoleTasks("", loadYAMLList(t, filepath.Join(root, "deploy", "ansible", "roles", "public_edge", "tasks", "main.yml")))
	byName := map[string]map[string]any{}
	order := map[string]int{}
	for i, task := range tasks {
		if name, ok := task.body["name"].(string); ok {
			byName[name] = task.body
			order[name] = i
		}
	}
	guard := byName["public_edge — require origin-pull certificates where Cloudflare is admitted"]
	if guard == nil {
		t.Fatal("public_edge lost the origin-pull guard")
	}
	assert, _ := guard["ansible.builtin.assert"].(map[string]any)
	if got := fmt.Sprint(assert["that"]); !strings.Contains(got, "faas_public_edge_authenticated_origin_pulls") {
		t.Errorf("guard asserts %s, want authenticated origin pulls", got)
	}
	when := fmt.Sprint(guard["when"])
	for _, want := range []string{"faas_public_edge_cloudflare_only", "faas_custom_domain_tls", "not (faas_public_edge_allow_any_cloudflare_zone | bool)", "faas_public_edge_enabled"} {
		if !strings.Contains(when, want) {
			t.Errorf("guard condition %s is missing %q", when, want)
		}
	}

	shared := byName["public_edge — refuse Cloudflare's shared origin-pull CA"]
	if shared == nil || !strings.Contains(fmt.Sprint(shared["ansible.builtin.assert"]), "origin-pull.cloudflare.net") {
		t.Fatalf("public_edge does not refuse Cloudflare's shared origin-pull CA: %v", shared)
	}
	read, ok := order["public_edge — read the origin-pull CA's subject and issuer"]
	if !ok || read > order["public_edge — refuse Cloudflare's shared origin-pull CA"] {
		t.Error("the CA's names must be read before the shared-CA check")
	}
	blockTask := -1
	for i, task := range tasks {
		if args, ok := task.body["ansible.builtin.blockinfile"].(map[string]any); ok && args["marker"] == "# {mark} ANSIBLE MANAGED Gregale public edge" {
			blockTask = i
		}
	}
	if blockTask < 0 || order["public_edge — require origin-pull certificates where Cloudflare is admitted"] > blockTask {
		t.Error("the origin-pull guard must run before the Caddy block is written")
	}
}
