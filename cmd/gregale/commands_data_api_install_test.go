package main_test

import (
	"github.com/onebox-faas/faas/cmd/gregale"
	"os"
	"path/filepath"
	"testing"
)

func TestDataAPIInstallRejectsInvalidArgumentsBeforeRunningTools(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"private":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{}, {"--bundle", "bundle.json", "--restore"},
		{"--restore", "--base-url", "https://artifacts.example/"},
		{"--bundle", "bundle.json", "--from", "."},
		{"--bundle", "bundle.json", "--directory", root, "--client-directory", ".."},
		{"--bundle", "bundle.json", "--directory", root, "--client-directory", root},
		{"--bundle", "bundle.json", "--directory", root, "--client-directory", "missing"},
		{"--bundle", "bundle.json", "--directory", root, "unexpected"},
	} {
		if main.CmdDataAPIInstall(args) == 0 {
			t.Fatalf("accepted invalid install arguments: %v", args)
		}
	}
}
