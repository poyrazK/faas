package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func TestExportDiffExplainCLI(t *testing.T) {
	for _, mode := range []string{"json", "human", "env", "profile", "fail", "false", "last-false", "last-true", "unchanged", "added-only", "invalid-after", "custom", "literal-separator", "help"} {
		t.Run(mode, func(t *testing.T) {
			calls := setupExportVerificationCLI(t)
			before, after := `{"id":"row","settings":{"region":"before-secret"},"array":[1,2]}`, `{"id":"row","array":[2,1],"settings":{"region":"after-secret"}}`
			directory := t.TempDir()
			left, right := filepath.Join(directory, "before.jsonl"), filepath.Join(directory, "after.jsonl")
			extra := []string{"--explain"}
			want, explained := 0, true
			switch mode {
			case "fail":
				extra = append(extra, "--fail-on-change")
				want = 1
			case "false":
				extra = []string{"--explain=false"}
				explained = false
			case "last-false":
				extra = append(extra, "--explain=false")
				explained = false
			case "last-true":
				extra = []string{"--explain=false", "--explain"}
			case "unchanged":
				after = before
			case "added-only":
				before = ""
			case "invalid-after":
				after += "\n{\"id\":\"rejected-secret\",\"nested\":{\"a\":1,\"a\":2}}"
				want = 1
			case "custom":
				before = strings.ReplaceAll(before, `"id":`, `"literal.id":`)
				after = strings.ReplaceAll(after, `"id":`, `"literal.id":`)
				extra = append(extra, "--key", "literal.id")
			case "literal-separator":
				left, right = "-before.jsonl", "-after.jsonl"
			}
			for path, data := range map[string]string{left: before, right: after} {
				if err := os.WriteFile(path, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			args := append([]string{"--json", "export", "diff"}, extra...)
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
			if mode == "profile" {
				args = append([]string{"--profile", "missing-offline-profile"}, args...)
				t.Setenv("FAAS_API", "not-an-api-address")
			}
			if mode == "help" {
				args = []string{"--json", "help", "export", "diff"}
			}
			code, out, stderr := runExportVerificationCLI(t, args)
			if code != want || calls.Load() != 0 {
				t.Fatalf("exit=%d calls=%d out=%s err=%s", code, calls.Load(), out, stderr)
			}
			if strings.Contains(out+stderr, "secret") {
				t.Fatal("field value or rejected ID leaked", out, stderr)
			}
			switch mode {
			case "invalid-after":
				var problem map[string]json.RawMessage
				if out != "" || json.Unmarshal([]byte(stderr), &problem) != nil || !strings.Contains(stderr, "after export line 2") || problem["cli"] != nil || problem["pagination"] != nil || string(problem["exit_code"]) != "1" {
					t.Fatalf("out=%s err=%s", out, stderr)
				}
			case "help":
				if stderr != "" || !strings.Contains(out, "explain") || !strings.Contains(out, "fail-on-change") {
					t.Fatalf("out=%s err=%s", out, stderr)
				}
			case "human":
				if stderr != "" || !strings.Contains(out, "Fields for \"row\":\n  changed: \"/array\"\n  changed: \"/settings/region\"\n") {
					t.Fatalf("out=%s err=%s", out, stderr)
				}
			default:
				var report main.ExportDiffReport
				if stderr != "" || json.Unmarshal([]byte(out), &report) != nil || (report.Explanations != nil) != explained {
					t.Fatalf("out=%s err=%s", out, stderr)
				}
				if mode == "unchanged" || mode == "added-only" {
					if len(*report.Explanations) != 0 || strings.Contains(out, `"explanations":null`) || (mode == "unchanged" && report.HasChanges) || (mode == "added-only" && report.Summary.Added != 1) {
						t.Fatal(out)
					}
				} else if report.Summary.Changed != 1 || len(report.Changed) != 1 {
					t.Fatal(out)
				} else if explained {
					entries := *report.Explanations
					if len(entries) != 1 || string(entries[0].ID) != `"row"` || len(entries[0].Changes) != 2 || entries[0].Changes[0].Path != "/array" || entries[0].Changes[1].Path != "/settings/region" || entries[0].Changes[1].Kind != "changed" {
						t.Fatal(out)
					}
				}
			}
			for path, data := range map[string]string{left: before, right: after} {
				actual, err := os.ReadFile(path)
				if err != nil || string(actual) != data {
					t.Fatal("input changed", err)
				}
			}
		})
	}
}
