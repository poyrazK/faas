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

// GA row 156: field paths explain saved inventory drift without field values.
func TestExportDiffExplainIsolatedCLI(t *testing.T) {
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
	for _, mode := range []string{"json", "human", "env", "unchanged", "fail", "last-false", "invalid", "help"} {
		t.Run(mode, func(t *testing.T) {
			tg := &target{api: server.URL, cli: cli, home: t.TempDir(), appsDomain: "apps.example.test"}
			left, right := filepath.Join(tg.home, "before.jsonl"), filepath.Join(tg.home, "after.jsonl")
			before := `{"id":"row","nested":{"a/b":1,"gone":{"private":"before-secret"}},"precise":9007199254740993}`
			after := `{"precise":9007199254740994,"nested":{"a/b":2,"new":{"private":"after-secret"}},"id":"row"}`
			extra, want := []string{"--explain"}, 0
			switch mode {
			case "unchanged":
				after = before
			case "fail":
				extra = append(extra, "--fail-on-change")
				want = 1
			case "last-false":
				extra = append(extra, "--explain=false")
			case "invalid":
				after += "\n{\"id\":\"rejected-secret\",\"n\":{\"a\":1,\"a\":2}}"
				want = 1
			}
			for path, data := range map[string]string{left: before, right: after} {
				if err := os.WriteFile(path, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			args := append([]string{"--json", "export", "diff", left, right}, extra...)
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
			if code != want || calls.Load() != 0 || strings.Contains(out.String()+stderr.String(), "secret") || strings.Contains(out.String(), "900719925474099") || strings.Contains(out.String(), "/private") {
				t.Fatalf("exit=%d calls=%d out=%s err=%s", code, calls.Load(), out.String(), stderr.String())
			}
			switch mode {
			case "invalid":
				var problem map[string]json.RawMessage
				if out.Len() != 0 || json.Unmarshal(stderr.Bytes(), &problem) != nil || !strings.Contains(stderr.String(), "after export line 2") || problem["cli"] != nil || problem["pagination"] != nil {
					t.Fatal(stderr.String())
				}
			case "help":
				if stderr.Len() != 0 || !strings.Contains(out.String(), "explain") {
					t.Fatal(out.String(), stderr.String())
				}
			case "human":
				if stderr.Len() != 0 || !strings.Contains(out.String(), `changed: "/nested/a~1b"`) || !strings.Contains(out.String(), `removed: "/nested/gone"`) || !strings.Contains(out.String(), `added: "/nested/new"`) {
					t.Fatal(out.String(), stderr.String())
				}
			default:
				var result struct {
					HasChanges   bool `json:"has_changes"`
					Explanations *[]struct {
						ID      string                        `json:"id"`
						Changes []struct{ Path, Kind string } `json:"changes"`
					} `json:"explanations"`
				}
				if stderr.Len() != 0 || json.Unmarshal(out.Bytes(), &result) != nil || result.HasChanges != (mode != "unchanged") || (result.Explanations == nil) != (mode == "last-false") {
					t.Fatal(out.String(), stderr.String())
				}
				if mode == "unchanged" {
					if len(*result.Explanations) != 0 || !strings.Contains(out.String(), `"explanations":[]`) {
						t.Fatal(out.String())
					}
				} else if mode != "last-false" {
					entries := *result.Explanations
					if len(entries) != 1 || entries[0].ID != "row" || len(entries[0].Changes) != 4 || entries[0].Changes[0].Path != "/nested/a~1b" || entries[0].Changes[1].Kind != "removed" || entries[0].Changes[2].Kind != "added" || entries[0].Changes[3].Path != "/precise" {
						t.Fatal(out.String())
					}
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
