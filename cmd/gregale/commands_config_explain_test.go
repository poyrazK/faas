package main_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

func writeExplainFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func explainWinner(t *testing.T, e reposcan.SettingExplanation) reposcan.SettingLayer {
	t.Helper()
	for _, layer := range e.Layers {
		if layer.Wins {
			return layer
		}
	}
	t.Fatalf("%s: no winning layer in %+v", e.Key, e.Layers)
	return reposcan.SettingLayer{}
}

// ADR-20261107180000: `gregale config explain` names the file a target key
// comes from, mirroring the lookup app-scoped commands use.
func TestConfigExplainTargetKeys(t *testing.T) {
	repo := t.TempDir()
	writeExplainFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	writeExplainFile(t, filepath.Join(repo, "gregale.yaml"), "project:\n  name: shop\n  app: shop-web\n")
	writeExplainFile(t, filepath.Join(repo, "services", "billing", "gregale.yaml"), "project:\n  app: shop-billing\n")
	billing := filepath.Join(repo, "services", "billing")

	app, err := main.ExplainConfigKey("app", billing)
	if err != nil {
		t.Fatal(err)
	}
	if win := explainWinner(t, app); app.Value != "shop-billing" || win.Source != "gregale.yaml `project:`" || win.Field != "project.app" {
		t.Fatalf("app = %q from %+v, want shop-billing from the service manifest", app.Value, win)
	}
	project, err := main.ExplainConfigKey("project", billing)
	if err != nil || project.Value != "shop" {
		t.Fatalf("project = %+v, %v", project, err)
	}
	env, err := main.ExplainConfigKey("environment", repo)
	if err != nil || env.Value != "" || explainWinner(t, env).Source != "default" {
		t.Fatalf("environment = %+v, %v; want the command default (never from a manifest)", env, err)
	}

	// A link file wins as a whole; the manifest is shown as ignored.
	writeExplainFile(t, filepath.Join(repo, ".gregale", "project.json"), `{"version":1,"project":"shop-staging","environment":"staging"}`)
	app, err = main.ExplainConfigKey("app", billing)
	if err != nil {
		t.Fatal(err)
	}
	if app.Value != "" {
		t.Fatalf("app = %q, want none: the link file names no app and shadows the manifest", app.Value)
	}
	var out bytes.Buffer
	main.RenderSettingExplanation(&out, app)
	if !strings.Contains(out.String(), "ignored while .gregale/project.json exists") {
		t.Fatalf("output does not say the manifest is shadowed:\n%s", out.String())
	}
	env, err = main.ExplainConfigKey("environment", billing)
	if err != nil || env.Value != "staging" {
		t.Fatalf("environment = %+v, %v", env, err)
	}
}

func TestConfigExplainProjectSettingUsesRepoRoot(t *testing.T) {
	repo := t.TempDir()
	writeExplainFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	writeExplainFile(t, filepath.Join(repo, "compose.yaml"), "services:\n  api:\n    image: nginx:1.27\n    ports: [\"8080:80\"]\n")
	writeExplainFile(t, filepath.Join(repo, "gregale.yaml"), "project:\n  services:\n    api:\n      visibility: internal\n")
	sub := filepath.Join(repo, "docs")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := main.ExplainConfigKey("services.api.visibility", sub)
	if err != nil {
		t.Fatal(err)
	}
	if got.Value != "internal" || explainWinner(t, got).Field != "services.api.visibility" {
		t.Fatalf("got %+v", got)
	}
	var out bytes.Buffer
	main.RenderSettingExplanation(&out, got)
	if !strings.HasPrefix(out.String(), "services.api.visibility = internal\n") || !strings.Contains(out.String(), "<- wins") {
		t.Fatalf("output:\n%s", out.String())
	}
	if _, err := main.ExplainConfigKey("memory", repo); err == nil || !strings.Contains(err.Error(), "services.<name>") {
		t.Fatalf("unknown key error = %v, want the key list", err)
	}
}
