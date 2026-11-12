// adr: 20261112160000 — neond listens on a unix socket only, reads the Neon
// admin key from a credential file, and admits only named callers.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigFromEnv(t *testing.T) {
	full := map[string]string{
		"FAAS_NEOND_LISTEN":         "unix:///run/faas/neond.sock",
		"FAAS_NEOND_ADMIN_KEY_FILE": "/run/credentials/faas-neond.service/neon-admin-key",
		"FAAS_NEOND_CALLERS":        "faas-apid, faas-ops",
	}
	for _, tc := range []struct {
		name string
		edit map[string]string
		want string
	}{
		{name: "complete"},
		{name: "tcp listen", edit: map[string]string{"FAAS_NEOND_LISTEN": "tcp://10.0.0.1:7443"}, want: "want unix"},
		{name: "no key file", edit: map[string]string{"FAAS_NEOND_ADMIN_KEY_FILE": ""}, want: "ADMIN_KEY_FILE is required"},
		{name: "no callers", edit: map[string]string{"FAAS_NEOND_CALLERS": " , "}, want: "CALLERS is required"},
		{name: "bad limit", edit: map[string]string{"FAAS_NEOND_DESTRUCTIVE_PER_HOUR": "0"}, want: "positive integer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{}
			for k, v := range full {
				env[k] = v
			}
			for k, v := range tc.edit {
				env[k] = v
			}
			cfg, err := configFromEnv(func(k string) string { return env[k] })
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("err = %v; want %q", err, tc.want)
				}
				return
			}
			if err != nil || len(cfg.callers) != 2 || cfg.callers[1] != "faas-ops" || cfg.destructivePerHour != defaultDestructivePerHour {
				t.Fatalf("cfg = %+v, %v", cfg, err)
			}
		})
	}
}

func TestReadKey(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	if key, err := readKey(write("ok", "napi_abc123\n")); err != nil || key != "napi_abc123" {
		t.Fatalf("key = %q, %v", key, err)
	}
	for _, body := range []string{"", "\n", "two words"} {
		if _, err := readKey(write("bad", body)); err == nil {
			t.Fatalf("%q accepted", body)
		}
	}
	if _, err := readKey(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing file accepted")
	}
}
