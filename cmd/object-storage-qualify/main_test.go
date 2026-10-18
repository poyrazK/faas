//go:build linux

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/objectqualification"
)

func TestQualificationCommandFailureReport(t *testing.T) {
	root := t.TempDir()
	reportPath := filepath.Join(root, "report.json")
	var output bytes.Buffer
	err := run(t.Context(), []string{"--daemon", "/bin/true", "--ctl", "/bin/true", "--work-dir", filepath.Join(root, "new"), "--workers", "0", "--report", reportPath}, &output)
	if err == nil {
		t.Fatal("invalid workload accepted")
	}
	body, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	var report objectqualification.Report
	if json.Unmarshal(body, &report) != nil || report.Passed || report.Error == "" {
		t.Fatal("missing failure artifact", string(body))
	}
	before := append([]byte(nil), body...)
	if err = run(t.Context(), []string{"--report", reportPath}, &bytes.Buffer{}); err == nil {
		t.Fatal("existing report accepted")
	}
	after, err := os.ReadFile(reportPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("existing report changed", err)
	}
}

func TestPreflightCommandPreservesReadOnlyFailureEvidence(t *testing.T) {
	root := t.TempDir()
	directory, path := filepath.Join(root, "planned"), filepath.Join(root, "preflight.json")
	var output bytes.Buffer
	err := run(t.Context(), []string{"preflight", "--daemon", "/bin/true", "--ctl", "/bin/true", "--work-dir", directory, "--report", path}, &output)
	if err == nil {
		t.Fatal("missing follower was qualified")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report objectqualification.PreflightReport
	if json.Unmarshal(body, &report) != nil || report.Ready || report.Primary == nil || report.Error == "" || report.PhysicalFailureDomainsVerified {
		t.Fatal(string(body))
	}
	if !strings.Contains(output.String(), `"ready":false`) {
		t.Fatal(output.String())
	}
	if _, err = os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("preflight created fixture data", err)
	}
	if err = run(t.Context(), []string{"preflight", "--report", path}, &bytes.Buffer{}); err == nil {
		t.Fatal("existing report was accepted")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(body, after) {
		t.Fatal("existing report changed")
	}
}

func TestDedicatedQualificationRequiresRemoteBeforeCreatingData(t *testing.T) {
	root := t.TempDir()
	directory, path := filepath.Join(root, "planned"), filepath.Join(root, "failure.json")
	err := run(t.Context(), []string{"--require-dedicated-storage", "--recovery-retention", "--daemon", "/bin/true", "--ctl", "/bin/true", "--work-dir", directory, "--report", path}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("guarded qualification accepted no follower")
	}
	body, _ := os.ReadFile(path)
	var report objectqualification.Report
	if json.Unmarshal(body, &report) != nil || report.Passed || !report.Config.RequireDedicatedStorage || !report.Config.RecoveryRetention {
		t.Fatal(string(body))
	}
	if _, err = os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("invalid qualification created data", err)
	}
}

// adr: 1557
func TestSoakCommandDefaultsAndExplicitOverrides(t *testing.T) {
	for _, overrides := range [][]string{nil, {"--duration", "12s", "--cycles", "2", "--max-workflows", "24", "--compaction-records", "16"}} {
		root := t.TempDir()
		path := filepath.Join(root, "report.json")
		args := []string{"--soak", "--daemon", "/bin/true", "--ctl", "/bin/true", "--work-dir", filepath.Join(root, "data"), "--report", path, "--workers", "0"}
		if err := run(t.Context(), append(args, overrides...), &bytes.Buffer{}); err == nil {
			t.Fatal("invalid worker count accepted")
		}
		body, err := os.ReadFile(path)
		var report objectqualification.Report
		if err != nil || json.Unmarshal(body, &report) != nil || !report.Config.Soak {
			t.Fatal(string(body), err)
		}
		if len(overrides) == 0 {
			defaults := objectqualification.DefaultSoakOptions()
			if report.Config.Duration != defaults.Duration.String() || report.Config.MaxWorkflows != defaults.MaxWorkflows || report.Config.Cycles != defaults.Cycles || report.Config.CompactionRecords != defaults.CompactionRecords {
				t.Fatal("soak defaults omitted", report.Config)
			}
		} else if report.Config.Duration != "12s" || report.Config.MaxWorkflows != 24 || report.Config.Cycles != 2 || report.Config.CompactionRecords != 16 {
			t.Fatal("explicit soak configuration overwritten", report.Config)
		}
	}
}
