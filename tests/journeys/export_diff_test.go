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

// GA row 156: local inventory comparisons drive CI without API requests.
func TestExportDiffIsolatedCLI(t *testing.T) {
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
	for _, mode := range []string{"json", "human", "env", "unchanged", "changed-number", "fail", "last-false", "invalid", "duplicate", "help"} {
		t.Run(mode, func(t *testing.T) {
			tg := &target{api: server.URL, cli: cli, home: t.TempDir(), appsDomain: "apps.example.test"}
			beforePath, afterPath := filepath.Join(tg.home, "before.jsonl"), filepath.Join(tg.home, "after.jsonl")
			before := `{"id":"same","n":1}` + "\n" + `{"id":"gone","private":"before-value"}` + "\n"
			after := `{"private":"after-value","id":"new"}` + "\n" + `{"n":1.00,"id":"same"}` + "\n"
			want := 0
			extra := []string{}
			switch mode {
			case "unchanged":
				after = `{"private":"before-value","id":"gone"}` + "\n" + `{"n":1e0,"id":"same"}` + "\n"
				extra = []string{"--fail-on-change"}
			case "changed-number":
				before = `{"id":"same","n":9007199254740993}`
				after = `{"n":9007199254740994,"id":"same"}`
				extra = []string{"--fail-on-change"}
				want = 1
			case "fail":
				extra = []string{"--fail-on-change"}
				want = 1
			case "last-false":
				extra = []string{"--fail-on-change", "--fail-on-change=false"}
			case "invalid":
				after = "{\"id\":\"valid-first\"}\n{\"secret\":credential-secret}\n"
				want = 1
			case "duplicate":
				after = "{\"id\":\"credential-secret\"}\n{\"id\":\"credential-secret\"}\n"
				want = 1
			}
			for path, data := range map[string]string{beforePath: before, afterPath: after} {
				if err := os.WriteFile(path, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			args := append([]string{"--json", "export", "diff", beforePath, afterPath, "--key", "id"}, extra...)
			if mode == "human" || mode == "env" {
				args = args[1:]
			}
			if mode == "help" {
				args = []string{"--json", "help", "export", "diff"}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, cli, args...)
			cmd.Env, cmd.Dir = tg.cliEnv(""), tg.home
			cmd.Env = append(cmd.Env, "FAAS_JSON=0")
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
			if code != want || calls.Load() != 0 {
				t.Fatalf("exit=%d calls=%d out=%s err=%s", code, calls.Load(), out.String(), stderr.String())
			}
			if strings.Contains(out.String()+stderr.String(), "before-value") || strings.Contains(out.String()+stderr.String(), "after-value") || strings.Contains(out.String()+stderr.String(), "credential-secret") {
				t.Fatal("row or rejected key leaked")
			}
			switch mode {
			case "invalid", "duplicate":
				var problem map[string]json.RawMessage
				if out.Len() != 0 || json.Unmarshal(stderr.Bytes(), &problem) != nil || string(problem["exit_code"]) != "1" || problem["pagination"] != nil || problem["cli"] != nil {
					t.Fatalf("out=%s err=%s", out.String(), stderr.String())
				}
				if !strings.Contains(stderr.String(), "after export line 2") {
					t.Fatal(stderr.String())
				}
			case "human":
				if stderr.Len() != 0 || !strings.HasPrefix(out.String(), "Export diff: added=1 removed=1 changed=0 unchanged=1\n") {
					t.Fatalf("out=%s err=%s", out.String(), stderr.String())
				}
			case "help":
				if stderr.Len() != 0 || !strings.Contains(out.String(), "fail-on-change") {
					t.Fatalf("out=%s err=%s", out.String(), stderr.String())
				}
			default:
				var result struct {
					SchemaVersion           int  `json:"schema_version"`
					HasChanges              bool `json:"has_changes"`
					Summary                 struct{ Added, Removed, Changed, Unchanged int }
					Added, Removed, Changed []json.RawMessage
				}
				if stderr.Len() != 0 || json.Unmarshal(out.Bytes(), &result) != nil || result.SchemaVersion != 1 || result.HasChanges != (mode != "unchanged") {
					t.Fatalf("out=%s err=%s", out.String(), stderr.String())
				}
				if mode == "changed-number" {
					if result.Summary.Changed != 1 || len(result.Changed) != 1 || string(result.Changed[0]) != `"same"` {
						t.Fatal(out.String())
					}
				} else if mode != "unchanged" && (result.Summary.Added != 1 || result.Summary.Removed != 1 || result.Summary.Unchanged != 1) {
					t.Fatal(out.String())
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
