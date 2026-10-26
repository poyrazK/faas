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
}

func (c *fakeDevGroupClient) RegisterScenarioTest(_ context.Context, runID string, req api.RegisterScenarioTestRequest) error {
	if c.err != nil {
		return c.err
	}
	c.runs = append(c.runs, runID)
	c.registered = append(c.registered, req.Members...)
	return nil
}

// ADR-20261025121530: each `gregale dev --all` child joins its group's
// namespace under its project name once its environment exists.
func TestJoinDevGroup(t *testing.T) {
	workspace := strings.Repeat("a", 32)
	client := &fakeDevGroupClient{}
	var warn bytes.Buffer
	if err := main.JoinDevGroup(context.Background(), client, workspace, "api", "dev-api-1234", &warn); err != nil {
		t.Fatal(err)
	}
	if want := []api.ScenarioTestWorkload{{Workload: "api", AppSlug: "dev-api-1234"}}; !reflect.DeepEqual(client.registered, want) ||
		!reflect.DeepEqual(client.runs, []string{workspace}) {
		t.Fatalf("registered %v in runs %v", client.registered, client.runs)
	}

	// Too short to be called by name: reported, and the loop keeps running.
	client = &fakeDevGroupClient{}
	if err := main.JoinDevGroup(context.Background(), client, workspace, "ui", "dev-ui-1234", &warn); err != nil || len(client.registered) != 0 {
		t.Fatalf("err = %v, registered = %v", err, client.registered)
	}
	if !strings.Contains(warn.String(), `"ui" runs outside the group`) {
		t.Fatalf("warning = %q", warn.String())
	}

	// Still registered from an earlier run of the same group.
	client = &fakeDevGroupClient{err: &api.APIError{Problem: api.Problem{Status: 409}}}
	if err := main.JoinDevGroup(context.Background(), client, workspace, "web", "dev-web-1234", &warn); err != nil {
		t.Fatalf("a repeat registration failed: %v", err)
	}

	client = &fakeDevGroupClient{err: errors.New("unavailable")}
	if err := main.JoinDevGroup(context.Background(), client, workspace, "web", "dev-web-1234", &warn); err == nil {
		t.Fatal("a registration failure was ignored")
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
