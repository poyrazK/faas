package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCmdLinkStoresSingleWorkloadContextAndIgnoresIt(t *testing.T) {
	resetJSONOut(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "services", "api")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)
	authedFakeAPI(t, `{"id":"project-1","slug":"shop","workloads":[{"slug":"api","workload_name":"api"}]}`, http.StatusOK)
	var out bytes.Buffer
	previousOut := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = previousOut })

	if code := cmdLink([]string{"shop"}); code != 0 {
		t.Fatalf("exit = %d, output=%s", code, out.String())
	}
	path := projectContextPath(root)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read context: %v", err)
	}
	var context localProjectContext
	if err := json.Unmarshal(b, &context); err != nil {
		t.Fatalf("decode context: %v", err)
	}
	if context.Version != projectContextVersion || context.Project != "shop" || context.App != "api" {
		t.Fatalf("context = %+v", context)
	}
	ignored, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatalf("read gitignore: %v", err)
	}
	if !strings.Contains(string(ignored), ".gregale/") {
		t.Fatalf("gitignore = %q, want .gregale/", ignored)
	}
	if app, err := linkedAppSlug(nested); err != nil || app != "api" {
		t.Fatalf("linked app = %q, err=%v", app, err)
	}
}

func TestCmdLinkRequiresExplicitAppForMultipleWorkloads(t *testing.T) {
	resetJSONOut(t)
	root := t.TempDir()
	t.Chdir(root)
	authedFakeAPI(t, `{"id":"project-1","slug":"shop","workloads":[{"slug":"api"},{"slug":"worker"}]}`, http.StatusOK)
	var out bytes.Buffer
	previousOut := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = previousOut })

	if code := cmdLink([]string{"shop"}); code != 0 {
		t.Fatalf("exit = %d, want 0; output=%s", code, out.String())
	}
	context, _, err := linkedProjectContext(root)
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
	resetJSONOut(t)
	root := t.TempDir()
	nested := filepath.Join(root, "packages", "api")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := saveProjectContext(root, localProjectContext{Version: projectContextVersion, Project: "shop", App: "api", Environment: "development"}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)
	var out bytes.Buffer
	previousOut := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = previousOut })

	if code := cmdContext(nil); code != 0 {
		t.Fatalf("context exit = %d; output=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "Project:     shop") || !strings.Contains(out.String(), "Environment: development") {
		t.Fatalf("context output = %s", out.String())
	}
	if code := cmdUnlink(nil); code != 0 {
		t.Fatalf("unlink exit = %d; output=%s", code, out.String())
	}
	if _, err := os.Stat(projectContextPath(root)); !os.IsNotExist(err) {
		t.Fatalf("context file after unlink: err=%v", err)
	}
}
