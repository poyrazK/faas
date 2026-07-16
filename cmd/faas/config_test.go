package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSaveToken_FilePermsAre0600 covers the file-mode line in saveToken.
// The existing cli_test.go uses t.Setenv but doesn't pin the file mode, so
// perm of 0o600 was at 0% coverage.
func TestSaveToken_FilePermsAre0600(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)
	if err := saveToken("  fpn_test  "); err != nil {
		t.Fatalf("saveToken: %v", err)
	}
	p, err := tokenPath()
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if mode := st.Mode().Perm(); mode != 0o600 {
		t.Errorf("perm = %o, want 0600", mode)
	}
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(body)); got != "fpn_test" {
		t.Errorf("body trimmed = %q, want fpn_test", got)
	}
}

// TestLoadToken_PrefersEnvOverFile covers the env-vs-file branch in loadToken.
// The existing TestSaveAndLoadToken_EnvOverride tests env, but does NOT cover
// "env present AND file present AND env wins" — only one source at a time.
func TestLoadToken_PrefersEnvOverFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)
	p, _ := tokenPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_TOKEN", "env-token")
	if got := loadToken(); got != "env-token" {
		t.Errorf("loadToken = %q, want env-token (env should win)", got)
	}
}

// TestLoadToken_MissingFileAndMissingEnv covers the "no token at all" branch
// of loadToken (returns empty string without erroring).
func TestLoadToken_MissingFileAndMissingEnv(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)
	t.Setenv("FAAS_TOKEN", "")
	if got := loadToken(); got != "" {
		t.Errorf("loadToken = %q, want empty", got)
	}
}
