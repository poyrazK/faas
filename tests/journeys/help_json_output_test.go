package journeys

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type discoveredListOutput struct {
	SchemaVersion int `json:"schema_version"`
	Output        struct {
		Scope       string
		SuccessExit int `json:"success_exit"`
		Modes       []struct {
			Name   string
			When   map[string]bool
			Stdout struct {
				Encoding, Shape string
				ItemsField      string `json:"items_field"`
			}
			File   *struct{ Encoding string }
			Errors struct {
				ReadErrorStdout string `json:"read_error_stdout"`
				ProgressField   string `json:"progress_field"`
			}
		}
		Errors struct {
			ServerErrorExit int    `json:"server_error_exit"`
			ExitCodeField   string `json:"exit_code_field"`
			RequestIDField  string `json:"request_id_field"`
		}
	}
}

// GA row 156: use offline help to choose parsers for actual CLI list output.
func TestJSONHelpOutputIsolatedCLI(t *testing.T) {
	cli := os.Getenv("GREGALE_JOURNEY_TEST_CLI")
	if tgt != nil {
		cli = tgt.cli
	}
	if cli == "" {
		t.Skip("set GREGALE_JOURNEY_TEST_CLI or configure a journey target")
	}
	for _, kind := range []string{"build", "invocation"} {
		t.Run(kind, func(t *testing.T) {
			var calls atomic.Int32
			var empty, fail atomic.Bool
			const requestID = "019a767e7f0070008000000000000001"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				want, key := "/v1/builds", "items"
				if kind == "invocation" {
					want, key = "/v1/invocations", "invocations"
				}
				if r.Method != http.MethodGet || r.URL.Path != want {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					http.NotFound(w, r)
					return
				}
				if fail.Load() && r.URL.Query().Get("before") != "" {
					w.Header().Set("Content-Type", "application/problem+json")
					w.Header().Set("X-Faas-Request-Id", requestID)
					w.WriteHeader(503)
					_, _ = io.WriteString(w, `{"status":503,"code":"service_unavailable","title":"Try later"}`)
					return
				}
				rows, next := []map[string]string{}, ""
				if !empty.Load() {
					id := "last"
					if r.URL.Query().Get("before") == "" {
						id, next = "first", "next"
					}
					rows = append(rows, map[string]string{"id": id})
				}
				_ = json.NewEncoder(w).Encode(map[string]any{key: rows, "next_before": next})
			}))
			t.Cleanup(server.Close)
			tg := &target{api: server.URL, cli: cli, home: t.TempDir(), appsDomain: "apps.example.test"}
			const token = "fp_test_output-help-journey"
			help := tg.exec(t, token, tg.home, time.Minute, "--profile", "missing-output-profile", "help", kind, "list")
			var document discoveredListOutput
			if help.exit != 0 || len(help.stderr) != 0 || json.Unmarshal(help.stdout, &document) != nil || document.SchemaVersion != 1 || document.Output.Scope != "json" || len(document.Output.Modes) != 4 || calls.Load() != 0 {
				t.Fatalf("offline help: %d %s %s requests=%d", help.exit, help.stdout, help.stderr, calls.Load())
			}
			for _, private := range []string{token, server.URL, "missing-output-profile"} {
				if strings.Contains(string(help.stdout), private) {
					t.Fatal("discovery exposed active configuration")
				}
			}
			for _, mode := range document.Output.Modes {
				for _, scenario := range []string{"complete", "empty", "api-error"} {
					if scenario == "api-error" && !mode.When["all"] {
						continue
					}
					t.Run(mode.Name+"/"+scenario, func(t *testing.T) {
						calls.Store(0)
						empty.Store(scenario == "empty")
						fail.Store(scenario == "api-error")
						path := filepath.Join(t.TempDir(), "rows.jsonl")
						args := []string{kind, "list", "--limit", "1"}
						for _, name := range []string{"all", "stream", "output"} {
							value, present := mode.When[name]
							if !present {
								continue
							}
							if name == "output" {
								if value {
									args = append(args, "--output", path)
								}
							} else {
								args = append(args, "--"+name+"="+strconv.FormatBool(value))
							}
						}
						res := tg.exec(t, token, tg.home, time.Minute, args...)
						if scenario == "api-error" {
							var problem map[string]json.RawMessage
							if res.exit != document.Output.Errors.ServerErrorExit || json.Unmarshal(res.stderr, &problem) != nil || string(problem[document.Output.Errors.ExitCodeField]) != strconv.Itoa(res.exit) || string(problem[document.Output.Errors.RequestIDField]) != strconv.Quote(requestID) || calls.Load() != 2 {
								t.Fatalf("Problem contract: %d %s", res.exit, res.stderr)
							}
							if mode.Errors.ReadErrorStdout == "empty" {
								if len(res.stdout) != 0 {
									t.Fatalf("private/buffered rows leaked: %s", res.stdout)
								}
							} else if n := discoveredOutputRows(t, res.stdout, mode.Stdout.Encoding, mode.Stdout.Shape, mode.Stdout.ItemsField); n != 1 {
								t.Fatalf("partial rows=%d", n)
							}
							if mode.Errors.ProgressField != "" && problem[mode.Errors.ProgressField] == nil {
								t.Fatalf("missing progress: %s", res.stderr)
							}
							if mode.File != nil {
								if _, err := os.Lstat(path); !os.IsNotExist(err) {
									t.Fatalf("failed export published: %v", err)
								}
							}
							return
						}
						if res.exit != document.Output.SuccessExit || len(res.stderr) != 0 {
							t.Fatalf("list: %d %s %s", res.exit, res.stdout, res.stderr)
						}
						data, encoding, shape, items := res.stdout, mode.Stdout.Encoding, mode.Stdout.Shape, mode.Stdout.ItemsField
						want := 1
						if mode.When["all"] {
							want = 2
						}
						if scenario == "empty" {
							want = 0
						}
						if mode.File != nil {
							var receipt struct {
								Published      bool
								Format, SHA256 string
								Items          int   `json:"items_emitted"`
								Bytes          int64 `json:"bytes_written"`
							}
							if json.Unmarshal(data, &receipt) != nil || !receipt.Published || receipt.Format != mode.File.Encoding || receipt.Items != want {
								t.Fatalf("receipt: %s", data)
							}
							var err error
							data, err = os.ReadFile(path)
							if err != nil {
								t.Fatal(err)
							}
							digest := sha256.Sum256(data)
							if receipt.Bytes != int64(len(data)) || receipt.SHA256 != hex.EncodeToString(digest[:]) {
								t.Fatal("receipt integrity drift")
							}
							encoding, shape, items = mode.File.Encoding, "records", ""
						}
						if n := discoveredOutputRows(t, data, encoding, shape, items); n != want {
							t.Fatalf("declared format parsed %d rows; want %d", n, want)
						}
						wantCalls := want
						if wantCalls == 0 {
							wantCalls = 1
						}
						if int(calls.Load()) != wantCalls {
							t.Fatalf("requests=%d want=%d", calls.Load(), wantCalls)
						}
					})
				}
			}
		})
	}
}

func discoveredOutputRows(t *testing.T, data []byte, encoding, shape, items string) int {
	t.Helper()
	var rows []json.RawMessage
	if encoding == "jsonl" {
		scanner := bufio.NewScanner(bytes.NewReader(data))
		for scanner.Scan() {
			if !json.Valid(scanner.Bytes()) {
				t.Fatalf("invalid record: %q", scanner.Bytes())
			}
			rows = append(rows, append(json.RawMessage(nil), scanner.Bytes()...))
		}
		if err := scanner.Err(); err != nil {
			t.Fatal(err)
		}
	} else if encoding == "json" {
		if !json.Valid(data) {
			t.Fatalf("invalid JSON: %q", data)
		}
		switch shape {
		case "array":
			if json.Unmarshal(data, &rows) != nil {
				t.Fatalf("not array: %s", data)
			}
		case "object":
			var object map[string]json.RawMessage
			if json.Unmarshal(data, &object) != nil || json.Unmarshal(object[items], &rows) != nil {
				t.Fatalf("missing items field %q: %s", items, data)
			}
		default:
			t.Fatalf("unknown shape %q", shape)
		}
	} else {
		t.Fatalf("unknown encoding %q", encoding)
	}
	for _, raw := range rows {
		var row struct{ ID string }
		if json.Unmarshal(raw, &row) != nil || row.ID == "" {
			t.Fatalf("invalid fixture row: %s", raw)
		}
	}
	return len(rows)
}
