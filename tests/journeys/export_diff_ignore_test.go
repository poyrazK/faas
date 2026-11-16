package journeys

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// GA row 156: explicit ignored paths make saved inventory drift checks useful.
func TestExportDiffIgnoreIsolatedCLI(t *testing.T) {
	cli := os.Getenv("GREGALE_JOURNEY_TEST_CLI")
	if tgt != nil {
		cli = tgt.cli
	}
	if cli == "" {
		t.Skip("set GREGALE_JOURNEY_TEST_CLI or configure a journey target")
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.Error(w, "must stay offline", 500) }))
	defer server.Close()
	for _, mode := range []string{"ignored-only", "remaining-change", "human", "env", "records", "invalid-policy", "invalid-input", "help"} {
		t.Run(mode, func(t *testing.T) {
			tg := &target{api: server.URL, cli: cli, home: t.TempDir(), appsDomain: "apps.example.test"}
			left, right := filepath.Join(tg.home, "before.jsonl"), filepath.Join(tg.home, "after.jsonl")
			before, after := `{"id":"row","updated_at":"before-secret","meta":{"n":9007199254740993}}`, `{"meta":{"n":9007199254740993},"updated_at":"after-secret","id":"row"}`
			flags := []string{"--ignore-path", "/updated_at", "--ignore-path", "/updated_at", "--explain", "--fail-on-change"}
			want, changed := 0, false
			switch mode {
			case "remaining-change", "human", "env":
				after = strings.Replace(after, "9007199254740993", "9007199254740994", 1)
				want, changed = 1, true
			case "records":
				after += "\n" + `{"id":"new","updated_at":"after-secret"}`
				want, changed = 1, true
			case "invalid-policy":
				flags = append(flags, "--ignore-path", "/id")
				want = 1
			case "invalid-input":
				after += "\n" + `{"id":"bad","updated_at":{"a":1,"a":2}}`
				want = 1
			}
			for path, data := range map[string]string{left: before, right: after} {
				if err := os.WriteFile(path, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			args := append([]string{"--json", "export", "diff", left, right}, flags...)
			if mode == "human" || mode == "env" {
				args = args[1:]
			}
			if mode == "help" {
				args = []string{"--json", "help", "export", "diff"}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, cli, args...)
			cmd.Env, cmd.Dir = append(tg.cliEnv(""), "FAAS_JSON=0"), tg.home
			if mode == "env" {
				cmd.Env = append(cmd.Env, "FAAS_JSON=1")
			}
			var out, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &stderr
			err := cmd.Run()
			if ctx.Err() != nil {
				t.Fatal(ctx.Err())
			}
			code := 0
			if err != nil {
				var exit *exec.ExitError
				if !errors.As(err, &exit) {
					t.Fatal(err)
				}
				code = exit.ExitCode()
			}
			if code != want || calls.Load() != 0 || strings.Contains(out.String()+stderr.String(), "secret") || strings.Contains(out.String(), "900719925474099") {
				t.Fatalf("exit=%d calls=%d out=%s err=%s", code, calls.Load(), out.String(), stderr.String())
			}
			switch mode {
			case "invalid-policy", "invalid-input":
				var problem map[string]json.RawMessage
				if out.Len() != 0 || json.Unmarshal(stderr.Bytes(), &problem) != nil || problem["cli"] != nil || problem["pagination"] != nil || string(problem["exit_code"]) != "1" {
					t.Fatal(out.String(), stderr.String())
				}
				if mode == "invalid-input" && !strings.Contains(stderr.String(), "after export line 2") {
					t.Fatal(stderr.String())
				}
			case "help":
				if stderr.Len() != 0 || !strings.Contains(out.String(), "ignore-path") {
					t.Fatal(out.String(), stderr.String())
				}
			case "human":
				if stderr.Len() != 0 || !strings.Contains(out.String(), `Ignored paths: ["/updated_at"]`) || !strings.Contains(out.String(), `changed: "/meta/n"`) {
					t.Fatal(out.String(), stderr.String())
				}
			default:
				var report struct {
					HasChanges   bool     `json:"has_changes"`
					IgnoredPaths []string `json:"ignored_paths"`
					Summary      struct{ Added, Changed, Unchanged int }
					Explanations []struct {
						ID      string
						Changes []struct{ Path, Kind string }
					} `json:"explanations"`
				}
				if stderr.Len() != 0 || json.Unmarshal(out.Bytes(), &report) != nil || report.HasChanges != changed || len(report.IgnoredPaths) != 1 || report.IgnoredPaths[0] != "/updated_at" {
					t.Fatal(out.String(), stderr.String())
				}
				if mode == "remaining-change" || mode == "env" {
					if len(report.Explanations) != 1 || report.Explanations[0].ID != "row" || len(report.Explanations[0].Changes) != 1 || report.Explanations[0].Changes[0].Path != "/meta/n" || report.Explanations[0].Changes[0].Kind != "changed" {
						t.Fatal(out.String())
					}
				} else if !strings.Contains(out.String(), `"explanations":[]`) {
					t.Fatal(out.String())
				}
				if mode == "records" && (report.Summary.Added != 1 || report.Summary.Changed != 0 || report.Summary.Unchanged != 1) {
					t.Fatal(out.String())
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
