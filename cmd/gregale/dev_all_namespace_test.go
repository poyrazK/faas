package main_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

type fakeDevGroupClient struct {
	runs       []string
	registered []api.ScenarioTestWorkload
	err        error
	slugsOnly  bool // an API that predates service names
}

func (c *fakeDevGroupClient) RegisterScenarioTest(_ context.Context, runID string, req api.RegisterScenarioTestRequest) error {
	if c.err != nil {
		return c.err
	}
	for _, member := range req.Members {
		if c.slugsOnly && !api.ValidAppSlug(member.Workload) {
			return &api.APIError{Problem: api.Problem{Status: 400}}
		}
	}
	c.runs = append(c.runs, runID)
	c.registered = append(c.registered, req.Members...)
	return nil
}

// ADR-20261025121530, ADR-20261026105459: each `gregale dev --all` child
// joins its group's namespace under its service name once its environment
// exists.
func TestJoinDevGroup(t *testing.T) {
	workspace := strings.Repeat("a", 32)
	ctx := context.Background()
	var warn bytes.Buffer
	for _, tc := range []struct {
		name, service, project string
		slugsOnly              bool
		want                   string
	}{
		{"service name", "api", "api", false, "api"},
		{"short service name", "ui", "app-ui", false, "ui"},
		{"older API takes the project name", "ui", "app-ui", true, "app-ui"},
	} {
		client := &fakeDevGroupClient{slugsOnly: tc.slugsOnly}
		if err := main.JoinDevGroup(ctx, client, workspace, tc.service, tc.project, "dev-1234", &warn); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if want := []api.ScenarioTestWorkload{{Workload: tc.want, AppSlug: "dev-1234"}}; !reflect.DeepEqual(client.registered, want) ||
			!reflect.DeepEqual(client.runs, []string{workspace}) {
			t.Fatalf("%s: registered %v in runs %v", tc.name, client.registered, client.runs)
		}
	}
	if !strings.Contains(warn.String(), `cannot register the service name "ui" yet; the other apps call this one "app-ui"`) {
		t.Fatalf("warning = %q", warn.String())
	}

	// Still registered from an earlier run of the same group.
	client := &fakeDevGroupClient{err: &api.APIError{Problem: api.Problem{Status: 409}}}
	if err := main.JoinDevGroup(ctx, client, workspace, "web", "web", "dev-web-1234", &warn); err != nil {
		t.Fatalf("a repeat registration failed: %v", err)
	}

	client = &fakeDevGroupClient{err: errors.New("unavailable")}
	if err := main.JoinDevGroup(ctx, client, workspace, "web", "web", "dev-web-1234", &warn); err == nil {
		t.Fatal("a registration failure was ignored")
	}
}

func TestDevGroupServiceAndDepends(t *testing.T) {
	t.Setenv(main.DevGroupServiceEnv, "ui")
	t.Setenv(main.DevGroupDependsEnv, "api, billing,Not_A_Name,")
	if got := main.DevGroupService("app-ui"); got != "ui" {
		t.Fatalf("service = %q", got)
	}
	if got := main.DevGroupDepends(); !reflect.DeepEqual(got, []string{"api", "billing"}) {
		t.Fatalf("depends = %q", got)
	}
	t.Setenv(main.DevGroupServiceEnv, "")
	if got := main.DevGroupService("app-ui"); got != "app-ui" {
		t.Fatalf("service without a supervisor = %q, want the project", got)
	}
}

// Each declared group dependency gets the variables a deployed project
// service gets for its depends_on.
func TestDevGroupServiceConfig(t *testing.T) {
	got := map[string]string{}
	for _, pair := range main.DevGroupServiceConfig([]string{"billing-api"}) {
		got[pair.Key] = pair.Value
	}
	want := map[string]string{
		"GREGALE_SERVICE_BILLING_API_URL":       "http://billing-api.svc.gregale:10081",
		"GREGALE_SERVICE_BILLING_API_HTTPS_URL": "https://billing-api.internal",
		"GREGALE_SERVICE_BILLING_API_HOST":      "billing-api.svc.gregale",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("config = %v, want %v", got, want)
	}
}

func TestDevGroupWorkspace(t *testing.T) {
	for _, tc := range []struct{ env, want string }{
		{"", ""},
		{strings.Repeat("0", 32), strings.Repeat("0", 32)},
		{strings.Repeat("A", 32), ""},
		{"short", ""},
	} {
		t.Setenv(main.DevGroupWorkspaceEnv, tc.env)
		if got := main.DevGroupWorkspace(); got != tc.want {
			t.Errorf("env %q: workspace = %q, want %q", tc.env, got, tc.want)
		}
	}
}

// Compose services that build from a source directory join `dev --all` and
// `gregale start`'s picker; an image-only service has nothing to run.
func TestDiscoverProjectSourcesFindsComposeServices(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("compose.yaml", "services:\n  api:\n    build: ./api\n  web:\n    build: ./web\n  db:\n    image: postgres:17\n  proxy:\n    image: nginx:1.27\n")
	write("api/Dockerfile", "FROM scratch\n")
	write("web/package.json", `{"name":"web","scripts":{"start":"node server.js"}}`)

	sources, err := main.DiscoverProjectSources(root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, source := range sources {
		got = append(got, source.Name+"="+source.RootDir)
	}
	if want := []string{"api=api", "web=web"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sources = %v, want %v", got, want)
	}
}
