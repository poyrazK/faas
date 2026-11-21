package runenv_test

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runenv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBuildDiagnosticsExcludeBootSecretsAndHostPaths(t *testing.T) {
	if got := runenv.BuildDiagnosticLine("[0.121] kernel host root /srv/fc/private"); got != "" {
		t.Fatal(got)
	}
	if got := runenv.BuildDiagnosticLine(`#1 {"Authorization":"Bearer PRIVATE_BEARER", "cookie":"PRIVATE_COOKIE"}`); strings.Contains(got, "PRIVATE_") {
		t.Fatal("quoted credential leaked", got)
	}
	for _, raw := range []string{
		"#1 Cookie: session=PRIVATE_SESSION; other=PRIVATE_OTHER",
		`#1 password="PRIVATE VALUE WITH SPACES"`,
		"#1 Authorization: Bearer PRIVATE_TOKEN more=PRIVATE_OTHER",
	} {
		if got := runenv.BuildDiagnosticLine(raw); strings.Contains(got, "PRIVATE") {
			t.Fatal("multi-part credential leaked", got)
		}
	}
	input := "#7 1.2 fetching https://user:password@example.com/package?token=private&sig=credential#secret authorization=BearerSecret /srv/fc/private/file"
	got := runenv.BuildDiagnosticLine(input)
	for _, secret := range []string{"password", "private", "credential", "BearerSecret", "/srv/fc", "#secret"} {
		if strings.Contains(got, secret) {
			t.Fatalf("leaked %s: %s", secret, got)
		}
	}
	if !strings.Contains(got, "https://example.com/package") {
		t.Fatal(got)
	}
	got = runenv.BuildDiagnosticLine("#8 0.2 " + strings.Repeat("é", api.ExecutionEnvironmentDiagnosticMaxLineBytes))
	if len(got) > api.ExecutionEnvironmentDiagnosticMaxLineBytes || !utf8.ValidString(got) {
		t.Fatal("invalid bound")
	}
}
func TestEnvironmentFailureDiagnostics(t *testing.T) {
	for _, tc := range []struct{ raw, code string }{{"npm ERR E404", "dependency_resolution"}, {"ERROR: hashes mismatch", "integrity_mismatch"}, {"context deadline exceeded", "build_timeout"}, {"registry DNS preflight", "builder_unavailable"}, {"builderd: insufficient host free space for build drive", "builder_unavailable"}, {"no space left", "size_limit"}} {
		code, msg := runenv.DiagnosticFailure(tc.raw)
		if code != tc.code || msg == "" {
			t.Fatal(code, msg)
		}
	}
}
