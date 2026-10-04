// adr: 473
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigResourceJournal(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil || cfg.ResourceJournalDir != "/var/lib/faas/vmmd-resources" {
		t.Fatalf("default resource journal: %+v, %v", cfg, err)
	}
	path := filepath.Join(t.TempDir(), "vmmd.toml")
	if err := os.WriteFile(path, []byte("resource_journal_dir = '/var/lib/private-vmmd-resources'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadConfig(path)
	if err != nil || cfg.ResourceJournalDir != "/var/lib/private-vmmd-resources" {
		t.Fatalf("journal TOML override: %+v, %v", cfg, err)
	}
}
