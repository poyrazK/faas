package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func dataAPICheckFixture(t *testing.T, after dataAPISnapshot) (string, string, string, []byte) {
	t.Helper()
	root := t.TempDir()
	baseline, current, types := filepath.Join(root, "schema.json"), filepath.Join(root, "candidate.json"), filepath.Join(root, "database.types.ts")
	beforeBytes, afterBytes := dataAPIContractFixture(t, dataAPIDiffFixture()), dataAPIContractFixture(t, after)
	for path, content := range map[string][]byte{baseline: beforeBytes, current: afterBytes} {
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	contract, err := parseDataAPIContract(afterBytes)
	if err != nil {
		t.Fatal(err)
	}
	generated, err := renderDataAPICheckTypes(context.Background(), contract)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(generated), "export const schemaFingerprint = \""+contract.Fingerprint+"\"") || !strings.Contains(string(generated), "export type Database = {") {
		t.Fatalf("invalid generated types: %s", generated)
	}
	if err := os.WriteFile(types, generated, 0600); err != nil {
		t.Fatal(err)
	}
	return baseline, current, types, generated
}

func TestDataAPICheckOfflineReportsCompatibilityAndExactTypeFreshness(t *testing.T) {
	for _, scenario := range []string{"unchanged", "additive", "additive-stale", "removed-column", "changed-type", "required-field", "missing-types", "body-edited", "oversized", "approved", "approved-stale", "partial-approval"} {
		t.Run(scenario, func(t *testing.T) {
			resetJSONOut(t)
			jsonOutput = true
			var output bytes.Buffer
			previous := osStdout
			osStdout = &output
			t.Cleanup(func() { osStdout = previous })
			// Offline checks must not need credentials, even when a breaking change fails.
			t.Setenv("FAAS_TOKEN", "")
			t.Setenv("FAAS_API", "http://127.0.0.1:1")
			after := dataAPIDiffFixture()
			switch scenario {
			case "additive", "additive-stale":
				after.Tables[0].Columns = append(after.Tables[0].Columns, dataAPIColumn{Name: "label", Type: "string", Nullable: true, Optional: true, Insertable: true, Updatable: true})
			case "removed-column":
				after.Tables[0].Columns = []dataAPIColumn{{Name: "id", Type: "number"}}
			case "changed-type", "approved", "approved-stale", "partial-approval":
				after.Tables[0].Columns[0].Type = "number"
			case "required-field":
				after.Tables[0].Columns = append(after.Tables[0].Columns, dataAPIColumn{Name: "priority", Type: "number", Insertable: true})
			}
			if scenario == "partial-approval" {
				after.Functions = []dataAPIFunction{}
			}
			baseline, current, types, original := dataAPICheckFixture(t, after)
			switch scenario {
			case "additive-stale", "approved-stale":
				old, err := readDataAPIContractFile(baseline)
				if err != nil {
					t.Fatal(err)
				}
				original, err = renderDataAPICheckTypes(context.Background(), old)
				if err != nil {
					t.Fatal(err)
				}
			case "body-edited":
				original = bytes.Replace(original, []byte(`"body": string`), []byte(`"body": number`), 1)
			case "oversized":
				original = bytes.Repeat([]byte("x"), api.AppTaskDefaultMaxOutputBytes+1)
			}
			if scenario == "missing-types" {
				if err := os.Remove(types); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(types, original, 0600); err != nil {
				t.Fatal(err)
			}
			args := []string{"check", "--current", current, "--baseline", baseline, "--types", types}
			if strings.HasPrefix(scenario, "approved") || scenario == "partial-approval" {
				approval := filepath.Join(filepath.Dir(baseline), "approval.json")
				before, err := os.ReadFile(baseline)
				if err != nil {
					t.Fatal(err)
				}
				next, err := os.ReadFile(current)
				if err != nil {
					t.Fatal(err)
				}
				writeDataAPIApprovalFixture(t, approval, before, next, []dataAPIApprovedChange{{Path: "api.notes.body", Reason: "field type changed; update readers and writers", Rationale: "client upgrade reviewed"}})
				args = append(args, "--approval", approval)
			}
			code := cmdDataAPI(args)
			expectedPass := scenario == "unchanged" || scenario == "additive" || scenario == "approved"
			if (code == 0) != expectedPass {
				t.Fatalf("exit=%d report=%s", code, output.String())
			}
			var report dataAPICheckReport
			if err := json.Unmarshal(output.Bytes(), &report); err != nil {
				t.Fatalf("invalid report %s: %v", output.String(), err)
			}
			if report.Passed != expectedPass || report.Version != 1 || report.Changes == nil {
				t.Fatalf("incorrect report: %+v", report)
			}
			if report.Drift != (report.BaselineFingerprint != report.CurrentFingerprint) {
				t.Fatal("incorrect drift")
			}
			expectedStatus := "current"
			switch scenario {
			case "additive-stale", "approved-stale", "body-edited":
				expectedStatus = "stale"
			case "missing-types":
				expectedStatus = "missing"
			case "oversized":
				expectedStatus = "too_large"
			}
			if report.Types.Status != expectedStatus || report.Types.Matches != (expectedStatus == "current") {
				t.Fatalf("incorrect types result: %+v", report.Types)
			}
			if strings.Contains(output.String(), "export type Database") || strings.Contains(output.String(), "client upgrade reviewed") {
				t.Fatal("report included file content")
			}
			if expectedStatus == "missing" {
				if _, err := os.Stat(types); !os.IsNotExist(err) {
					t.Fatal("check created types")
				}
			} else {
				actual, err := os.ReadFile(types)
				if err != nil || !bytes.Equal(actual, original) {
					t.Fatal("check rewrote types")
				}
			}
			unchanged, err := readDataAPIContractFile(baseline)
			if err != nil || unchanged.Fingerprint != report.BaselineFingerprint {
				t.Fatal("check rewrote baseline")
			}
			if scenario == "partial-approval" && (report.Approved != 1 || report.Unapproved != 1) {
				t.Fatalf("partial approval bypassed break: %+v", report)
			}
		})
	}
}

func TestDataAPICheckHostedInspectsOnceAndNeverMutatesApp(t *testing.T) {
	for _, scenario := range []string{"current", "stale", "malformed", "truncated", "task-failed", "bad-baseline", "bad-approval", "symlink-types", "deadline"} {
		t.Run(scenario, func(t *testing.T) {
			baseline, _, types, _ := dataAPICheckFixture(t, dataAPIDiffFixture())
			content := dataAPIContractFixture(t, dataAPIDiffFixture())
			if scenario == "bad-baseline" {
				if err := os.WriteFile(baseline, []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "stale" {
				if err := os.WriteFile(types, []byte("unchanged source"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "symlink-types" {
				alias := types + ".link"
				if err := os.Symlink(types, alias); err != nil {
					t.Fatal(err)
				}
				types = alias
			}
			var calls atomic.Int32
			management := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "POST" || r.URL.Path != "/v1/apps/notes/data-api/contract-tasks" {
					t.Errorf("unexpected app mutation %s %s", r.Method, r.URL.Path)
				}
				var request api.CreateDataAPIContractTaskRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				if request.Kind != api.DataAPIContractSchema {
					t.Errorf("unexpected task: %+v", request)
				}
				task := api.AppTaskResponse{ID: "inspection", DeploymentID: "revision", Status: api.AppTaskStatusSucceeded, StdoutTail: string(content)}
				if scenario == "malformed" {
					task.StdoutTail = `{}`
				}
				if scenario == "truncated" {
					task.OutputTruncated = true
				}
				if scenario == "task-failed" {
					task.Status = api.AppTaskStatusFailed
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				if err := json.NewEncoder(w).Encode(task); err != nil {
					t.Error(err)
				}
			}))
			defer management.Close()
			t.Setenv("FAAS_API", management.URL)
			t.Setenv("FAAS_TOKEN", "owner-secret-that-must-not-appear")
			stdout, stderr, restore := swapIO(t)
			defer restore()
			args := []string{"--json", "data-api", "check", "notes", "--baseline", baseline, "--types", types}
			if scenario == "bad-approval" {
				path := filepath.Join(filepath.Dir(baseline), "approval.json")
				if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--approval", path)
			}
			if scenario == "deadline" {
				args = append(args, "--timeout", "1ns")
			}
			code := run(args)
			if (code == 0) != (scenario == "current") {
				t.Fatalf("exit=%d output=%s err=%s", code, stdout.String(), stderr())
			}
			expectedCalls := int32(1)
			if scenario == "bad-baseline" || scenario == "bad-approval" || scenario == "symlink-types" || scenario == "deadline" {
				expectedCalls = 0
			}
			if calls.Load() != expectedCalls {
				t.Fatalf("tasks=%d expected=%d", calls.Load(), expectedCalls)
			}
			if scenario == "current" || scenario == "stale" {
				var report dataAPICheckReport
				if err := json.Unmarshal([]byte(stdout.String()), &report); err != nil {
					t.Fatal(err)
				}
				if report.TaskID != "inspection" || report.DeploymentID != "revision" || report.App != "notes" {
					t.Fatalf("missing provenance %+v", report)
				}
			}
			if strings.Contains(stdout.String()+stderr(), "owner-secret-that-must-not-appear") {
				t.Fatal("check leaked account credential")
			}
		})
	}
}

func TestDataAPICheckRenderDeadlineAndBoundedInput(t *testing.T) {
	contract, err := parseDataAPIContract(dataAPIContractFixture(t, dataAPIDiffFixture()))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := renderDataAPICheckTypes(ctx, contract); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	if _, err := renderDataAPICheckTypes(ctx, contract); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline: %v", err)
	}
	var b bytes.Buffer
	w := dataAPICheckOutput{writer: &b, remaining: 3}
	if _, err := w.Write([]byte("123")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("4")); err == nil || b.String() != "123" {
		t.Fatal("output bound not enforced")
	}
}

func TestDataAPICheckHelpAndInvalidTargets(t *testing.T) {
	command, ok := lookupCliCommand("data-api")
	if !ok {
		t.Fatal("data-api metadata missing")
	}
	found := false
	for _, sub := range command.Subcommands {
		if sub.Name == "check" {
			found = true
		}
	}
	if !found {
		t.Fatal("check metadata missing")
	}
	stdout, _, restore := swapIO(t)
	defer restore()
	if code := run([]string{"data-api", "check", "--help"}); code != 0 || !strings.Contains(stdout.String(), "--baseline") || !strings.Contains(stdout.String(), "--types") {
		t.Fatalf("help exit=%d %s", code, stdout.String())
	}
	for _, args := range [][]string{
		{"check"},
		{"check", "notes", "--baseline", "schema.json"},
		{"check", "notes", "--baseline", "schema.json", "--types", "types.ts", "--current", "candidate.json"},
		{"check", "--baseline", "schema.json", "--types", "types.ts"},
		{"check", "notes", "--baseline", "schema.json", "--types", "types.ts", "--timeout", "0s"},
		{"check", "notes", "--baseline", "schema.json", "--types", "types.ts", "--timeout", "2h"},
	} {
		if cmdDataAPI(args) == 0 {
			t.Fatalf("accepted invalid target: %v", args)
		}
	}
}
