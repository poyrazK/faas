package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDataAPIApprovalFixture(t *testing.T, path string, before, after []byte, changes []dataAPIApprovedChange) {
	t.Helper()
	previous, err := parseDataAPIContract(before)
	if err != nil {
		t.Fatal(err)
	}
	current, err := parseDataAPIContract(after)
	if err != nil {
		t.Fatal(err)
	}
	content, err := json.Marshal(dataAPIApproval{1, previous.Fingerprint, current.Fingerprint, changes})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDataAPIApprovalChecksOnlyWaiveNamedBreaks(t *testing.T) {
	resetJSONOut(t)
	jsonOutput = true
	var output bytes.Buffer
	previous := osStdout
	osStdout = &output
	t.Cleanup(func() { osStdout = previous })
	root := t.TempDir()
	baseline, currentPath, approval := filepath.Join(root, "before.json"), filepath.Join(root, "after.json"), filepath.Join(root, "approval.json")
	before := dataAPIContractFixture(t, dataAPIDiffFixture())
	after := dataAPIDiffFixture()
	after.Tables[0].Columns[0].Type = "number"
	after.Functions = []dataAPIFunction{}
	current := dataAPIContractFixture(t, after)
	for path, content := range map[string][]byte{baseline: before, currentPath: current} {
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	changes := []dataAPIApprovedChange{{"api.notes.body", "field type changed; update readers and writers", "All readers and writers migrate together."}}
	for _, checkFlag := range []string{"--check", "--check-compatible"} {
		for _, all := range []bool{false, true} {
			selected := append([]dataAPIApprovedChange{}, changes...)
			if all {
				selected = append(selected, dataAPIApprovedChange{"api.echo", "RPC removed; update calls", "The client no longer calls echo."})
			}
			writeDataAPIApprovalFixture(t, approval, before, current, selected)
			output.Reset()
			code := cmdDataAPI([]string{"diff", "--baseline", baseline, "--current", currentPath, "--approval", approval, checkFlag})
			if (code == 0) != all {
				t.Fatalf("%s all=%v exit=%d", checkFlag, all, code)
			}
			var report struct {
				Breaking, Approved, Unapproved int
				Changes                        []dataAPIChange
			}
			if err := json.Unmarshal(output.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if report.Breaking != 2 || report.Approved != len(selected) || report.Unapproved != 2-len(selected) {
				t.Fatalf("report: %s", output.String())
			}
			for _, change := range report.Changes {
				if !change.Breaking {
					t.Fatal("approval hid a breaking change")
				}
			}
		}
	}
	// The file is tied to the candidate even if the named changes still exist.
	after.Tables[0].Columns = append(after.Tables[0].Columns, dataAPIColumn{Name: "label", Type: "string", Optional: true})
	if err := os.WriteFile(currentPath, dataAPIContractFixture(t, after), 0o600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if code := cmdDataAPI([]string{"diff", "--baseline", baseline, "--current", currentPath, "--approval", approval, "--check-compatible"}); code == 0 || output.Len() != 0 {
		t.Fatal("stale approval accepted or emitted a successful report")
	}
}

func TestDataAPIApprovalRejectsMalformedAndUnmatchedChanges(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "approval.json")
	before := dataAPIContractFixture(t, dataAPIDiffFixture())
	after := dataAPIDiffFixture()
	after.Tables[0].Columns[0].Type = "number"
	current := dataAPIContractFixture(t, after)
	change := dataAPIApprovedChange{"api.notes.body", "field type changed; update readers and writers", "Coordinated client upgrade."}
	writeDataAPIApprovalFixture(t, path, before, current, []dataAPIApprovedChange{change})
	valid, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range [][]byte{
		[]byte(`null`), []byte(`{}`), append(append([]byte{}, valid...), []byte(` {}`)...),
		bytes.Replace(valid, []byte(`"version":1`), []byte(`"version":2`), 1),
		bytes.Replace(valid, []byte(`"rationale":"Coordinated client upgrade."`), []byte(`"rationale":" "`), 1),
		bytes.Replace(valid, []byte(`"version":1`), []byte(`"version":1,"unknown":true`), 1),
		bytes.Replace(valid, []byte(`"baseline_fingerprint":"`), []byte(`"baseline_fingerprint":"z`), 1),
	} {
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readDataAPIApprovalFile(path); err == nil {
			t.Fatalf("accepted malformed approval: %s", content)
		}
	}
	writeDataAPIApprovalFixture(t, path, before, current, []dataAPIApprovedChange{change, change})
	if _, err := readDataAPIApprovalFile(path); err == nil {
		t.Fatal("accepted duplicate approvals")
	}
	for _, named := range []dataAPIApprovedChange{
		{"api.missing", change.Reason, change.Rationale},
		{change.Path, "outdated reason", change.Rationale},
	} {
		writeDataAPIApprovalFixture(t, path, before, current, []dataAPIApprovedChange{named})
		approval, err := readDataAPIApprovalFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := applyDataAPIApproval(approval, approval.BaselineFingerprint, approval.CurrentFingerprint, diffDataAPISnapshots(dataAPIDiffFixture(), after)); err == nil {
			t.Fatal("accepted an unmatched approval")
		}
	}
}

func TestDataAPIApprovalPreflightNeverStartsTask(t *testing.T) {
	resetJSONOut(t)
	root := t.TempDir()
	baseline, approval := filepath.Join(root, "before.json"), filepath.Join(root, "approval.json")
	before := dataAPIContractFixture(t, dataAPIDiffFixture())
	if err := os.WriteFile(baseline, before, 0o600); err != nil {
		t.Fatal(err)
	}
	f := authedFakeAPI(t, `{}`, http.StatusInternalServerError)
	if code := cmdDataAPI([]string{"diff", "notes", "--baseline", baseline, "--approval", approval, "--check"}); code == 0 || f.sawMethod != "" {
		t.Fatal("missing approval started a remote task")
	}
	writeDataAPIApprovalFixture(t, approval, before, before, []dataAPIApprovedChange{{"api.notes", "relation removed; update queries and generated types", "Retired."}})
	parsed, err := readDataAPIApprovalFile(approval)
	if err != nil {
		t.Fatal(err)
	}
	parsed.BaselineFingerprint = strings.Repeat("0", 64)
	content, err := json.Marshal(parsed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(approval, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if code := cmdDataAPI([]string{"diff", "notes", "--baseline", baseline, "--approval", approval}); code == 0 || f.sawMethod != "" {
		t.Fatal("invalid approval started a remote task")
	}
}
