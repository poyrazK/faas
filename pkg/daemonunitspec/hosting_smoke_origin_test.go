package daemonunitspec

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// imaged's post-readiness smoke must connect to a host that resolves. Tenant
// hosts are <slug>.<apps domain>, so a label under the apps domain resolves
// wherever tenants are served; the bare apex need not. The template rendered
// https://gregale.app after the apps-domain move, gregale.app has no apex
// record, and every candidate verification failed during the rc.367 rollout
// (2026-11-22).
func TestHostingSmokeOriginIsALabelUnderTheAppsDomain(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "roles", "compute_only_service", "templates", "zz-faas-api-hosting-smoke.conf.j2"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^Environment=FAAS_API_HOSTING_SMOKE_URL=https://(.*)$`).FindSubmatch(raw)
	if m == nil {
		t.Fatal("template does not set FAAS_API_HOSTING_SMOKE_URL to an https origin")
	}
	if !regexp.MustCompile(`^[a-z0-9-]+\.\{\{ gatewayd_apps_domain `).Match(m[1]) {
		t.Fatalf("smoke origin host %q must be a label under gatewayd_apps_domain, not the bare apex", m[1])
	}
}
