package main_test

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/productcap"
)

// Only commands that can submit a deployment tell the user to inspect
// deployment status before retrying after a lost reply.
func TestTransportHintMentionsDeploysOnlyForDeployCommands(t *testing.T) {
	old := *main.InvokedCommandPath
	t.Cleanup(func() { *main.InvokedCommandPath = old })
	refused := &url.Error{Op: "Get", URL: "https://api.example.test", Err: errors.New("connection refused")}
	for _, tc := range []struct {
		path   string
		err    error
		deploy bool
	}{
		{"logs", refused, false},
		{"status", refused, false},
		{"secrets set", refused, false},
		{"deploy", refused, true},
		{"start", refused, true},
		{"mcp deploy", refused, true},
		{"logs", context.Canceled, false},
		{"rollback", context.Canceled, true},
	} {
		*main.InvokedCommandPath = tc.path
		hint := main.TransportProblem(tc.err).Hint
		if got := strings.Contains(hint, "deployment status"); got != tc.deploy {
			t.Errorf("%s (%v): hint mentions deployments = %v, want %v: %q", tc.path, tc.err, got, tc.deploy, hint)
		}
		if hint == "" {
			t.Errorf("%s (%v): empty hint", tc.path, tc.err)
		}
	}
}

func TestCapabilitiesLoggedOutShowsPublicCatalog(t *testing.T) {
	t.Setenv("FAAS_TOKEN", "")
	stdout, readStderr, restore := main.SwapIO(t)
	defer restore()
	if code := main.Run([]string{"capabilities"}); code != 0 {
		t.Fatalf("capabilities exit = %d; stderr %q", code, readStderr())
	}
	out := stdout.String()
	if !strings.Contains(out, "Public capability catalog") || !strings.Contains(out, "custom-domains") || !strings.Contains(out, "gregale login") {
		t.Fatalf("logged-out catalog incomplete:\n%s", out)
	}
	catalog, err := productcap.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, capability := range catalog.Capabilities {
		if capability.Maturity == productcap.MaturityInternal && strings.Contains(out, capability.ID+" ") {
			t.Errorf("internal capability %q listed:\n%s", capability.ID, out)
		}
	}
}

// --json stays account-scoped: scripts must not mistake the public catalog
// for their entitlement.
func TestCapabilitiesLoggedOutJSONStillRequiresLogin(t *testing.T) {
	t.Setenv("FAAS_TOKEN", "")
	var out, errOut bytes.Buffer
	oldOut, oldErr := *main.OsStdout, *main.OsStderr
	*main.OsStdout, *main.OsStderr = &out, &errOut
	t.Cleanup(func() { *main.OsStdout, *main.OsStderr = oldOut, oldErr })
	if code := main.Run([]string{"--json", "capabilities"}); code != 2 {
		t.Fatalf("capabilities --json exit = %d, want 2; stdout %q", code, out.String())
	}
}
