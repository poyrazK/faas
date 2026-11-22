package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type fakeProjectAppReader map[string]api.AppResponse

func (f fakeProjectAppReader) GetApp(_ context.Context, slug string) (api.AppResponse, error) {
	app, ok := f[slug]
	if !ok {
		return api.AppResponse{}, errors.New("not found")
	}
	return app, nil
}

// The final project-deploy screen names each public app's URL and auth, and
// how to open a bearer-protected one (retail-store feedback B5).
func TestRenderProjectEndpointsListsURLsAndAuth(t *testing.T) {
	t.Setenv("FAAS_APPS_DOMAIN", "")
	apps := fakeProjectAppReader{
		"shop-ui":     {Slug: "shop-ui", RequireAuthn: true, PublicAuth: api.PublicAuthStatus{Mode: api.AppPublicAuthModeBearer}},
		"shop-cart":   {Slug: "shop-cart", Visibility: string(api.AppVisibilityInternal)},
		"shop-public": {Slug: "shop-public", CanonicalURL: "https://shop.example.com"},
	}
	apply := api.ApplyResponse{Apps: []api.ApplyResponseApp{{Slug: "shop-ui"}, {Slug: "shop-cart"}, {Slug: "shop-public"}, {Slug: "shop-gone"}}}
	var out bytes.Buffer
	renderProjectEndpoints(context.Background(), &out, apps, apply)
	got := out.String()
	for _, want := range []string{
		"Endpoints:",
		"shop-ui      https://shop-ui.gregale.dev  (AUTH: bearer)",
		"shop-public  https://shop.example.com  (AUTH: open)",
		"internal only (service mesh): shop-cart",
		"shop-ui needs a Gregale API key; to serve it publicly: gregale app update shop-ui --no-require-authn",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "shop-gone") {
		t.Errorf("an unreadable app was guessed:\n%s", got)
	}
}

// R6-3 (deploy-path probe 2026-11-21): a project whose apps are all internal
// printed "Endpoints:" with no URL and no way to make one public.
func TestRenderProjectEndpointsSaysHowToPublishWhenAllInternal(t *testing.T) {
	apps := fakeProjectAppReader{
		"r6-web": {Slug: "r6-web", Visibility: string(api.AppVisibilityInternal)},
		"r6-api": {Slug: "r6-api", Visibility: string(api.AppVisibilityInternal)},
	}
	apply := api.ApplyResponse{Apps: []api.ApplyResponseApp{{Slug: "r6-web"}, {Slug: "r6-api"}}}
	var out bytes.Buffer
	renderProjectEndpoints(context.Background(), &out, apps, apply)
	if got := out.String(); !strings.Contains(got, "no app has a public URL; to give one a public URL: gregale app r6-web --visibility public") {
		t.Fatalf("output:\n%s", got)
	}
}

type fakePlainVariables struct{ resp api.AppEnvListResponse }

func (f fakePlainVariables) GetAppsSlugEnvValues(context.Context, string, string) (api.AppEnvListResponse, error) {
	return f.resp, nil
}

// B7: env list shows plain variables with their values, sorted, filtered by
// --key, and says so when an older server returns no value.
func TestPrintPlainVariables(t *testing.T) {
	url := "http://orders.svc.gregale:10081"
	client := fakePlainVariables{resp: api.AppEnvListResponse{Count: 2, Quota: 32, Env: []api.AppEnvResponse{
		{Key: "ORDERS_URL", Value: &url}, {Key: "GIN_MODE"},
	}}}
	var out bytes.Buffer
	printPlainVariables(&out, client, "shop-ui", "", nil)
	if got := out.String(); !strings.Contains(got, "Variables (plain, readable; 2/32):\n  GIN_MODE=(value not returned by this server)\n  ORDERS_URL="+url+"\n") {
		t.Fatalf("output:\n%s", got)
	}
	out.Reset()
	printPlainVariables(&out, client, "shop-ui", "", []string{"ORDERS_URL"})
	if strings.Contains(out.String(), "GIN_MODE") {
		t.Fatalf("--key filter ignored:\n%s", out.String())
	}
}

// A successful project deploy links an unlinked checkout, and never replaces
// a link to another project (retail-store feedback A8).
func TestLinkCheckoutAfterProjectDeploy(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("node_modules/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	linkCheckoutAfterProjectDeploy(&out, root, "retail-store", false)
	context, _, err := linkedProjectContext(root)
	if err != nil || context.Project != "retail-store" {
		t.Fatalf("linked context = %+v, %v; want project retail-store", context, err)
	}
	if !strings.Contains(out.String(), "Linked this checkout to project retail-store") {
		t.Fatalf("missing link notice: %q", out.String())
	}

	out.Reset()
	linkCheckoutAfterProjectDeploy(&out, root, "other-project", false)
	context, _, _ = linkedProjectContext(root)
	if context.Project != "retail-store" {
		t.Fatalf("existing link replaced by %q", context.Project)
	}
	if !strings.Contains(out.String(), "link other-project") {
		t.Fatalf("missing relink hint: %q", out.String())
	}
}

type fakeEnvironmentEndpoints struct {
	fakeProjectAppReader
	state api.ProjectEnvironmentStateResponse
}

func (f fakeEnvironmentEndpoints) GetProjectEnvironmentState(context.Context, string, string) (api.ProjectEnvironmentStateResponse, error) {
	return f.state, nil
}

// prod 2026-11-19: `deploy --project --env staging` listed bh3-web's
// production URL under Endpoints; staging answers on its own URL.
func TestRenderProjectEndpointsForNamesTheEnvironmentURLs(t *testing.T) {
	t.Setenv("FAAS_APPS_DOMAIN", "")
	client := fakeEnvironmentEndpoints{
		fakeProjectAppReader: fakeProjectAppReader{"bh3-web": {Slug: "bh3-web"}},
		state: api.ProjectEnvironmentStateResponse{Workloads: []api.ProjectEnvironmentStateWorkloadResponse{
			{WorkloadSlug: "bh3-web", Release: api.ProjectEnvironmentReleaseWorkloadResponse{URL: "https://env-abc-web.gregale.dev"}},
		}},
	}
	apply := api.ApplyResponse{Apps: []api.ApplyResponseApp{{Slug: "bh3-web"}}}
	var out bytes.Buffer
	renderProjectEndpointsFor(context.Background(), &out, client, apply, "proj", "staging")
	if got := out.String(); !strings.Contains(got, "Endpoints (environment staging):") || !strings.Contains(got, "https://env-abc-web.gregale.dev") || strings.Contains(got, "https://bh3-web.gregale.dev") {
		t.Fatalf("staging endpoints = %q; want the environment URL, not production's", got)
	}
	out.Reset()
	renderProjectEndpointsFor(context.Background(), &out, client, apply, "proj", "")
	if got := out.String(); !strings.Contains(got, "https://bh3-web.gregale.dev") {
		t.Fatalf("production endpoints = %q; want the app URL", got)
	}
}
