package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func TestExportDiffCLIContracts(t *testing.T) {
	for _, mode := range []string{"json", "unchanged", "fail", "false", "last-false", "last-true", "human", "env", "profile", "custom", "key-last-wins", "literal-separator", "invalid-before", "invalid-after", "duplicate", "missing-key", "help"} {
		t.Run(mode, func(t *testing.T) {
			calls := setupExportVerificationCLI(t)
			before := `{"id":"same","value":1}` + "\n" + `{"id":"gone","private":"before-value"}` + "\n"
			after := `{"id":"new","private":"after-value"}` + "\n" + `{"value":1.0,"id":"same"}` + "\n"
			beforePath, afterPath := filepath.Join(t.TempDir(), "before.jsonl"), filepath.Join(t.TempDir(), "after.jsonl")
			want := 0
			key := "id"
			extra := []string{}
			switch mode {
			case "unchanged":
				after = `{"private":"before-value","id":"gone"}` + "\n" + `{"value":1e0,"id":"same"}` + "\n"
			case "fail":
				extra = []string{"--fail-on-change"}
				want = 1
			case "false":
				extra = []string{"--fail-on-change=false"}
			case "last-false":
				extra = []string{"--fail-on-change", "--fail-on-change=false"}
			case "last-true":
				extra = []string{"--fail-on-change=false", "--fail-on-change"}
				want = 1
			case "custom":
				key = "identity.id"
				before = strings.ReplaceAll(before, `"id":`, `"identity.id":`)
				after = strings.ReplaceAll(after, `"id":`, `"identity.id":`)
			case "key-last-wins":
				extra = []string{"--key", "missing", "--key", "id"}
			case "literal-separator":
				beforePath, afterPath = "-before.jsonl", "-after.jsonl"
			case "invalid-before":
				before = "{\"secret\":credential-secret}\n"
				want = 1
			case "invalid-after":
				after = "{}\n{\"secret\":credential-secret}\n"
				want = 1
			case "duplicate":
				before = "{\"id\":\"credential-secret\"}\n{\"id\":\"credential-secret\"}\n"
				want = 1
			case "missing-key":
				after = "{}\n"
				want = 1
			}
			for path, data := range map[string]string{beforePath: before, afterPath: after} {
				if err := os.WriteFile(path, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			args := append([]string{"--json", "export", "diff", beforePath, afterPath, "--key", key}, extra...)
			if mode == "human" {
				args = args[1:]
			}
			if mode == "env" {
				t.Setenv("FAAS_JSON", "1")
				args = args[1:]
			}
			if mode == "profile" {
				args = append([]string{"--profile", "missing-offline-profile"}, args...)
				t.Setenv("FAAS_API", "not-an-api-address")
			}
			if mode == "literal-separator" {
				args = []string{"--json", "export", "diff", "--key", "id", "--", beforePath, afterPath}
			}
			if mode == "help" {
				args = []string{"--json", "help", "export", "diff"}
			}
			code, out, stderr := runExportVerificationCLI(t, args)
			if code != want || calls.Load() != 0 {
				t.Fatalf("mode=%s exit=%d calls=%d out=%s err=%s", mode, code, calls.Load(), out, stderr)
			}
			if strings.Contains(out+stderr, "before-value") || strings.Contains(out+stderr, "after-value") || strings.Contains(out+stderr, "credential-secret") {
				t.Fatal("row body or rejected key leaked")
			}
			switch mode {
			case "invalid-before", "invalid-after", "duplicate", "missing-key":
				var problem map[string]json.RawMessage
				if out != "" || json.Unmarshal([]byte(stderr), &problem) != nil || string(problem["exit_code"]) != "1" || problem["pagination"] != nil || problem["cli"] != nil {
					t.Fatalf("out=%s err=%s", out, stderr)
				}
				if mode == "invalid-after" && !strings.Contains(stderr, "after export line 2") {
					t.Fatal(stderr)
				}
			case "human":
				if stderr != "" || !strings.HasPrefix(out, "Export diff: added=1 removed=1 changed=0 unchanged=1\n") {
					t.Fatalf("out=%s err=%s", out, stderr)
				}
			case "help":
				if stderr != "" || !strings.Contains(out, "fail-on-change") || !strings.Contains(out, "key") {
					t.Fatalf("out=%s err=%s", out, stderr)
				}
			default:
				var report main.ExportDiffReport
				if stderr != "" || json.Unmarshal([]byte(out), &report) != nil || report.SchemaVersion != 1 || report.Key != key || report.HasChanges != (mode != "unchanged") {
					t.Fatalf("out=%s err=%s", out, stderr)
				}
				if mode != "unchanged" && (report.Summary.Added != 1 || report.Summary.Removed != 1 || report.Summary.Changed != 0 || report.Summary.Unchanged != 1) {
					t.Fatal(out)
				}
			}
			for path, wantData := range map[string]string{beforePath: before, afterPath: after} {
				got, err := os.ReadFile(path)
				if err != nil || string(got) != wantData {
					t.Fatal("input changed", err)
				}
			}
		})
	}
}

func TestExportDiffCLIRefusesInvalidArguments(t *testing.T) {
	for _, args := range [][]string{
		{"export", "diff"}, {"export", "diff", "before"}, {"export", "diff", "before", "after", "extra"},
		{"export", "diff", "-", "after"}, {"export", "diff", "before", "-"}, {"export", "diff", "before", "after", "--key="},
		{"export", "diff", "before", "after", "--unknown"},
		{"export", "diff", "before", "after", "--key", "\xff"},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			calls := setupExportVerificationCLI(t)
			code, out, stderr := runExportVerificationCLI(t, append([]string{"--json"}, args...))
			if code != 1 || out != "" || stderr == "" || calls.Load() != 0 {
				t.Fatalf("exit=%d calls=%d out=%s err=%s", code, calls.Load(), out, stderr)
			}
		})
	}
}

func TestExportDiffCLILargeNumericChange(t *testing.T) {
	calls := setupExportVerificationCLI(t)
	before, after := filepath.Join(t.TempDir(), "before.jsonl"), filepath.Join(t.TempDir(), "after.jsonl")
	for path, data := range map[string]string{before: `{"id":"same","value":9007199254740993}`, after: `{"value":9007199254740994,"id":"same"}`} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	code, out, stderr := runExportVerificationCLI(t, []string{"--json", "export", "diff", before, after, "--fail-on-change"})
	var report main.ExportDiffReport
	if code != 1 || stderr != "" || calls.Load() != 0 || json.Unmarshal([]byte(out), &report) != nil || report.Summary.Changed != 1 || report.Summary.Unchanged != 0 || len(report.Changed) != 1 || string(report.Changed[0]) != `"same"` {
		t.Fatalf("exit=%d out=%s err=%s", code, out, stderr)
	}
}
