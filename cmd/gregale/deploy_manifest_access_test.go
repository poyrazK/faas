// adr: 20261124090000
package main_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gregale "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/simpleapp"
)

type accessClient struct {
	app api.AppResponse
	err error
}

func (c accessClient) GetApp(context.Context, string) (api.AppResponse, error) { return c.app, c.err }

// TestCheckManifestAccessFlags pins the single-app access key
// (ADR-20261124090000 amendment 1): apid applies gregale.yaml's access on the
// deploy, so a flag the file would undo fails before anything changes.
func TestCheckManifestAccessFlags(t *testing.T) {
	dir := func(t *testing.T, yaml string) string {
		t.Helper()
		d := t.TempDir()
		if yaml != "" {
			if err := os.WriteFile(filepath.Join(d, "gregale.yaml"), []byte(yaml), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return d
	}
	for _, tc := range []struct {
		name, yaml, previous         string
		requireAuthn, noRequireAuthn bool
		getErr                       error
		wantErr                      string
	}{
		{name: "no flag", yaml: "access: key\n"},
		{name: "flag agrees", yaml: "access: public\n", noRequireAuthn: true},
		{name: "key contradicts --no-require-authn", yaml: "access: key\n", noRequireAuthn: true, wantErr: "contradicts"},
		{name: "public contradicts --require-authn", yaml: "access: public\n", requireAuthn: true, wantErr: "contradicts"},
		{name: "removed key contradicts --no-require-authn", previous: "public", noRequireAuthn: true, wantErr: "used to declare access: public"},
		{name: "never declared keeps the flag", noRequireAuthn: true},
		{name: "new app keeps the flag", noRequireAuthn: true, getErr: &api.APIError{Problem: *api.NewProblem(http.StatusNotFound, "not_found", "Not found", "no app")}},
		{name: "lookup failure is reported", noRequireAuthn: true, getErr: errors.New("connection refused"), wantErr: "connection refused"},
		{name: "bad value", yaml: "access: open\n", requireAuthn: true, wantErr: "not public or key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := accessClient{app: api.AppResponse{Slug: "demo", DeclaredAccess: tc.previous}, err: tc.getErr}
			err := gregale.CheckManifestAccessFlags(t.Context(), client, "demo", dir(t, tc.yaml), tc.requireAuthn, tc.noRequireAuthn)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

// TestSimpleAppPlanShowsAccess: the plan an MCP agent reviews says whether the
// URL is public, and how to open it when the source declares nothing.
func TestSimpleAppPlanShowsAccess(t *testing.T) {
	for _, tc := range []struct{ yaml, access, hint string }{
		{yaml: "access: public\n", access: "public"},
		{yaml: "", hint: simpleapp.AccessHintUndeclared},
	} {
		d := t.TempDir()
		files := map[string]string{"package.json": `{"name":"demo","scripts":{"start":"node server.js"}}`, "server.js": "require('http').createServer((q,s)=>s.end('ok')).listen(8080)\n"}
		if tc.yaml != "" {
			files["gregale.yaml"] = tc.yaml
		}
		for name, body := range files {
			if err := os.WriteFile(filepath.Join(d, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		plan, err := gregale.ResolveSimpleAppPlan(d, "demo", "", simpleapp.SourceDirectory, true, false, "")
		if err != nil {
			t.Fatal(err)
		}
		if plan.Access != tc.access || plan.AccessHint != tc.hint {
			t.Fatalf("plan access = %q hint = %q; want %q %q", plan.Access, plan.AccessHint, tc.access, tc.hint)
		}
	}
}
