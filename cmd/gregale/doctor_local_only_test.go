package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

func loopbackCheck(t *testing.T, dir string) main.DoctorCheck {
	t.Helper()
	for _, check := range main.RunDoctorChecks(dir).Checks {
		if check.Name == "loopback-bind" {
			return check
		}
	}
	t.Fatal("loopback-bind check missing")
	return main.DoctorCheck{}
}

// Hunt 14 H14-92: `gregale doctor` on a fresh data-api template reported
// app_loopback_bound for dev.mjs, the local development server, although the
// deployed server.mjs binds 0.0.0.0. A listener line marked
// gregale:local-only is deliberate; an unmarked one still fails.
func TestDoctorLoopbackSkipsLinesMarkedLocalOnly(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("server.mjs", "server.listen(Number(process.env.PORT || 8080), '0.0.0.0')\n")
	write("dev.mjs", "server.listen(port, '127.0.0.1', ready) // gregale:local-only — local development server\n")
	if check := loopbackCheck(t, dir); check.Status != "ok" {
		t.Fatalf("marked local-only listener = %q %v, want ok", check.Status, check.Sources)
	}
	write("other.mjs", "server.listen(port, '127.0.0.1')\n")
	check := loopbackCheck(t, dir)
	if check.Status != "error" || len(check.Sources) != 1 || !strings.HasSuffix(check.Sources[0], "other.mjs:1") {
		t.Fatalf("unmarked loopback listener = %q %v, want one error at other.mjs:1", check.Status, check.Sources)
	}
}

// The built-in templates' deliberate local listeners carry the marker, so a
// freshly initialized data-api project passes the loopback check.
func TestDataAPITemplatePassesTheLoopbackCheck(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "app")
	if code, _, stderr := runCLI(t, "init", "--template", "data-api", "--path", dir); code != 0 {
		t.Fatalf("init data-api: exit %d %s", code, stderr)
	}
	if check := loopbackCheck(t, dir); check.Status != "ok" {
		t.Fatalf("data-api template loopback check = %q %v", check.Status, check.Sources)
	}
}
