package main_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// TestDoctorReportsADirectoryWithoutSource is Hunt 14 H14-61: doctor on an
// empty directory passed every local check and said "No local findings",
// although gregale deploy refuses it.
func TestDoctorReportsADirectoryWithoutSource(t *testing.T) {
	empty := t.TempDir()
	stdout, stderr, restore := main.SwapIO(t)
	code := main.Run([]string{"doctor", empty})
	restore()
	var out bytes.Buffer
	out.WriteString(stdout.String())
	if code != 1 {
		t.Fatalf("doctor on an empty dir: exit %d, want 1; stderr=%q", code, stderr())
	}
	if !strings.Contains(out.String(), "source") || !strings.Contains(out.String(), "No app or function source was found") {
		t.Fatalf("doctor on an empty dir does not report the missing source:\n%s", out.String())
	}
	if strings.Contains(out.String(), "No local findings") {
		t.Fatalf("doctor still reports no findings for an empty dir:\n%s", out.String())
	}

	app := t.TempDir()
	if err := os.WriteFile(filepath.Join(app, "package.json"), []byte(`{"name":"x","scripts":{"start":"node index.js"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, _, restore = main.SwapIO(t)
	_ = main.Run([]string{"doctor", app})
	restore()
	if strings.Contains(stdout.String(), "No app or function source was found") {
		t.Fatalf("doctor flagged a Node app as having no source:\n%s", stdout.String())
	}
}
