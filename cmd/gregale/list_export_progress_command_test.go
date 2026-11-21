package main_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func decodeExportStderr(t *testing.T, text string) ([]main.ListExportProgressEvent, []map[string]json.RawMessage) {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(text))
	events := []main.ListExportProgressEvent{}
	problems := []map[string]json.RawMessage{}
	for {
		var raw map[string]json.RawMessage
		err := decoder.Decode(&raw)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("invalid diagnostic stream: %s", text)
		}
		if string(raw["type"]) == `"list_export_progress"` {
			var event main.ListExportProgressEvent
			data, _ := json.Marshal(raw)
			if json.Unmarshal(data, &event) != nil || len(raw) != 8 {
				t.Fatalf("progress fields: %s", data)
			}
			events = append(events, event)
		} else {
			if raw["code"] == nil || raw["exit_code"] == nil {
				t.Fatalf("not progress/Problem: %s", text)
			}
			problems = append(problems, raw)
		}
	}
	return events, problems
}

func lastExportProgress(t *testing.T, events []main.ListExportProgressEvent) main.ListExportProgressEvent {
	t.Helper()
	if len(events) == 0 {
		t.Fatal("missing opted-in progress events")
	}
	return events[len(events)-1]
}

func TestExportProgressCLIReceiptsAndDefaultSilence(t *testing.T) {
	for _, kind := range []string{"build", "invocation"} {
		for _, mode := range []string{"default", "off", "plain", "ndjson", "human", "environment"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				setupListStream(t)
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					n := calls.Add(1)
					key, want := "items", "/v1/builds"
					if kind == "invocation" {
						key, want = "invocations", "/v1/invocations"
					}
					if r.Method != "GET" || r.URL.Path != want {
						t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					}
					id, next := "private-first", "private-cursor"
					if n == 2 {
						id, next = "private-last", ""
					}
					_ = json.NewEncoder(w).Encode(map[string]any{key: []map[string]string{{"id": id}}, "next_before": next})
				}))
				t.Cleanup(server.Close)
				t.Setenv("FAAS_API", server.URL)
				path := filepath.Join(t.TempDir(), "private-path.jsonl")
				args := []string{"--json", kind, "list", "--all", "--output", path}
				selected := mode
				if mode == "human" || mode == "environment" {
					selected = "ndjson"
					args = args[1:]
					if mode == "environment" {
						t.Setenv("FAAS_JSON", "1")
					}
				}
				if mode != "default" {
					args = append(args, "--progress", selected)
				}
				out, stderr, restore := main.SwapIO(t)
				code := main.Run(args)
				text := stderr()
				restore()
				data, err := os.ReadFile(path)
				if code != 0 || err != nil || calls.Load() != 2 {
					t.Fatalf("export: %d %s %s %v calls=%d", code, out, text, err, calls.Load())
				}
				var receipt main.ListOutputReceipt
				if mode == "human" {
					if !strings.Contains(out.String(), "Exported 2 items") {
						t.Fatal(out.String())
					}
				} else {
					digest := sha256.Sum256(data)
					if json.Unmarshal(out.Bytes(), &receipt) != nil || !receipt.Published || receipt.PagesCompleted != 2 || receipt.ItemsEmitted != 2 || receipt.BytesWritten != int64(len(data)) || receipt.SHA256 != hex.EncodeToString(digest[:]) {
						t.Fatalf("receipt: %s", out)
					}
				}
				if mode == "default" || mode == "off" {
					if text != "" {
						t.Fatal(text)
					}
					return
				}
				if mode == "plain" {
					if !strings.HasPrefix(text, "Export start:") || !strings.Contains(text, "Export published: pages=2 items=2 bytes=") {
						t.Fatal(text)
					}
					return
				}
				events, problems := decodeExportStderr(t, text)
				last := lastExportProgress(t, events)
				if len(problems) != 0 || len(events) < 2 || events[0].Event != "start" || last.Event != "published" || !last.Published || last.PagesCompleted != 2 || last.ItemsEmitted != 2 || last.BytesWritten != int64(len(data)) || strings.Contains(text, "private") || strings.Contains(text, server.URL) {
					t.Fatalf("progress/receipt: %s", text)
				}
			})
		}
	}
}

func TestExportProgressAPIProblemAfterStoppedEvent(t *testing.T) {
	for _, kind := range []string{"build", "invocation"} {
		t.Run(kind, func(t *testing.T) {
			setupListStream(t)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) == 1 {
					key := "items"
					if kind == "invocation" {
						key = "invocations"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{key: []map[string]string{{"id": "private-row"}}, "next_before": "private-next"})
					return
				}
				w.Header().Set("Content-Type", "application/problem+json")
				w.Header().Set("X-Faas-Request-Id", errorCorrelationID)
				w.WriteHeader(503)
				_, _ = io.WriteString(w, `{"status":503,"code":"service_unavailable","title":"Try later"}`)
			}))
			t.Cleanup(server.Close)
			t.Setenv("FAAS_API", server.URL)
			path := filepath.Join(t.TempDir(), "private-output.jsonl")
			out, stderr, restore := main.SwapIO(t)
			code := main.Run([]string{"--json", kind, "list", "--all", "--output", path, "--progress", "ndjson"})
			text := stderr()
			restore()
			events, problems := decodeExportStderr(t, text)
			last := lastExportProgress(t, events)
			if code != 3 || out.Len() != 0 || len(problems) != 1 || string(problems[0]["code"]) != `"service_unavailable"` || string(problems[0]["request_id"]) != `"`+errorCorrelationID+`"` || problems[0]["pagination"] != nil || problems[0]["cli"] != nil || last.Event != "stopped" || last.Published || last.PagesCompleted != 1 || last.ItemsEmitted != 1 || last.BytesWritten == 0 || calls.Load() != 2 {
				t.Fatalf("stopped/Problem: %d %s %s", code, out, text)
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("failed export published: %v", err)
			}
			for _, event := range events {
				encoded, _ := json.Marshal(event)
				if bytes.Contains(encoded, []byte("private")) {
					t.Fatalf("event exposed input: %s", encoded)
				}
			}
		})
	}
}

type exportProgressBrokenSink struct{ calls int }

func (w *exportProgressBrokenSink) Write([]byte) (int, error) { w.calls++; return 0, syscall.EPIPE }

type exportProgressBrokenReceipt struct{}

func (exportProgressBrokenReceipt) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestExportProgressSinkAndReceiptFailures(t *testing.T) {
	for _, kind := range []string{"build", "invocation"} {
		for _, receiptFailure := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "/progress", true: "/receipt"}[receiptFailure], func(t *testing.T) {
				setupListStream(t)
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					calls.Add(1)
					key := "items"
					if kind == "invocation" {
						key = "invocations"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{key: []any{}})
				}))
				t.Cleanup(server.Close)
				t.Setenv("FAAS_API", server.URL)
				path := filepath.Join(t.TempDir(), "rows.jsonl")
				out, stderr, restore := main.SwapIO(t)
				sink := &exportProgressBrokenSink{}
				if receiptFailure {
					*main.OsStdout = exportProgressBrokenReceipt{}
				} else {
					*main.OsStderr = sink
				}
				code := main.Run([]string{"--json", kind, "list", "--all", "--output", path, "--progress", "ndjson"})
				text := stderr()
				restore()
				data, err := os.ReadFile(path)
				if err != nil || len(data) != 0 || calls.Load() != 1 {
					t.Fatalf("diagnostic failure lost artifact: %q %v", data, err)
				}
				if receiptFailure {
					events, problems := decodeExportStderr(t, text)
					last := lastExportProgress(t, events)
					if code != 1 || len(problems) != 1 || !last.Published || last.Event != "published" {
						t.Fatalf("receipt failure changed commit: %d %s", code, text)
					}
				} else {
					var receipt main.ListOutputReceipt
					if code != 0 || sink.calls != 1 || text != "" || json.Unmarshal(out.Bytes(), &receipt) != nil || !receipt.Published {
						t.Fatalf("progress sink controlled export: %d %s %s calls=%d", code, out, text, sink.calls)
					}
				}
			})
		}
	}
}

func TestExportProgressValidationBeforeAuthentication(t *testing.T) {
	for _, kind := range []string{"build", "invocation"} {
		t.Run(kind, func(t *testing.T) {
			setupListStream(t)
			t.Setenv("FAAS_TOKEN", "")
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(500) }))
			t.Cleanup(server.Close)
			t.Setenv("FAAS_API", server.URL)
			for _, test := range []struct {
				args    []string
				missing []string
			}{
				{[]string{"--progress", "ndjson"}, []string{"output", "all"}},
				{[]string{"--all", "--progress", "plain"}, []string{"output"}},
				{[]string{"--output", "private-path", "--progress", "ndjson"}, []string{"all"}},
				{[]string{"--progress", "private-value"}, nil},
			} {
				out, stderr, restore := main.SwapIO(t)
				code := main.Run(append([]string{"--json", kind, "list"}, test.args...))
				text := stderr()
				restore()
				events, problems := decodeExportStderr(t, text)
				if code != 1 || out.Len() != 0 || len(events) != 0 || len(problems) != 1 || string(problems[0]["code"]) != `"invalid_request"` || strings.Contains(text, "private") || calls.Load() != 0 {
					t.Fatalf("pre-auth progress: %d %s %s", code, out, text)
				}
				if test.missing != nil {
					var cli main.CLIFlagRuleDiagnostic
					if json.Unmarshal(problems[0]["cli"], &cli) != nil || cli.Kind != "invalid_flag_combination" || cli.Rule.ID != main.ListProgressRequiresExport {
						t.Fatal(text)
					}
					names := []string{}
					for _, condition := range cli.Missing {
						names = append(names, condition.Flag)
					}
					if !reflect.DeepEqual(names, test.missing) {
						t.Fatal(cli.Missing)
					}
				}
			}
			out, stderr, restore := main.SwapIO(t)
			code := main.Run([]string{"--json", kind, "list", "--progress", "off"})
			text := stderr()
			restore()
			events, problems := decodeExportStderr(t, text)
			if code != 2 || out.Len() != 0 || len(events) != 0 || len(problems) != 1 || string(problems[0]["code"]) != `"unauthorized"` || calls.Load() != 0 {
				t.Fatalf("disabled progress changed auth: %d %s", code, text)
			}
		})
	}
}

func TestExportProgressJSONHelpContract(t *testing.T) {
	setupJSONHelp(t)
	for _, kind := range []string{"build", "invocation"} {
		out, stderr, restore := main.SwapIO(t)
		code := main.Run([]string{"--json", "help", kind, "list"})
		text := stderr()
		restore()
		var doc struct {
			Flags []struct {
				Name    string
				Choices []string
			}
			Output *main.CLIOutputContract
		}
		if code != 0 || text != "" || json.Unmarshal(out.Bytes(), &doc) != nil || doc.Output == nil {
			t.Fatalf("help: %d %s %s", code, out, text)
		}
		found := false
		for _, flag := range doc.Flags {
			if flag.Name == "progress" {
				found = true
				if !reflect.DeepEqual(flag.Choices, []string{"off", "plain", "ndjson"}) {
					t.Fatal(flag)
				}
			}
		}
		if !found {
			t.Fatal("progress flag missing")
		}
		for _, mode := range doc.Output.Modes {
			if mode.File == nil {
				if mode.Progress != nil {
					t.Fatal("progress advertised for stdout")
				}
				continue
			}
			if mode.Progress == nil || mode.Progress.Flag != "progress" || mode.Progress.NDJSONType != "list_export_progress" || mode.Progress.SchemaVersion != 1 || mode.Progress.MinUpdateIntervalMS != 250 || !mode.Progress.BestEffort {
				t.Fatal(mode.Progress)
			}
		}
	}
}
