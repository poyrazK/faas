package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

func TestAutovacuumConfigKeepsNewestRevision(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	if err := os.Mkdir(data, 0o700); err != nil {
		t.Fatal(err)
	}
	mainConfig := filepath.Join(data, "postgresql.conf")
	const original = "autovacuum_naptime = 5"
	if err := os.WriteFile(mainConfig, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	c := autovacuumConfig{pgData: data, file: filepath.Join(root, "gregale-autovacuum.conf"), uid: os.Getuid(), gid: os.Getgid()}
	highChurn, _ := dbguest.AutovacuumHighChurn.Settings()
	defaults, _ := dbguest.AutovacuumDefault.Settings()
	if err := c.write(highChurn, 1); err != nil {
		t.Fatal(err)
	}
	if err := c.write(defaults, 2); err != nil {
		t.Fatal(err)
	}
	if err := c.write(highChurn, 1); err == nil {
		t.Fatal("stale revision overwrote the reset")
	}
	overrides, err := os.ReadFile(c.file)
	if err != nil || !strings.Contains(string(overrides), "revision 2\n") || !strings.Contains(string(overrides), "autovacuum_vacuum_scale_factor = 0.2\n") {
		t.Fatalf("managed profile file: %q, %v", overrides, err)
	}
	contents, err := os.ReadFile(mainConfig)
	if err != nil || !strings.HasPrefix(string(contents), original+"\n") ||
		strings.Count(string(contents), "include_if_exists = '../gregale-autovacuum.conf'\n") != 1 {
		t.Fatalf("postgresql.conf: %q, %v", contents, err)
	}
}
