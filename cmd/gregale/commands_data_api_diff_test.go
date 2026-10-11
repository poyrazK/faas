package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func dataAPIDiffFixture() dataAPISnapshot {
	return dataAPISnapshot{Version: 1, Schemas: []string{"api"}, Enums: map[string][]string{}, Tables: []dataAPITable{{Schema: "api", Name: "notes", Columns: []dataAPIColumn{{Name: "body", Type: "string", Insertable: true, Updatable: true}}, Relationships: json.RawMessage(`[]`)}}, Functions: []dataAPIFunction{{Schema: "api", Name: "echo", Args: []dataAPIArgument{{Name: "value", Type: "string", Optional: true}}, Returns: "string | null"}}}
}
func dataAPIContractFixture(t *testing.T, snapshot dataAPISnapshot) []byte {
	t.Helper()
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	content, err := json.Marshal(dataAPIContract{Version: 1, Fingerprint: fmt.Sprintf("%x", sha256.Sum256(raw)), Snapshot: raw})
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func TestDataAPIDiffCompatibility(t *testing.T) {
	cases := []struct {
		name     string
		modify   func(*dataAPISnapshot)
		breaking bool
	}{
		{"unchanged", func(s *dataAPISnapshot) {}, false},
		{"remove table", func(s *dataAPISnapshot) { s.Tables = []dataAPITable{} }, true},
		{"remove column", func(s *dataAPISnapshot) { s.Tables[0].Columns = []dataAPIColumn{} }, true},
		{"type change", func(s *dataAPISnapshot) { s.Tables[0].Columns[0].Type = "number" }, true},
		{"nullable read", func(s *dataAPISnapshot) { s.Tables[0].Columns[0].Nullable = true }, true},
		{"write grant removed", func(s *dataAPISnapshot) { s.Tables[0].Columns[0].Insertable = false }, true},
		{"required new field", func(s *dataAPISnapshot) {
			s.Tables[0].Columns = append(s.Tables[0].Columns, dataAPIColumn{Name: "priority", Type: "number", Insertable: true})
		}, true},
		{"default new field", func(s *dataAPISnapshot) {
			s.Tables[0].Columns = append(s.Tables[0].Columns, dataAPIColumn{Name: "priority", Type: "number", Insertable: true, Optional: true})
		}, false},
		{"remove RPC", func(s *dataAPISnapshot) { s.Functions = []dataAPIFunction{} }, true},
		{"RPC result", func(s *dataAPISnapshot) { s.Functions[0].Returns = "number | null" }, true},
		{"RPC removed arg", func(s *dataAPISnapshot) { s.Functions[0].Args = []dataAPIArgument{} }, true},
		{"RPC arg type", func(s *dataAPISnapshot) { s.Functions[0].Args[0].Type = "number" }, true},
		{"RPC required arg", func(s *dataAPISnapshot) { s.Functions[0].Args[0].Optional = false }, true},
		{"RPC new optional arg", func(s *dataAPISnapshot) {
			s.Functions[0].Args = append(s.Functions[0].Args, dataAPIArgument{Name: "mode", Type: "string", Optional: true})
		}, false},
		{"RPC new required arg", func(s *dataAPISnapshot) {
			s.Functions[0].Args = append(s.Functions[0].Args, dataAPIArgument{Name: "mode", Type: "string"})
		}, true},
		{"relationships", func(s *dataAPISnapshot) { s.Tables[0].Relationships = json.RawMessage(`[{"foreignKeyName":"fk"}]`) }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := dataAPIDiffFixture()
			after := dataAPIDiffFixture()
			tc.modify(&after)
			changes := diffDataAPISnapshots(before, after)
			breaking := false
			for _, c := range changes {
				breaking = breaking || c.Breaking
			}
			if breaking != tc.breaking {
				t.Fatalf("changes: %+v", changes)
			}
			if tc.name != "unchanged" && len(changes) == 0 {
				t.Fatal("missing change")
			}
		})
	}
	// Removing null support breaks existing writers; making a defaulted field
	// required breaks inserts even when its PostgreSQL type stays unchanged.
	before := dataAPIDiffFixture()
	before.Tables[0].Columns[0].Nullable = true
	before.Tables[0].Columns[0].Optional = true
	changes := diffDataAPISnapshots(before, dataAPIDiffFixture())
	if len(changes) != 2 || !changes[0].Breaking || !changes[1].Breaking {
		t.Fatalf("tightened write: %+v", changes)
	}
}

func TestDataAPIDiffOfflineUsesValidatedContractsAndNeverInspectsAnApp(t *testing.T) {
	resetJSONOut(t)
	jsonOutput = true
	var output bytes.Buffer
	previous := osStdout
	osStdout = &output
	t.Cleanup(func() { osStdout = previous })
	f := authedFakeAPI(t, `{}`, http.StatusInternalServerError)
	root := t.TempDir()
	baseline, currentPath := filepath.Join(root, "before.json"), filepath.Join(root, "after.json")
	before := dataAPIDiffFixture()
	after := dataAPIDiffFixture()
	after.Tables[0].Columns[0].Type = "number"
	if err := os.WriteFile(baseline, dataAPIContractFixture(t, before), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(currentPath, dataAPIContractFixture(t, after), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, check := range []bool{false, true} {
		output.Reset()
		args := []string{"diff", "--baseline", baseline, "--current", currentPath}
		if check {
			args = append(args, "--check")
		}
		if code := cmdDataAPI(args); (code != 0) != check {
			t.Fatalf("check=%v exit=%d", check, code)
		}
		var report struct {
			Breaking int             `json:"breaking"`
			Changes  []dataAPIChange `json:"changes"`
		}
		if err := json.Unmarshal(output.Bytes(), &report); err != nil || report.Breaking != 1 || len(report.Changes) != 1 {
			t.Fatalf("report: %s, %v", output.String(), err)
		}
	}
	if err := os.WriteFile(currentPath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := cmdDataAPI([]string{"diff", "--baseline", baseline, "--current", currentPath}); code == 0 {
		t.Fatal("accepted an invalid current contract")
	}
	if code := cmdDataAPI([]string{"diff", "notes", "--baseline", baseline, "--current", currentPath}); code == 0 {
		t.Fatal("accepted an ambiguous local/remote target")
	}
	if f.sawMethod != "" {
		t.Fatal("offline comparison contacted the API")
	}
}

func TestDataAPIContractRefusesIncompleteOrUnsupportedSnapshots(t *testing.T) {
	valid := dataAPIContractFixture(t, dataAPIDiffFixture())
	if _, err := parseDataAPIContract(valid); err != nil {
		t.Fatal(err)
	}
	for _, content := range [][]byte{[]byte(`{}`), append(valid, []byte(` {}`)...), []byte(strings.Replace(string(valid), `"version":1`, `"version":2`, 1)), []byte(strings.Replace(string(valid), `"fingerprint":"`, `"fingerprint":"0`, 1)), []byte(strings.Repeat("x", api.AppTaskDefaultMaxOutputBytes+1))} {
		if _, err := parseDataAPIContract(content); err == nil {
			t.Fatal("accepted malformed baseline")
		}
	}
	snapshot := dataAPIDiffFixture()
	snapshot.Tables = append(snapshot.Tables, snapshot.Tables[0])
	if _, err := parseDataAPIContract(dataAPIContractFixture(t, snapshot)); err == nil {
		t.Fatal("accepted duplicate relation")
	}
}

func TestDataAPIDiffCheckAndSnapshotPrivateTasks(t *testing.T) {
	for _, check := range []bool{false, true} {
		t.Run(fmt.Sprint(check), func(t *testing.T) {
			resetJSONOut(t)
			jsonOutput = true
			var output bytes.Buffer
			previous := osStdout
			osStdout = &output
			t.Cleanup(func() { osStdout = previous })
			baseline := filepath.Join(t.TempDir(), "schema.json")
			if err := os.WriteFile(baseline, dataAPIContractFixture(t, dataAPIDiffFixture()), 0600); err != nil {
				t.Fatal(err)
			}
			current := dataAPIDiffFixture()
			current.Tables[0].Columns[0].Type = "number"
			response, _ := json.Marshal(api.AppTaskResponse{ID: "task", DeploymentID: "deploy", Status: api.AppTaskStatusSucceeded, StdoutTail: string(dataAPIContractFixture(t, current))})
			f := authedFakeAPI(t, string(response), http.StatusCreated)
			args := []string{"diff", "notes", "--baseline", baseline}
			if check {
				args = append(args, "--check")
			}
			if code := cmdDataAPI(args); (code != 0) != check {
				t.Fatalf("check=%v exit=%d", check, code)
			}
			var report struct {
				Breaking int             `json:"breaking"`
				Changes  []dataAPIChange `json:"changes"`
			}
			if err := json.Unmarshal(output.Bytes(), &report); err != nil || report.Breaking != 1 || len(report.Changes) != 1 {
				t.Fatalf("report %s: %v", output.String(), err)
			}
			var request api.CreateAppTaskRequest
			if err := json.Unmarshal(f.sawBody, &request); err != nil {
				t.Fatal(err)
			}
			if strings.Join(request.Command, " ") != "node /app/types.mjs --snapshot" || request.TimeoutSeconds != 60 || request.MaxOutputBytes != api.AppTaskDefaultMaxOutputBytes {
				t.Fatalf("task: %+v", request)
			}
		})
	}
	for _, truncated := range []bool{false, true} {
		t.Run("export"+fmt.Sprint(truncated), func(t *testing.T) {
			resetJSONOut(t)
			content := dataAPIContractFixture(t, dataAPIDiffFixture())
			response, _ := json.Marshal(api.AppTaskResponse{ID: "task", Status: api.AppTaskStatusSucceeded, StdoutTail: string(content), OutputTruncated: truncated})
			authedFakeAPI(t, string(response), http.StatusCreated)
			output := filepath.Join(t.TempDir(), "schema.json")
			_ = os.WriteFile(output, []byte("original"), 0600)
			code := cmdDataAPI([]string{"types", "notes", "--snapshot", "--output", output})
			if (code != 0) != truncated {
				t.Fatalf("exit %d", code)
			}
			got, _ := os.ReadFile(output)
			if truncated {
				if string(got) != "original" {
					t.Fatal("overwrote baseline")
				}
			} else if string(got) != string(content) {
				t.Fatal("wrong baseline")
			}
		})
	}
}

func TestDataAPIDiffBaselinePreflightAndCompatibleCheck(t *testing.T) {
	resetJSONOut(t)
	baseline := filepath.Join(t.TempDir(), "schema.json")
	if err := os.WriteFile(baseline, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	current := dataAPIContractFixture(t, dataAPIDiffFixture())
	response, _ := json.Marshal(api.AppTaskResponse{ID: "task", Status: api.AppTaskStatusSucceeded, StdoutTail: string(current)})
	f := authedFakeAPI(t, string(response), http.StatusCreated)
	if code := cmdDataAPI([]string{"diff", "notes", "--baseline", baseline, "--check"}); code == 0 || f.sawMethod != "" {
		t.Fatal("invalid baseline started a task")
	}
	if err := os.WriteFile(baseline, current, 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	previous := osStdout
	osStdout = &output
	t.Cleanup(func() { osStdout = previous })
	jsonOutput = true
	if code := cmdDataAPI([]string{"diff", "notes", "--baseline", baseline, "--check"}); code != 0 {
		t.Fatal("compatible check failed")
	}
	var report struct {
		Breaking int             `json:"breaking"`
		Changes  []dataAPIChange `json:"changes"`
	}
	if err := json.Unmarshal(output.Bytes(), &report); err != nil || report.Breaking != 0 || report.Changes == nil || len(report.Changes) != 0 {
		t.Fatalf("report %s: %v", output.String(), err)
	}
}

func TestDataAPIContractMissingBooleansAreRejected(t *testing.T) {
	raw, _ := json.Marshal(dataAPIDiffFixture())
	raw = bytes.Replace(raw, []byte(`"nullable":false,`), nil, 1)
	content, _ := json.Marshal(dataAPIContract{Version: 1, Fingerprint: fmt.Sprintf("%x", sha256.Sum256(raw)), Snapshot: raw})
	if _, err := parseDataAPIContract(content); err == nil {
		t.Fatal("accepted incomplete column metadata")
	}
}

func TestDataAPIDiffRefusesSymlinkBaselineBeforeInspection(t *testing.T) {
	resetJSONOut(t)
	root := t.TempDir()
	baseline := filepath.Join(root, "schema.json")
	if err := os.WriteFile(baseline, dataAPIContractFixture(t, dataAPIDiffFixture()), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked-schema.json")
	if err := os.Symlink(baseline, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	f := authedFakeAPI(t, `{}`, http.StatusCreated)
	if code := cmdDataAPI([]string{"diff", "notes", "--baseline", link, "--check"}); code == 0 || f.sawMethod != "" {
		t.Fatal("symlink baseline was inspected")
	}
}
