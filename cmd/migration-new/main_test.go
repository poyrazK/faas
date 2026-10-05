package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunCreatesTimestampMigration(t *testing.T) {
	dir := t.TempDir()
	now := func() time.Time {
		return time.Date(2026, time.September, 4, 18, 45, 12, 345_000_000, time.UTC)
	}
	if err := run([]string{"-dir", dir, "-name", "add_job_priority"}, now); err != nil {
		t.Fatalf("run: %v", err)
	}
	path := filepath.Join(dir, "20260904184512345_add_job_priority.sql")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read generated migration: %v", err)
	}
	for _, want := range []string{
		"-- filename: 20260904184512345_add_job_priority.sql",
		"-- +goose Up",
		"-- +goose Down",
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("generated migration missing %q", want)
		}
	}
}

func TestRunRejectsInvalidName(t *testing.T) {
	err := run([]string{"-dir", t.TempDir(), "-name", "Add-Column"}, time.Now)
	if err == nil {
		t.Fatal("invalid name: expected error")
	}
}

func TestRunDoesNotOverwriteCollision(t *testing.T) {
	dir := t.TempDir()
	now := func() time.Time {
		return time.Date(2026, time.September, 4, 18, 45, 12, 345_000_000, time.UTC)
	}
	args := []string{"-dir", dir, "-name", "add_job_priority"}
	if err := run(args, now); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := run(args, now); err == nil {
		t.Fatal("second run: expected collision error")
	}
}

func TestRunAfterDependencyPreservesFreshInstallOrder(t *testing.T) {
	for _, item := range []struct {
		name, dependency, allocated, want string
		now                               time.Time
	}{
		{"clock behind", "20260904184512345", "", "20260904184512346", time.Date(2026, 9, 4, 17, 0, 0, 0, time.UTC)},
		{"already allocated", "20260904184512345", "20260904184512346", "20260904184512347", time.Date(2026, 9, 4, 17, 0, 0, 0, time.UTC)},
		{"clock ahead collision", "20260904184512345", "20260904190000123", "20260904190000124", time.Date(2026, 9, 4, 19, 0, 0, 123000000, time.UTC)},
		{"day rollover", "20260904235959999", "", "20260905000000001", time.Date(2026, 9, 4, 17, 0, 0, 0, time.UTC)},
	} {
		t.Run(item.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, version := range []string{item.dependency, item.allocated} {
				if version != "" {
					if err := os.WriteFile(filepath.Join(dir, version+"_existing.sql"), []byte("existing SQL"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := run([]string{"-dir", dir, "-name", "dependent", "-after", item.dependency}, func() time.Time { return item.now }); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(dir, item.want+"_dependent.sql")); err != nil {
				t.Fatalf("dependent migration is not ordered/unique: %v", err)
			}
			original, err := os.ReadFile(filepath.Join(dir, item.dependency+"_existing.sql"))
			if err != nil || string(original) != "existing SQL" {
				t.Fatal("prerequisite was modified")
			}
		})
	}
}

func TestRunAfterRejectsUnknownOrInvalidDependency(t *testing.T) {
	for _, version := range []string{"590", "20260904184512345", "20261304184512345", "20260904184512abc", "20260903184512345"} {
		dir := t.TempDir()
		if err := run([]string{"-dir", dir, "-name", "dependent", "-after", version}, time.Now); err == nil {
			t.Fatalf("prerequisite %q accepted", version)
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Fatal("rejected prerequisite wrote a migration")
		}
	}
}
