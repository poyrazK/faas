//go:build linux

package main

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func testSecretFileWriter(t *testing.T, files ...api.SecretFile) *secretFileWriter {
	t.Helper()
	return &secretFileWriter{
		dir: filepath.Join(t.TempDir(), "secrets"), files: files, uid: os.Getuid(), gid: os.Getgid(),
		mount: func(dir string) error { return os.MkdirAll(dir, 0o755) },
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func readSecretFile(t *testing.T, w *secretFileWriter, name string) (string, os.FileMode) {
	t.Helper()
	path := filepath.Join(w.dir, name)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body), info.Mode().Perm()
}

func TestSecretFileWriter(t *testing.T) {
	w := testSecretFileWriter(t,
		api.SecretFile{Secret: "DB_PASSWORD", Name: "db_password"},
		api.SecretFile{Secret: "API_KEY", Name: "key.txt", Mode: 0o400})
	if err := w.write(map[string]string{"DB_PASSWORD": "hunter2", "API_KEY": "k1", "OTHER": "unused"}); err != nil {
		t.Fatal(err)
	}
	if value, mode := readSecretFile(t, w, "db_password"); value != "hunter2" || mode != 0o444 {
		t.Fatalf("db_password = %q %#o, want hunter2 0444", value, mode)
	}
	if value, mode := readSecretFile(t, w, "key.txt"); value != "k1" || mode != 0o400 {
		t.Fatalf("key.txt = %q %#o, want k1 0400", value, mode)
	}
	entries, err := os.ReadDir(w.dir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("directory holds %v (%v); want only the two files", entries, err)
	}

	// A reload replaces values and removes the file of a deleted secret.
	if err := w.write(map[string]string{"DB_PASSWORD": "rotated"}); err != nil {
		t.Fatal(err)
	}
	if value, _ := readSecretFile(t, w, "db_password"); value != "rotated" {
		t.Fatalf("db_password = %q after rotation", value)
	}
	if _, err := os.Stat(filepath.Join(w.dir, "key.txt")); !os.IsNotExist(err) {
		t.Fatalf("file of a deleted secret remains: %v", err)
	}
}

func TestSecretFileWriterNilAndMountFailure(t *testing.T) {
	var none *secretFileWriter
	if err := none.write(map[string]string{"A": "b"}); err != nil {
		t.Fatalf("nil writer: %v", err)
	}
	w := testSecretFileWriter(t, api.SecretFile{Secret: "A", Name: "a"})
	w.mount = func(string) error { return errors.New("no tmpfs") }
	if err := w.write(map[string]string{"A": "b"}); err == nil || !strings.Contains(err.Error(), "no tmpfs") {
		t.Fatalf("err = %v, want the mount failure", err)
	}
}

func TestNewSecretFileWriterWithoutFiles(t *testing.T) {
	w, err := newSecretFileWriter("", api.AppManifest{Entrypoint: []string{"/app"}}, nil)
	if err != nil || w != nil {
		t.Fatalf("writer = %+v, %v; want none", w, err)
	}
}

func TestRuntimeSecretProjectionRewritesSecretFiles(t *testing.T) {
	s, _ := runtimeSecretSnapshotFixture(t)
	s.files = testSecretFileWriter(t, api.SecretFile{Secret: "TOKEN", Name: "token"})
	if err := s.publishProjection(map[string]string{"DATABASE_URL": "old", "TOKEN": "new"}, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	if value, _ := readSecretFile(t, s.files, "token"); value != "new" {
		t.Fatalf("token = %q, want new", value)
	}
	// A failed file write leaves the published generation where it was, so
	// the reloader retries both.
	s.files.mount = func(string) error { return errors.New("no tmpfs") }
	if err := s.publishProjection(map[string]string{"TOKEN": "newer"}, strings.Repeat("c", 64)); err == nil {
		t.Fatal("publish succeeded without its files")
	}
	if got := s.snapshot()["TOKEN"]; got != "new" {
		t.Fatalf("snapshot advanced to %q after a failed file write", got)
	}
}
