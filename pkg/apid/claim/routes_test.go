package claim_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/claim"
)

func TestRefusedField(t *testing.T) {
	const patch = "PATCH /v1/apps/{slug}"
	for _, c := range []struct{ body, want string }{
		{`{"require_authn":false}`, ""},
		{`{"public_auth":{"mode":"open"},"resource_profile":"small"}`, ""},
		{`{"require_authn":false,"egress_allowlist":["0.0.0.0/0"]}`, "egress_allowlist"},
		{`{"min_instances":3}`, "min_instances"},
		{`{"service_binding_targets":["other"]}`, "service_binding_targets"},
		{`[]`, "body"},
		{`not json`, "body"},
	} {
		if got := claim.RefusedField(patch, []byte(c.body)); got != c.want {
			t.Errorf("RefusedField(%s) = %q, want %q", c.body, got, c.want)
		}
	}
	const create = "POST /v1/apps"
	if got := claim.RefusedField(create, []byte(`{"slug":"x","type":"function","runtime":"node22","require_authn":false,"resource_profile":"small"}`)); got != "" {
		t.Errorf("create with deploy settings = %q, want allowed", got)
	}
	for _, field := range []string{"service_binding_targets", "allowed_service_callers", "warm_pool_size", "ports", "worker_replicas", "project_id"} {
		if got := claim.RefusedField(create, []byte(`{"slug":"x","`+field+`":null}`)); got != field {
			t.Errorf("create with %s = %q, want it refused", field, got)
		}
	}
	if got := claim.RefusedField("POST /v1/apps/{slug}/deployments", []byte(`{"anything":1}`)); got != "" {
		t.Errorf("ungated route = %q", got)
	}
}

func TestRouteAllowed(t *testing.T) {
	for _, c := range []struct {
		pattern string
		want    bool
	}{
		{"GET /v1/apps", true},
		{"GET /v1/deployments/{id}/logs", true},
		{"GET /v1/account", true},
		{"POST /v1/apps", true},
		{"POST /v1/apps/{slug}/deployments", true},
		{"POST /v1/uploads", true},
		{"PUT /v1/apps/{slug}/env/{key}", true},
		{"POST /v1/account/claim-link", true},
		// Writes not listed are refused, whatever their path says.
		{"POST /v1/apps/{slug}/ai-access-tokens", false},
		{"POST /v1/apps/{slug}/log-drains", false},
		{"POST /v1/projects/{slug}/environment-clones", false},
		{"POST /v1/apps/{slug}/realtime/endpoints/{id}/publish-credentials", false},
		{"PATCH /v1/apps/{slug}", true}, // field-gated
		{"POST /v1/projects/scan", false},
		{"GET /v1/apps/{slug}/entities/connect", false},
		{"GET /v1/apps/{slug}/entities/session-connect", false},
		{"GET /v1/apps/{slug}/forks/{id}/execs", false},
		{"GET /v1/apps/{slug}/deploy-tokens", false},
		{"GET /v1/apps/{slug}/ai-access-tokens", false},
		{"GET /v1/apps/{slug}/registry-credentials", false},
		{"GET /v1/apps/{slug}/consumers/{consumer_id}/keys", false},
		{"GET /v1/account/platform-tenants/{id}/access-tokens", false},
		{"GET /v1/apps/{slug}/secrets", true},
		{"POST /v1/projects", false},
		{"DELETE /v1/account", false},
		{"POST /v1/account/keys", false},
		{"POST /v1/apps/{slug}/domains", false},
		{"POST /v1/keys", false},
		{"GET /v1/keys", false},
		{"POST /v1/orgs", false},
		{"POST /v1/billing/checkout", false},
		{"", false},
		{"/v1/apps", false},
	} {
		if got := claim.RouteAllowed(c.pattern); got != c.want {
			t.Errorf("RouteAllowed(%q) = %v, want %v", c.pattern, got, c.want)
		}
	}
}

// Every writable route is one apid registers, so a renamed route cannot
// silently drop out of what an anonymous deploy needs.
func TestWriteRoutesAreRegistered(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "apidserver", "server.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range claim.WriteRoutes() {
		if !strings.Contains(string(src), `"`+p+`"`) {
			t.Errorf("writable route %q is not registered in apidserver/server.go", p)
		}
	}
}
