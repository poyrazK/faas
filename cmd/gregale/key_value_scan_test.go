package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A KEY=VALUE line over the reader's cap must name the cap, not surface
// bufio's "token too long" (H10-66).
func TestReadSecretsFileNamesTheLineCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.env")
	line := "BIG=" + strings.Repeat("x", keyValueLineMaxBytes+10) + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := readSecretsFile(path)
	if err == nil {
		t.Fatal("readSecretsFile accepted an over-long line")
	}
	if msg := err.Error(); strings.Contains(msg, "token too long") || !strings.Contains(msg, "longer than 64 KiB") || !strings.Contains(msg, "32768 bytes") {
		t.Fatalf("error = %q, want the line cap and the plan value cap", msg)
	}
}
