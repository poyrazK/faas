package journeys

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Row 19 (projects), declared service access (ADR-20261124090000): a Compose
// service that declares `x-gregale-access: public` answers on its public URL
// without a Gregale API key, a sibling that declares nothing keeps the plan
// default, and removing the declaration closes the URL again on the next
// deploy. On a plan without a key gate (Free) the gate checks skip.

// accessCompose runs the shop's web code as two services. site declares
// public access unless public is false; admin never declares any.
func accessCompose(site, admin string, public bool) string {
	access := ""
	if public {
		access = "    x-gregale-access: public\n"
	}
	return fmt.Sprintf(`services:
  %[1]s:
    build: ./web
    ports: ["8080:8080"]
%[3]s  %[2]s:
    build: ./web
    ports: ["8080:8080"]
`, site, admin, access)
}

// TestDeclaredServiceAccess checks:
//   - the deploy plan says which service becomes public;
//   - the declared service answers anonymously after the deploy;
//   - the undeclared sibling still needs an API key;
//   - a redeploy without the declaration needs a key on the first service again.
func TestDeclaredServiceAccess(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	site, admin := tg.app(t, "site"), tg.app(t, "admin")
	project := tg.slug("access")
	t.Cleanup(func() {
		if tg.keep && t.Failed() {
			return
		}
		res := tg.run(t, 2*time.Minute, "project", "delete", project, "--yes")
		if res.exit != 0 && !strings.Contains(res.problem(), "not_found") {
			t.Logf("warning: cleanup could not delete project %s (%s)", project, res.problem())
		}
	})
	source := func(t *testing.T, public bool) string {
		t.Helper()
		dir := shopServices(t)
		if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(accessCompose(site, admin, public)), 0o644); err != nil {
			t.Fatalf("write compose.yaml: %v", err)
		}
		return dir
	}
	deploy := func(t *testing.T, public bool) {
		t.Helper()
		tg.deploySlots <- struct{}{}
		defer func() { <-tg.deploySlots }()
		res := tg.exec(t, tg.token, t.TempDir(), deployLimit+5*time.Minute, "deploy", "--project",
			"--project-slug", project, "--path", source(t, public),
			"--wait", "--timeout", deployLimit.String(), "--yes")
		if res.exit != 0 {
			t.Fatalf("project deploy failed (exit %d): %s\noutput: %s", res.exit, tg.redact(res.problem()),
				tg.redact(tail(string(res.stdout), 2000)))
		}
		t.Logf("metric service_access_deploy_s=%d", int(res.elapsed.Seconds()))
	}
	keyGate := func(t *testing.T) {
		t.Helper()
		app := object(t, tg.ok(t, time.Minute, "app", "get", admin).stdout)
		if field(app, "require_authn") != true {
			t.Skip("this plan has no API-key gate on public URLs (Free)")
		}
	}
	s := newStory(t)

	s.step("plan_names_public_service", func(t *testing.T) {
		res := tg.exec(t, tg.token, t.TempDir(), 2*time.Minute, "deploy", "--project",
			"--project-slug", project, "--path", source(t, true), "--dry-run", "--json")
		if res.exit != 0 {
			t.Fatalf("dry run failed (exit %d): %s", res.exit, tg.redact(res.problem()))
		}
		want := fmt.Sprintf("access: %s becomes public", site)
		if out := string(res.stdout) + string(res.stderr); !strings.Contains(out, want) {
			t.Fatalf("plan does not say %q:\n%s", want, tg.redact(tail(out, 2000)))
		}
	})
	s.step("deploy_declared", func(t *testing.T) { deploy(t, true) })
	s.check("declared_service_is_public", func(t *testing.T) {
		eventually(t, "site answers without an API key", 2*time.Minute, 3*time.Second, func() (bool, string) {
			r := get(tg.appURL(site) + "/")
			return r.status == http.StatusOK && str(jsonBody(t, r), "service") == "web", r.String()
		})
	})
	s.check("undeclared_service_needs_key", func(t *testing.T) {
		keyGate(t)
		if r := get(tg.appURL(admin) + "/"); r.status != http.StatusUnauthorized {
			t.Fatalf("admin declared no access but answered anonymously: %s", r)
		}
	})
	s.step("removing_access_needs_key_again", func(t *testing.T) {
		keyGate(t)
		deploy(t, false)
		app := object(t, tg.ok(t, time.Minute, "app", "get", site).stdout)
		if field(app, "require_authn") != true {
			t.Fatalf("site no longer declares access but require_authn = %v", field(app, "require_authn"))
		}
		eventually(t, "site refuses anonymous calls", time.Minute, 3*time.Second, func() (bool, string) {
			r := get(tg.appURL(site) + "/")
			return r.status == http.StatusUnauthorized, r.String()
		})
	})
}
