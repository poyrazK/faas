package main_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/onebox-faas/faas/cmd/gregale"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectContextTestSandboxPreservesAncestorContext(t *testing.T) {
	parent := t.TempDir()
	if !main.ProjectContextRootAllowed(parent) {
		t.Fatalf("test temporary directory escaped the project-context sandbox: %s", parent)
	}
	if _, err := main.SaveProjectContext(parent, main.LocalProjectContext{Version: 1, Project: "shop"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(parent, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "isolated")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	previous := (*main.TestOnlyProjectContextRoot)
	(*main.TestOnlyProjectContextRoot) = root
	t.Cleanup(func() { (*main.TestOnlyProjectContextRoot) = previous })
	if _, _, err := main.FindProjectContext(root); !errors.Is(err, (*main.ErrProjectContextNotFound)) {
		t.Fatalf("inherited parent context: %v", err)
	}
	got, err := main.ProjectContextRoot(root)
	if err != nil || got != root {
		t.Fatalf("link destination escaped sandbox: %q, %v", got, err)
	}
	if _, err := main.SaveProjectContext(got, main.LocalProjectContext{Version: 1, Project: "test-project", App: "test-app"}); err != nil {
		t.Fatal(err)
	}
	context, _, err := main.FindProjectContext(root)
	if err != nil || context.Project != "test-project" {
		t.Fatalf("test-owned context unavailable: %+v, %v", context, err)
	}
	body, err := os.ReadFile(main.ProjectContextPath(parent))
	if err != nil || !bytes.Contains(body, []byte(`"shop"`)) {
		t.Fatalf("ancestor context overwritten: %s, %v", body, err)
	}
}

func TestCmdLinkStoresSingleWorkloadContextAndIgnoresIt(t *testing.T) {
	main.ResetJSONOut(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "services", "api")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)
	main.AuthedFakeAPI(t, `{"id":"project-1","slug":"shop","workloads":[{"slug":"api","workload_name":"api"}]}`, http.StatusOK)
	var out bytes.Buffer
	previousOut := (*main.OsStdout)
	(*main.OsStdout) = &out
	t.Cleanup(func() { (*main.OsStdout) = previousOut })

	if code := main.CmdLink([]string{"shop"}); code != 0 {
		t.Fatalf("exit = %d, output=%s", code, out.String())
	}
	path := main.ProjectContextPath(root)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read context: %v", err)
	}
	var context main.LocalProjectContext
	if err := json.Unmarshal(b, &context); err != nil {
		t.Fatalf("decode context: %v", err)
	}
	if context.Version != main.ProjectContextVersion || context.Project != "shop" || context.App != "api" {
		t.Fatalf("context = %+v", context)
	}
	ignored, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatalf("read gitignore: %v", err)
	}
	if !strings.Contains(string(ignored), ".gregale/") {
		t.Fatalf("gitignore = %q, want .gregale/", ignored)
	}
	if app, err := main.LinkedAppSlug(nested); err != nil || app != "api" {
		t.Fatalf("linked app = %q, err=%v", app, err)
	}
}

func TestCmdLinkRequiresExplicitAppForMultipleWorkloads(t *testing.T) {
	main.ResetJSONOut(t)
	root := t.TempDir()
	t.Chdir(root)
	main.AuthedFakeAPI(t, `{"id":"project-1","slug":"shop","workloads":[{"slug":"api"},{"slug":"worker"}]}`, http.StatusOK)
	var out bytes.Buffer
	previousOut := (*main.OsStdout)
	(*main.OsStdout) = &out
	t.Cleanup(func() { (*main.OsStdout) = previousOut })

	if code := main.CmdLink([]string{"shop"}); code != 0 {
		t.Fatalf("exit = %d, want 0; output=%s", code, out.String())
	}
	context, _, err := main.LinkedProjectContext(root)
	if err != nil {
		t.Fatalf("read context: %v", err)
	}
	if context.App != "" {
		t.Fatalf("app = %q, want empty for ambiguous project", context.App)
	}
	if !strings.Contains(out.String(), "workloads; pass --app") {
		t.Fatalf("output missing ambiguity guidance: %s", out.String())
	}
}

func TestCmdContextAndUnlinkUseNearestCheckoutContext(t *testing.T) {
	main.ResetJSONOut(t)
	root := t.TempDir()
	nested := filepath.Join(root, "packages", "api")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := main.SaveProjectContext(root, main.LocalProjectContext{Version: main.ProjectContextVersion, Project: "shop", App: "api", Environment: "development"}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)
	var out bytes.Buffer
	previousOut := (*main.OsStdout)
	(*main.OsStdout) = &out
	t.Cleanup(func() { (*main.OsStdout) = previousOut })

	if code := main.CmdContext(nil); code != 0 {
		t.Fatalf("context exit = %d; output=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "Project:     shop") || !strings.Contains(out.String(), "Environment: development") {
		t.Fatalf("context output = %s", out.String())
	}
	if code := main.CmdUnlink(nil); code != 0 {
		t.Fatalf("unlink exit = %d; output=%s", code, out.String())
	}
	if _, err := os.Stat(main.ProjectContextPath(root)); !os.IsNotExist(err) {
		t.Fatalf("context file after unlink: err=%v", err)
	}
}

func TestResolveEnvironmentFlagOrContext(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "packages", "api")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := main.SaveProjectContext(root, main.LocalProjectContext{
		Version: main.ProjectContextVersion, Project: "shop", App: "api", Environment: "staging",
	}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)

	got, err := main.ResolveEnvironmentFlagOrContext("")
	if err != nil || got != "staging" {
		t.Fatalf("context environment = %q, err=%v; want staging", got, err)
	}
	got, err = main.ResolveEnvironmentFlagOrContext("production")
	if err != nil || got != "production" {
		t.Fatalf("explicit environment = %q, err=%v; want production", got, err)
	}
}

func TestResolveEnvironmentFlagOrContextWithoutContext(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := main.ResolveEnvironmentFlagOrContext("")
	if err != nil {
		t.Fatalf("resolve without context: %v", err)
	}
	if got != "" {
		t.Fatalf("environment without context = %q, want empty", got)
	}
}
