package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestHostAgeRetirementGuard(t *testing.T) {
	for _, mode := range []string{"referenced", "database_failure", "dry_run", "retired"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			id, _ := age.GenerateX25519Identity()
			path := filepath.Join(dir, "host.age.previous")
			if err := secretbox.WriteHostKeyAtPath(path, id); err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-31 * 24 * time.Hour)
			if err := os.Chtimes(path, old, old); err != nil {
				t.Fatal(err)
			}
			called := false
			check := func(_ string, dry bool) error {
				called = true
				if mode == "referenced" || mode == "database_failure" {
					return state.ErrConflict
				}
				if dry != (mode == "dry_run") {
					t.Fatal("dry run attempted retirement")
				}
				return nil
			}
			report := guardedHostAgePruneTo(dir, 30, true, false, mode == "dry_run", check)
			if !called {
				t.Fatal("force bypassed encrypted dependency check")
			}
			_, err := os.Stat(path)
			if mode == "retired" {
				if !report.Applied || !errors.Is(err, os.ErrNotExist) {
					t.Fatal("verified key was not pruned")
				}
			} else if err != nil || report.Applied {
				t.Fatal("unverified key was removed")
			}
			if mode == "referenced" || mode == "database_failure" {
				if report.Decision != "refused-secret-dependencies" || report.WouldApply {
					t.Fatal("failed check reported safe retirement")
				}
			}
		})
	}
}

func TestFleetSealReleaseKeyRetirementRejectsCurrentIdentity(t *testing.T) {
	dir := t.TempDir()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "fleet.age")
	if err := secretbox.WriteHostKeyAtPath(path, id); err != nil {
		t.Fatal(err)
	}
	err = runFleetSealRetireReleaseKey([]string{"--kid", id.Recipient().String(), "--current-key", path, "--dry-run"})
	if err == nil || !strings.Contains(err.Error(), "current encryption key") {
		t.Fatal("current fleet identity was eligible for retirement")
	}
	err = runFleetSealRetireReleaseKey([]string{"--kid", "invalid-private-marker", "--current-key", path})
	if err == nil || strings.Contains(err.Error(), "invalid-private-marker") {
		t.Fatal("malformed recipient was accepted or echoed")
	}
}

func TestHostAgeRetirementRefusesBrokenFleetOverlap(t *testing.T) {
	dir := t.TempDir()
	fleet, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	host, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	for path, id := range map[string]*age.X25519Identity{"fleet.age": fleet, "fleet.age.previous": host, "host.age": host, "host.age.previous": fleet} {
		if err := secretbox.WriteHostKeyAtPath(filepath.Join(dir, path), id); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(dir, "fleet.age.previous"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = checkHostAgeSecretRetirement(dir, true)
	if err == nil || !strings.Contains(err.Error(), "identify the current encryption key") {
		t.Fatal("broken fleet identity was replaced by a legacy host guess during retirement")
	}
}
