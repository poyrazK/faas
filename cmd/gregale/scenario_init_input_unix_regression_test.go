//go:build unix

package main_test

import (
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
)

func TestOpenAPIScaffoldReadsBoundedRegularInput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.yaml")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 7<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	body, err := main.ReadScaffoldOpenAPIDocument(path)
	if err != nil || len(body) != (5<<20)+1 {
		t.Fatalf("read length %d, error %v", len(body), err)
	}
	main.CaptureSuiteTestOutput(t)
	output := filepath.Join(dir, "manifest.yaml")
	if code := main.CmdTestInit([]string{"--from", path, "--project", "test-app", "--output", output}); code == 0 {
		t.Fatal("oversized document accepted")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("oversized input left output: %v", err)
	}
	link := filepath.Join(dir, "link.yaml")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := main.ReadScaffoldOpenAPIDocument(link); err == nil {
		t.Fatal("symlink accepted")
	}
	fifo := filepath.Join(dir, "fifo.yaml")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := main.ReadScaffoldOpenAPIDocument(fifo); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO accepted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("FIFO with no writer blocked input validation")
	}
}
