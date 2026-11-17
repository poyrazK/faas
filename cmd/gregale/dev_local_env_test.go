package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func localEnvValue(env []string, key string) string {
	value := ""
	for _, item := range env {
		if k, v, ok := strings.Cut(item, "="); ok && k == key {
			value = v
		}
	}
	return value
}

func TestDevLocalEnvFileSelection(t *testing.T) {
	root := writeDevLocalProject(t, map[string]string{
		"gregale.yaml": "dev:\n  env_file: .env.dev\n",
		".env.dev":     "SELECTED=manifest\n",
	})
	cwd := writeDevLocalProject(t, map[string]string{"explicit.env": "SELECTED=flag\n"})
	t.Chdir(cwd)
	for _, tc := range []struct {
		name, flag, value, path string
		explicit                bool
	}{
		{name: "manifest relative to source", value: "manifest", path: filepath.Join(root, ".env.dev")},
		{name: "flag relative to cwd", flag: "explicit.env", explicit: true, value: "flag", path: filepath.Join(cwd, "explicit.env")},
		{name: "explicit empty disables default", explicit: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotRoot, path, env, err := main.DevLocalInputsForTest(root, tc.flag, tc.explicit)
			if err != nil || gotRoot != root || path != tc.path || localEnvValue(env, "SELECTED") != tc.value {
				t.Fatalf("root=%q path=%q selection=%q err=%v", gotRoot, path, localEnvValue(env, "SELECTED"), err)
			}
		})
	}
}

func TestDevLocalEnvReloadRebuildsLayers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.env")
	base := main.DevLocalEnvForTest([]string{"SHELL=shell", "DATABASE_URL=shell-db"}, map[string]string{"DATABASE_URL": "resource-db"}, nil)
	for _, tc := range []struct {
		body, database, shell, added string
	}{
		{body: "DATABASE_URL=file-db\nSHELL=file\nADDED=file-only\n", database: "file-db", shell: "file", added: "file-only"},
		{body: "SHELL=changed\n", database: "resource-db", shell: "changed"},
		{body: "SHELL=\n", database: "resource-db"},
		{body: "# all overrides removed\n", database: "resource-db", shell: "shell"},
	} {
		if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
			t.Fatal(err)
		}
		env, err := main.LoadDevLocalEnvForTest(path, base)
		if err != nil || localEnvValue(env, "DATABASE_URL") != tc.database || localEnvValue(env, "SHELL") != tc.shell || localEnvValue(env, "ADDED") != tc.added {
			t.Fatalf("layering failed: %v", err)
		}
	}
	if localEnvValue(base, "DATABASE_URL") != "resource-db" || localEnvValue(base, "ADDED") != "" {
		t.Fatal("reading the file mutated the original environment")
	}
	if _, _, err := main.ReadDevEnvFile(path); err == nil {
		t.Fatal("remote secret sync lost its empty-file guard")
	}
}

func TestDevLocalEnvRejectsInvalidFilesWithoutValues(t *testing.T) {
	for _, body := range []string{"BROKEN sensitive-fixture", "TOKEN=sensitive-fixture\nTOKEN=other\n", "TOKEN=sensitive-fixture\x00\n"} {
		path := filepath.Join(t.TempDir(), "app.env")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := main.LoadDevLocalEnvForTest(path, nil)
		if err == nil || strings.Contains(err.Error(), "sensitive-fixture") {
			t.Fatalf("unsafe or missing parse error: %v", err)
		}
	}
	path := filepath.Join(t.TempDir(), "app.env")
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), path); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := main.LoadDevLocalEnvForTest(path, nil); err == nil {
		t.Fatal("followed a symlink")
	}
}
