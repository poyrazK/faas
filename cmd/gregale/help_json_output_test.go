package main_test

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func readListOutputContract(t *testing.T, topic ...string) *main.CLIOutputContract {
	t.Helper()
	out, stderr, restore := main.SwapIO(t)
	code := main.Run(append([]string{"--json", "help"}, topic...))
	text := stderr()
	restore()
	var document struct {
		SchemaVersion int `json:"schema_version"`
		Output        *main.CLIOutputContract
	}
	decoder := json.NewDecoder(bytes.NewReader(out.Bytes()))
	if code != 0 || text != "" || decoder.Decode(&document) != nil || decoder.Decode(new(any)) != io.EOF || document.SchemaVersion != 1 || document.Output == nil || document.Output.Scope != "json" {
		t.Fatalf("output help %q: exit=%d stdout=%s stderr=%s", topic, code, out, text)
	}
	return document.Output
}

func TestJSONHelpOutputContractsAreOfflineAndCanonical(t *testing.T) {
	setupJSONHelp(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_output-help-private")
	canonical := readListOutputContract(t, "invocation", "list")
	compatibility := readListOutputContract(t, "invocations", "list")
	if !reflect.DeepEqual(canonical, compatibility) {
		t.Fatal("compatibility help lost canonical output metadata")
	}
	readListOutputContract(t, "build", "list")
	out, stderr, restore := main.SwapIO(t)
	code := main.Run([]string{"--profile", "missing-private-profile", "--debug-http", "--json", "help", "build", "list"})
	text := stderr()
	restore()
	if code != 0 || text != "" || !json.Valid(out.Bytes()) {
		t.Fatalf("offline profile help: %d %s %s", code, out, text)
	}
	for _, private := range []string{"fp_test_output-help-private", "missing-private-profile", server.URL} {
		if strings.Contains(out.String(), private) {
			t.Fatal("output discovery exposed active configuration")
		}
	}
	for _, topic := range [][]string{nil, {"build"}, {"invocation"}, {"account"}} {
		out, stderr, restore := main.SwapIO(t)
		code := main.Run(append([]string{"--json", "help"}, topic...))
		text := stderr()
		restore()
		var document map[string]json.RawMessage
		if code != 0 || text != "" || json.Unmarshal(out.Bytes(), &document) != nil || document["output"] != nil {
			t.Fatalf("uncovered command acquired guessed output: %q %d %s %s", topic, code, out, text)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("offline output discovery made %d requests", calls.Load())
	}
}

func outputContractArgs(kind string, mode main.CLIOutputMode, path string) []string {
	args := []string{"--json", kind, "list"}
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
	return args
}

func outputContractRows(t *testing.T, data []byte, encoding, shape, items string) []string {
	t.Helper()
	var rows []json.RawMessage
	if encoding == "jsonl" {
		scan := bufio.NewScanner(bytes.NewReader(data))
		for scan.Scan() {
			if !json.Valid(scan.Bytes()) {
				t.Fatalf("invalid record: %s", scan.Bytes())
			}
			rows = append(rows, append(json.RawMessage(nil), scan.Bytes()...))
		}
		if err := scan.Err(); err != nil {
			t.Fatal(err)
		}
	} else {
		if !json.Valid(data) {
			t.Fatalf("invalid JSON: %q", data)
		}
		switch shape {
		case "array":
			if json.Unmarshal(data, &rows) != nil {
				t.Fatalf("not an array: %s", data)
			}
		case "object":
			var object map[string]json.RawMessage
			if json.Unmarshal(data, &object) != nil || items == "" || json.Unmarshal(object[items], &rows) != nil {
				t.Fatalf("missing declared items %q: %s", items, data)
			}
		default:
			t.Fatalf("unknown shape %q", shape)
		}
	}
	ids := make([]string, 0, len(rows))
	for _, raw := range rows {
		var row struct{ ID string }
		if json.Unmarshal(raw, &row) != nil || row.ID == "" {
			t.Fatalf("invalid fixture row: %s", raw)
		}
		ids = append(ids, row.ID)
	}
	return ids
}

func TestJSONHelpOutputMatchesSuccessfulCommands(t *testing.T) {
	for _, kind := range []string{"build", "invocation"} {
		for _, empty := range []bool{false, true} {
			t.Run(fmt.Sprint(kind, "/", empty), func(t *testing.T) {
				setupListStream(t)
				contract := readListOutputContract(t, kind, "list")
				for _, mode := range contract.Modes {
					t.Run(mode.Name, func(t *testing.T) {
						var calls atomic.Int32
						server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							n := calls.Add(1)
							key := "items"
							wantPath := "/v1/builds"
							if kind == "invocation" {
								key = "invocations"
								wantPath = "/v1/invocations"
							}
							if r.Method != "GET" || r.URL.Path != wantPath {
								t.Errorf("unexpected request: %s %s", r.Method, r.URL)
							}
							rows := []map[string]string{}
							next := ""
							if !empty {
								id := "first"
								if n == 1 {
									next = "next"
								} else {
									id = "last"
								}
								rows = append(rows, map[string]string{"id": id})
							}
							_ = json.NewEncoder(w).Encode(map[string]any{key: rows, "next_before": next})
						}))
						t.Cleanup(server.Close)
						t.Setenv("FAAS_API", server.URL)
						path := filepath.Join(t.TempDir(), "rows.jsonl")
						args := outputContractArgs(kind, mode, path)
						out, stderr, restore := main.SwapIO(t)
						code := main.Run(args)
						text := stderr()
						restore()
						if code != contract.SuccessExit || text != "" {
							t.Fatalf("exit=%d stdout=%s stderr=%s", code, out, text)
						}
						data := out.Bytes()
						encoding, shape, items := mode.Stdout.Encoding, mode.Stdout.Shape, mode.Stdout.ItemsField
						if mode.File != nil {
							if mode.Stdout.Role != "export_receipt" || encoding != "json" || shape != "object" {
								t.Fatalf("file stdout: %+v", mode.Stdout)
							}
							var receipt main.ListOutputReceipt
							decoder := json.NewDecoder(bytes.NewReader(data))
							if decoder.Decode(&receipt) != nil || decoder.Decode(new(any)) != io.EOF || !receipt.Published || receipt.Format != mode.File.Encoding {
								t.Fatalf("receipt: %s", data)
							}
							var err error
							data, err = os.ReadFile(path)
							if err != nil {
								t.Fatal(err)
							}
							digest := sha256.Sum256(data)
							if receipt.SHA256 != hex.EncodeToString(digest[:]) || receipt.BytesWritten != int64(len(data)) || receipt.PagesCompleted != int(calls.Load()) {
								t.Fatalf("receipt integrity: %+v", receipt)
							}
							encoding, shape, items = mode.File.Encoding, "records", ""
						}
						ids := outputContractRows(t, data, encoding, shape, items)
						want := []string{"first"}
						pages := int32(1)
						if mode.When["all"] {
							want = append(want, "last")
							pages = 2
						}
						if empty {
							want = []string{}
							pages = 1
						}
						if !reflect.DeepEqual(ids, want) || calls.Load() != pages {
							t.Fatalf("declared %s output drift: ids=%q want=%q calls=%d", mode.Name, ids, want, calls.Load())
						}
						if empty && (mode.Stdout.Empty == "zero_bytes" || mode.File != nil) && len(data) != 0 {
							t.Fatalf("empty contract drift: %q", data)
						}
						if mode.Stdout.CursorField != "" && !empty {
							var object map[string]json.RawMessage
							_ = json.Unmarshal(out.Bytes(), &object)
							if string(object[mode.Stdout.CursorField]) != `"next"` {
								t.Fatalf("cursor field drift: %s", out)
							}
						}
					})
				}
			})
		}
	}
}

func outputContractStatusExit(contract *main.CLIOutputContract, status int) int {
	for _, group := range contract.Errors.StatusExits {
		for _, candidate := range group.Statuses {
			if candidate == status {
				return group.ExitCode
			}
		}
	}
	if status >= 500 {
		return contract.Errors.ServerErrorExit
	}
	return contract.Errors.OtherAPIErrorExit
}

func TestJSONHelpOutputMatchesAPIProblems(t *testing.T) {
	for _, kind := range []string{"build", "invocation"} {
		t.Run(kind, func(t *testing.T) {
			setupListStream(t)
			contract := readListOutputContract(t, kind, "list")
			for _, mode := range contract.Modes {
				t.Run(mode.Name, func(t *testing.T) {
					var calls atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						n := calls.Add(1)
						key := "items"
						if kind == "invocation" {
							key = "invocations"
						}
						if n == 1 && mode.When["all"] {
							_ = json.NewEncoder(w).Encode(map[string]any{key: []map[string]string{{"id": "first"}}, "next_before": "next"})
							return
						}
						w.Header().Set("Content-Type", "application/problem+json")
						w.Header().Set("X-Faas-Request-Id", errorCorrelationID)
						w.WriteHeader(503)
						_, _ = io.WriteString(w, `{"status":503,"code":"service_unavailable","title":"Try later"}`)
					}))
					t.Cleanup(server.Close)
					t.Setenv("FAAS_API", server.URL)
					path := filepath.Join(t.TempDir(), "rows.jsonl")
					out, stderr, restore := main.SwapIO(t)
					code := main.Run(outputContractArgs(kind, mode, path))
					text := stderr()
					restore()
					var problem map[string]json.RawMessage
					if code != outputContractStatusExit(contract, 503) || json.Unmarshal([]byte(text), &problem) != nil || string(problem[contract.Errors.ExitCodeField]) != strconv.Itoa(code) || string(problem[contract.Errors.CodeField]) != `"service_unavailable"` || string(problem[contract.Errors.RequestIDField]) != `"`+errorCorrelationID+`"` || problem["output"] != nil {
						t.Fatalf("Problem contract drift: %d %s", code, text)
					}
					if mode.Errors.ReadErrorStdout == "empty" {
						if out.Len() != 0 {
							t.Fatalf("buffered/private rows leaked: %s", out)
						}
					} else {
						ids := outputContractRows(t, out.Bytes(), mode.Stdout.Encoding, mode.Stdout.Shape, mode.Stdout.ItemsField)
						if !reflect.DeepEqual(ids, []string{"first"}) {
							t.Fatal(ids)
						}
					}
					if mode.Errors.ProgressField != "" {
						var progress map[string]json.RawMessage
						if json.Unmarshal(problem[mode.Errors.ProgressField], &progress) != nil {
							t.Fatalf("progress missing: %s", text)
						}
						for _, field := range mode.Errors.ProgressFields {
							if progress[field] == nil {
								t.Fatalf("progress field %s missing: %s", field, text)
							}
						}
					} else if problem["pagination"] != nil {
						t.Fatalf("unexpected progress: %s", text)
					}
					if mode.File != nil {
						if _, err := os.Lstat(path); !os.IsNotExist(err) || mode.File.BeforePublicationError != "not_published" {
							t.Fatalf("failed export published: %v", err)
						}
					}
				})
			}
			for _, status := range []int{400, 401, 403, 404, 408, 409, 410, 412, 429} {
				t.Run(strconv.Itoa(status), func(t *testing.T) {
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.Header().Set("Content-Type", "application/problem+json")
						w.WriteHeader(status)
						_, _ = fmt.Fprintf(w, `{"status":%d,"code":"contract_fixture","title":"Fixture"}`, status)
					}))
					t.Cleanup(server.Close)
					t.Setenv("FAAS_API", server.URL)
					out, stderr, restore := main.SwapIO(t)
					code := main.Run([]string{"--json", kind, "list"})
					text := stderr()
					restore()
					var problem map[string]json.RawMessage
					if code != outputContractStatusExit(contract, status) || out.Len() != 0 || json.Unmarshal([]byte(text), &problem) != nil || string(problem[contract.Errors.ExitCodeField]) != strconv.Itoa(code) {
						t.Fatalf("status %d drift: %d %s", status, code, text)
					}
				})
			}
		})
	}
}

type outputContractPartialWriter struct{ bytes.Buffer }

func (w *outputContractPartialWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	_, _ = w.Buffer.Write(p[:1])
	return 1, io.ErrClosedPipe
}

func TestJSONHelpOutputMatchesStdoutWriteFailures(t *testing.T) {
	for _, kind := range []string{"build", "invocation"} {
		t.Run(kind, func(t *testing.T) {
			setupListStream(t)
			contract := readListOutputContract(t, kind, "list")
			for _, mode := range contract.Modes {
				t.Run(mode.Name, func(t *testing.T) {
					var calls atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						n := calls.Add(1)
						key := "items"
						if kind == "invocation" {
							key = "invocations"
						}
						next := ""
						if n == 1 {
							next = "next"
						}
						_ = json.NewEncoder(w).Encode(map[string]any{key: []map[string]string{{"id": "row"}}, "next_before": next})
					}))
					t.Cleanup(server.Close)
					t.Setenv("FAAS_API", server.URL)
					path := filepath.Join(t.TempDir(), "rows.jsonl")
					_, stderr, restore := main.SwapIO(t)
					writer := &outputContractPartialWriter{}
					*main.OsStdout = writer
					code := main.Run(outputContractArgs(kind, mode, path))
					text := stderr()
					restore()
					if mode.Errors.StdoutWriteError != "may_be_partial" || writer.Len() == 0 || json.Valid(writer.Bytes()) || code != contract.Errors.LocalErrorExit || !json.Valid([]byte(text)) {
						t.Fatalf("stdout write contract drift: %d %q %s", code, writer.String(), text)
					}
					if mode.File != nil {
						if mode.File.ReceiptError != "retained" {
							t.Fatal(mode.File)
						}
						data, err := os.ReadFile(path)
						if err != nil || len(data) == 0 {
							t.Fatalf("receipt failure lost completed artifact: %v", err)
						}
					}
				})
			}
		})
	}
}

func TestJSONHelpOutputDoesNotReplaceHumanHelp(t *testing.T) {
	setupJSONHelp(t)
	for _, args := range [][]string{{"help", "build", "list"}, {"--json", "build", "list", "--help"}} {
		out, stderr, restore := main.SwapIO(t)
		code := main.Run(args)
		text := stderr()
		restore()
		if code != 0 || text != "" || json.Valid(out.Bytes()) || !strings.Contains(out.String(), "gregale build list") || strings.Contains(out.String(), "after_complete_read") || strings.Contains(out.String(), "status_exits") {
			t.Fatalf("human help changed: %d %s %s", code, out, text)
		}
	}
}
