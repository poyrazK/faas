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
