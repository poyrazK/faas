package main_test

import (
	"bytes"
	"encoding/json"
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

// TestDoctorDoesNotScanADirectoryWithoutSource: doctor run from a home
// directory walked every folder under it for each source check and stalled
// on macOS-protected folders in ~/Library. With no deployable source there is
// nothing to scan, so the source checks are skipped.
func TestDoctorDoesNotScanADirectoryWithoutSource(t *testing.T) {
	home := t.TempDir()
	nested := filepath.Join(home, "Library", "Containers", "c", "Data")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "server.js"), []byte("app.listen(3000, '127.0.0.1')\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, restore := main.SwapIO(t)
	code := main.Run([]string{"doctor", "--json", home})
	restore()
	if code != 1 {
		t.Fatalf("exit %d, want 1; stderr=%q", code, stderr())
	}
	var report struct {
		Checks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Code   string `json:"code"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("doctor --json: %v\n%s", err, stdout.String())
	}
	if len(report.Checks) == 0 || report.Checks[0].Code != "no_deployable_source" {
		t.Fatalf("first check is not the missing source: %+v", report.Checks)
	}
	for _, check := range report.Checks[1:] {
		if check.Status != "skipped" {
			t.Fatalf("check %s = %s; a directory without source must not be scanned", check.Name, check.Status)
		}
	}
}
