package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// A malformed package.json produced package_json_invalid and
// missing_start_command profile warnings, yet `doctor --strict` and
// `deploy check --strict` exited 0 and human doctor said "No local findings"
// (CLI journey 2026-11-05, #6640). Profile warnings are findings: shown in
// both modes and failing --strict; without --strict they still exit 0.
func TestDoctorStrictCountsProfileWarnings(t *testing.T) {
	sources := map[string]map[string]string{
		"malformed package": {"package.json": "{\"name\": }\n"},
		"no start command":  {"package.json": `{"name":"demo","dependencies":{"express":"^4.19.2"}}`},
		"clean": {
			"package.json": `{"name":"demo","scripts":{"start":"node index.js"}}`,
			"index.js":     "require('http').createServer((q, s) => s.end('ok')).listen(process.env.PORT || 8080)\n",
		},
	}
	wantWarning := map[string]string{"malformed package": "package_json_invalid", "no start command": "missing_start_command"}
	for name, files := range sources {
		dir := t.TempDir()
		for file, body := range files {
			if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		code := wantWarning[name]
		for _, entry := range []struct {
			name string
			run  func([]string) int
		}{{"doctor", main.CmdDoctor}, {"deploy check", main.CmdDeployCheck}} {
			for _, jsonMode := range []bool{false, true} {
				for _, strict := range []bool{false, true} {
					args := []string{"--path", dir}
					if strict {
						args = append(args, "--strict")
					}
					main.WithResetJSONOutput(t, jsonMode)
					stdout, stderr, restore := main.SwapIO(t)
					exit := entry.run(args)
					restore()
					out := stdout.String()
					label := name + "/" + entry.name + "/json=" + boolString(jsonMode) + "/strict=" + boolString(strict)
					wantExit := 0
					if strict && code != "" {
						wantExit = 1
					}
					if exit != wantExit {
						t.Errorf("%s: exit=%d, want %d; stdout=%q stderr=%q", label, exit, wantExit, out, stderr())
					}
					if code == "" {
						continue
					}
					if jsonMode {
						var report struct {
							Profile struct {
								Warnings []struct {
									Code string `json:"code"`
								} `json:"warnings"`
							} `json:"profile"`
						}
						if err := json.Unmarshal([]byte(out), &report); err != nil || !strings.Contains(out, code) {
							t.Errorf("%s: JSON report missing %s (%v): %s", label, code, err, out)
						}
						continue
					}
					if !strings.Contains(out, code) || strings.Contains(out, "No local findings") || strings.Contains(out, "All checks passed") {
						t.Errorf("%s: human report hides %s:\n%s", label, code, out)
					}
				}
			}
		}
	}
}

func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// deploy --plan showed no profile warning for the same malformed package.json
// (#6640); it now lists them in human output and as profile_warnings in JSON.
func TestDeployPlanShowsProfileWarnings(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{\"name\": }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, warnings, err := main.ResolveSimpleAppPlanWithWarnings(dir, "demo", "", "directory", true, false, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, jsonMode := range []bool{false, true} {
		var out strings.Builder
		if code := main.RenderSimpleAppPlanWithWarnings(&out, plan, warnings, jsonMode); code != 0 {
			t.Fatalf("render exit=%d", code)
		}
		if !strings.Contains(out.String(), "package_json_invalid") {
			t.Errorf("json=%t plan hides the profile warning:\n%s", jsonMode, out.String())
		}
		if jsonMode && !strings.Contains(out.String(), `"profile_warnings"`) {
			t.Errorf("JSON plan has no profile_warnings: %s", out.String())
		}
	}
}
