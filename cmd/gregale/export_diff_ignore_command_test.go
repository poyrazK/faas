package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func TestExportDiffIgnoreCLI(t *testing.T) {
	for _, mode := range []string{"json", "explain", "human", "env", "custom-key", "last-key", "literal-separator", "records", "empty", "omitted", "invalid-after", "array-path", "help"} {
		t.Run(mode, func(t *testing.T) {
			calls := setupExportVerificationCLI(t)
			before, after := `{"id":"row","updated_at":"before-secret","meta":{"counter":1},"status":"ready"}`, `{"status":"ready","meta":{"counter":2},"updated_at":"after-secret","id":"row"}`
			left, right := filepath.Join(t.TempDir(), "before.jsonl"), filepath.Join(t.TempDir(), "after.jsonl")
			flags := []string{"--ignore-path", "/updated_at", "--ignore-path=/meta/counter", "--ignore-path", "/updated_at", "--fail-on-change"}
			want, changed, explained := 0, false, false
			switch mode {
			case "explain", "human", "env":
				after = strings.Replace(after, `"status":"ready"`, `"status":"failed"`, 1)
				flags = append(flags, "--explain")
				want, changed, explained = 1, true, true
			case "custom-key":
				before = strings.Replace(before, `"id":"row"`, `"actual.id":"row","id":"before-secret"`, 1)
				after = strings.Replace(after, `"id":"row"`, `"actual.id":"row","id":"after-secret"`, 1)
				flags = append(flags, "--key", "actual.id", "--ignore-path", "/id")
			case "last-key":
				flags = append(flags, "--key", "meta", "--key", "id")
			case "literal-separator":
				left, right = "-before.jsonl", "-after.jsonl"
			case "records":
				before += "\n" + `{"id":"gone","updated_at":1}`
				after += "\n" + `{"id":"new","updated_at":2}`
				want, changed = 1, true
			case "empty":
				before, after = "", ""
				flags = append(flags, "--explain")
				explained = true
			case "omitted":
				flags = []string{"--explain", "--fail-on-change"}
				want, changed, explained = 1, true, true
			case "invalid-after":
				after += "\n" + `{"id":"bad","meta":{"counter":1,"counter":2}}`
				want = 1
			case "array-path":
				before = `{"id":"row","meta":["before-secret"]}`
				after = `{"id":"row","meta":["after-secret"]}`
				flags = []string{"--ignore-path", "/meta/0"}
				want = 1
			}
			for path, data := range map[string]string{left: before, right: after} {
				if err := os.WriteFile(path, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			args := append([]string{"--json", "export", "diff"}, flags...)
			if mode == "literal-separator" {
				args = append(args, "--")
			}
			args = append(args, left, right)
			if mode == "human" || mode == "env" {
				args = args[1:]
			}
			if mode == "env" {
				t.Setenv("FAAS_JSON", "1")
			}
			if mode == "json" {
				args = append([]string{"--profile", "missing-offline-profile"}, args...)
				t.Setenv("FAAS_API", "not-an-api-address")
			}
			if mode == "help" {
				args = []string{"--json", "help", "export", "diff"}
			}
			code, out, stderr := runExportVerificationCLI(t, args)
			if code != want || calls.Load() != 0 || strings.Contains(out+stderr, "secret") {
				t.Fatalf("exit=%d calls=%d out=%s err=%s", code, calls.Load(), out, stderr)
			}
			switch mode {
			case "invalid-after", "array-path":
				var problem map[string]json.RawMessage
				if out != "" || json.Unmarshal([]byte(stderr), &problem) != nil || string(problem["exit_code"]) != "1" || problem["cli"] != nil || problem["pagination"] != nil {
					t.Fatal(out, stderr)
				}
				if mode == "invalid-after" && !strings.Contains(stderr, "after export line 2") {
					t.Fatal(stderr)
				}
			case "human":
				if stderr != "" || !strings.Contains(out, `Ignored paths: ["/meta/counter","/updated_at"]`) || !strings.Contains(out, `changed: "/status"`) {
					t.Fatal(out, stderr)
				}
			case "help":
				if stderr != "" || !strings.Contains(out, "ignore-path") || !strings.Contains(out, "repeatable") {
					t.Fatal(out, stderr)
				}
			default:
				var report main.ExportDiffReport
				if stderr != "" || json.Unmarshal([]byte(out), &report) != nil || report.HasChanges != changed || (report.Explanations != nil) != explained {
					t.Fatal(out, stderr)
				}
				if mode == "omitted" {
					if report.IgnoredPaths != nil || strings.Contains(out, "ignored_paths") {
						t.Fatal(out)
					}
				} else {
					expected := []string{"/meta/counter", "/updated_at"}
					if mode == "custom-key" {
						expected = []string{"/id", "/meta/counter", "/updated_at"}
					}
					if !reflect.DeepEqual(report.IgnoredPaths, expected) {
						t.Fatal(out)
					}
				}
				if mode == "explain" || mode == "env" {
					entries := *report.Explanations
					if len(entries) != 1 || len(entries[0].Changes) != 1 || entries[0].Changes[0].Path != "/status" {
						t.Fatal(out)
					}
				} else if mode == "records" && (report.Summary.Added != 1 || report.Summary.Removed != 1 || report.Summary.Changed != 0 || report.Summary.Unchanged != 1) {
					t.Fatal(out)
				}
			}
			for path, data := range map[string]string{left: before, right: after} {
				actual, err := os.ReadFile(path)
				if err != nil || string(actual) != data {
					t.Fatal("input modified", err)
				}
			}
		})
	}
}

func TestExportDiffIgnoreCLIRejectsPoliciesBeforeReads(t *testing.T) {
	for _, flags := range [][]string{
		{"--ignore-path="}, {"--ignore-path", "/id"}, {"--ignore-path", "/id/child"}, {"--ignore-path", "/bad~2credential-secret"},
		{"--ignore-path", "#/time"}, {"--ignore-path", "/\xff"}, {"--key", "actual.id", "--ignore-path", "/actual.id"},
	} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			calls := setupExportVerificationCLI(t)
			args := append([]string{"--json", "export", "diff", "missing-before", "missing-after"}, flags...)
			code, out, stderr := runExportVerificationCLI(t, args)
			if code != 1 || out != "" || stderr == "" || calls.Load() != 0 || strings.Contains(stderr, "missing-before") || strings.Contains(stderr, "credential-secret") {
				t.Fatalf("exit=%d out=%s err=%s", code, out, stderr)
			}
		})
	}
}
