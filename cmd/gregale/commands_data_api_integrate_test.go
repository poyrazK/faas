package main_test

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func TestDataAPIIntegratePreviewUsesExistingProjectWithoutRunningTools(t *testing.T) {
	root := t.TempDir()
	metadata := []byte(`{"name":"existing-app","devDependencies":{"typescript":"5.9.3"},"scripts":{"dev":"vite"}}`)
	if err := os.WriteFile(filepath.Join(root, "package.json"), metadata, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	if code := main.CmdDataAPIIntegrate([]string{"--directory", root, "--sdk-version", "0.1.0", "--dry-run"}); code != 0 {
		t.Fatalf("preview exited %d", code)
	}
	after, _ := os.ReadFile(filepath.Join(root, "package.json"))
	if !bytes.Equal(after, metadata) {
		t.Fatal("preview changed the package")
	}
	if _, err := os.Stat(filepath.Join(root, "gregale-data")); !os.IsNotExist(err) {
		t.Fatal("preview wrote integration files")
	}
}

func TestDataAPIIntegrateRejectsInvalidInputsWithoutChangingProject(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"devDependencies":{"typescript":"5.9.3"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{}, {"--directory", root}, {"--directory", root, "--sdk-version", "latest"},
		{"--directory", root, "--sdk-version", "0.1.0", "--bundle", "missing.json"},
		{"--directory", root, "--bundle", "missing.json"},
		{"--directory", root, "--sdk-version", "0.1.0", "--client-directory", ".."},
		{"--directory", root, "--sdk-version", "0.1.0", "extra"},
	} {
		if main.CmdDataAPIIntegrate(args) == 0 {
			t.Fatalf("accepted invalid arguments %v", args)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "gregale-data")); !os.IsNotExist(err) {
		t.Fatal("invalid command wrote integration files")
	}
}

func TestDataAPIIntegrateRunsTypeGenerationAndRequiredAuthorizationChecks(t *testing.T) {
	steps := main.DataAPIIntegrationSteps("/existing app", "web", "/approved bundle.json", "")
	want := []string{"--directory", "/existing app", "--config", "gregale-data/data-api.dev.json", "--port", "0", "--once", "--check", "--replay", "gregale-data/requests.json"}
	if steps[2].Command != "dev" || !reflect.DeepEqual(steps[2].Args, want) {
		t.Fatal(steps[2])
	}
	if !reflect.DeepEqual(steps[1].Args, []string{"--directory", "/existing app", "--client-directory", "web", "--bundle", "/approved bundle.json"}) {
		t.Fatal(steps[1])
	}
}

func TestDataAPIIntegrateUnavailableDockerLeavesProjectUnchanged(t *testing.T) {
	root, tools := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"devDependencies":{"typescript":"5.9.3"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"node", "npm", "docker"} {
		if err := os.WriteFile(filepath.Join(tools, name), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", tools)
	if main.CmdDataAPIIntegrate([]string{"--directory", root, "--sdk-version", "0.1.0"}) == 0 {
		t.Fatal("reported success with unavailable Docker")
	}
	if _, err := os.Stat(filepath.Join(root, "gregale-data")); !os.IsNotExist(err) {
		t.Fatal("unavailable Docker changed the project")
	}
}
