// adr: 20261124090000
package main_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gregale "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/simpleapp"
)

type accessClient struct {
	app     api.AppResponse
	updates []string
}

func (c *accessClient) GetApp(context.Context, string) (api.AppResponse, error) { return c.app, nil }

func (c *accessClient) UpdateApp(_ context.Context, _ string, req api.UpdateAppRequest) (api.AppResponse, error) {
	if req.DeclaredAccess != nil {
		c.updates = append(c.updates, *req.DeclaredAccess)
	}
	return c.app, nil
}

// TestDeployManifestAccess pins the single-app access key (ADR-20261124090000
// amendment 1): a deploy sends the declared access, sends "" to re-lock an
// app that used to declare it, sends nothing for an app that never used the
// key, and refuses a flag that contradicts the file.
func TestDeployManifestAccess(t *testing.T) {
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
		name, yaml, previous string
		noRequireAuthn       bool
		requireAuthn         bool
		want                 []string
		wantErr              string
	}{
		{name: "declared public", yaml: "access: public\n", want: []string{"public"}},
		{name: "removed re-locks", previous: "public", want: []string{""}},
		{name: "never declared sends nothing"},
		{name: "flag contradicts file", yaml: "access: key\n", noRequireAuthn: true, wantErr: "contradicts"},
		{name: "bad value", yaml: "access: open\n", wantErr: "not public or key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &accessClient{app: api.AppResponse{Slug: "demo", DeclaredAccess: tc.previous}}
			err := gregale.DeployManifestAccess(t.Context(), client, "demo", dir(t, tc.yaml), tc.requireAuthn, tc.noRequireAuthn)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(client.updates, ",") != strings.Join(tc.want, ",") || len(client.updates) != len(tc.want) {
				t.Fatalf("declared_access sent = %q, want %q", client.updates, tc.want)
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
