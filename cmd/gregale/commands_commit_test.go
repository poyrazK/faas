package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestCommitConnectionDoesNotExposeCredentials(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, "", http.StatusNoContent)
	stdout, restore := swapStdout(t)
	defer restore()
	file := filepath.Join(t.TempDir(), "connection")
	secret := "postgres://relay:secret-password@db.example/customer?sslmode=verify-full"
	if err := os.WriteFile(file, []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	source := uuid.NewString()
	if code := cmdCommit([]string{"connection", source, "--file", file}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if f.sawMethod != http.MethodPut || f.sawPath != "/v1/commit-sources/"+source+"/connection" {
		t.Fatalf("route=%s %s", f.sawMethod, f.sawPath)
	}
	if strings.Contains(stdout.String(), "secret-password") || strings.Contains(stdout.String(), "postgres://") {
		t.Fatal("credential leaked to output")
	}
	if err := os.WriteFile(file, []byte(strings.Repeat("x", 8193)), 0600); err != nil {
		t.Fatal(err)
	}
	if code := cmdCommit([]string{"connection", source, "--file", file}); code == 0 {
		t.Fatal("oversized credential file accepted")
	}
}

func TestCommitConnectionRejectsSymlinkBeforeUpload(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, "", http.StatusNoContent)
	dir := t.TempDir()
	secretFile := filepath.Join(dir, "secret")
	if err := os.WriteFile(secretFile, []byte("postgres://relay:private-password@db.example/customer?sslmode=verify-full"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "connection")
	if err := os.Symlink(secretFile, link); err != nil {
		t.Fatal(err)
	}
	if code := cmdCommit([]string{"connection", uuid.NewString(), "--file", link}); code == 0 {
		t.Fatal("symlink credential file accepted")
	}
	if f.sawMethod != "" {
		t.Fatalf("credential upload reached API: %s %s", f.sawMethod, f.sawPath)
	}
}
