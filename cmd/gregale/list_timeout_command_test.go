package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func TestListTimeoutCancelsReadsAndRetainsOutputContracts(t *testing.T) {
	for _, kind := range []string{"build", "invocation"} {
		for _, mode := range []string{"page", "all", "stream", "file"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				setupListStream(t)
				var calls atomic.Int32
				closed := make(chan struct{}, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					n := calls.Add(1)
					key, want := "items", "/v1/builds"
					if kind == "invocation" {
						key, want = "invocations", "/v1/invocations"
					}
					if r.Method != "GET" || r.URL.Path != want {
						t.Errorf("unexpected request: %s %s", r.Method, r.URL)
						http.NotFound(w, r)
						return
					}
					if n == 1 && mode != "page" {
						_ = json.NewEncoder(w).Encode(map[string]any{key: []map[string]string{{"id": "first"}}, "next_before": "next"})
						return
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(200)
					_, _ = io.WriteString(w, `{"`+key+`":[`)
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					closed <- struct{}{}
				}))
				t.Cleanup(server.Close)
				t.Setenv("FAAS_API", server.URL)
				path := filepath.Join(t.TempDir(), "rows.jsonl")
				args := []string{"--json", kind, "list", "--timeout", "500ms"}
				if mode != "page" {
					args = append(args, "--all")
				}
				if mode == "stream" {
					args = append(args, "--stream")
				}
				if mode == "file" {
					args = append(args, "--output", path)
				}
				out, stderr, restore := main.SwapIO(t)
				code := main.Run(args)
				text := stderr()
				restore()
				var problem struct {
					Code, Category string
					Exit           int `json:"exit_code"`
					CLI            json.RawMessage
					RequestID      string `json:"request_id"`
					Pagination     *main.ListStreamProgress
				}
				decoder := json.NewDecoder(strings.NewReader(text))
				if code != 3 || decoder.Decode(&problem) != nil || decoder.Decode(new(any)) != io.EOF || problem.Code != "transport_error" || problem.Category != "temporary_failure" || problem.Exit != 3 || problem.CLI != nil || problem.RequestID != "" {
					t.Fatalf("deadline Problem: %d %s %s", code, out, text)
				}
				select {
				case <-closed:
				case <-time.After(3 * time.Second):
					t.Fatal("deadline did not cancel HTTP body read")
				}
				wantCalls := int32(2)
				if mode == "page" {
					wantCalls = 1
				}
				if calls.Load() != wantCalls {
					t.Fatalf("calls=%d want=%d", calls.Load(), wantCalls)
				}
				if mode == "stream" {
					var row struct{ ID string }
					if json.Unmarshal(out.Bytes(), &row) != nil || row.ID != "first" || problem.Pagination == nil || problem.Pagination.PagesCompleted != 1 || problem.Pagination.ItemsEmitted != 1 || !problem.Pagination.Partial {
						t.Fatalf("stream progress: %s %s", out, text)
					}
				} else if out.Len() != 0 || problem.Pagination != nil {
					t.Fatalf("buffered/private rows leaked: %s %s", out, text)
				}
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatalf("expired file published: %v", err)
				}
			})
		}
	}
}

func TestListTimeoutRejectsInvalidBudgetsBeforeAuthentication(t *testing.T) {
	for _, kind := range []string{"build", "invocation"} {
		t.Run(kind, func(t *testing.T) {
			setupListStream(t)
			t.Setenv("FAAS_TOKEN", "")
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(500) }))
			t.Cleanup(server.Close)
			t.Setenv("FAAS_API", server.URL)
			for _, value := range []string{"0", "-1s", "", "bad", "999999999999999999999h"} {
				dir := t.TempDir()
				path := filepath.Join(dir, "rows.jsonl")
				out, stderr, restore := main.SwapIO(t)
				code := main.Run([]string{"--json", kind, "list", "--all", "--output", path, "--timeout=" + value})
				text := stderr()
				restore()
				var problem struct {
					Code string
					CLI  *struct{ Kind string }
				}
				if code != 1 || out.Len() != 0 || json.Unmarshal([]byte(text), &problem) != nil || problem.Code == "unauthorized" || (problem.CLI != nil && problem.CLI.Kind == "invalid_flag_combination") || calls.Load() != 0 {
					t.Fatalf("invalid budget %q: %d %s %s", value, code, out, text)
				}
				entries, err := os.ReadDir(dir)
				if err != nil || len(entries) != 0 {
					t.Fatalf("invalid budget created private output: %v %v", entries, err)
				}
			}
		})
	}
}

func TestListTimeoutBudgetsHumanInvocationEnrichment(t *testing.T) {
	setupListStream(t)
	var apps, lists atomic.Int32
	closed := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/apps" {
			apps.Add(1)
			w.WriteHeader(200)
			_, _ = io.WriteString(w, `[`)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			closed <- struct{}{}
			return
		}
		lists.Add(1)
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	out, stderr, restore := main.SwapIO(t)
	code := main.Run([]string{"invocation", "list", "--timeout", "500ms"})
	text := stderr()
	restore()
	if code != 3 || out.Len() != 0 || text == "" || apps.Load() != 1 || lists.Load() != 0 {
		t.Fatalf("enrichment escaped deadline: %d %s %s apps=%d lists=%d", code, out, text, apps.Load(), lists.Load())
	}
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("enrichment read remained open")
	}
}

func TestListTimeoutRetainsAPIProblemWithReadRetriesEnabled(t *testing.T) {
	for _, kind := range []string{"build", "invocation"} {
		t.Run(kind, func(t *testing.T) {
			setupListStream(t)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/problem+json")
				w.Header().Set("Retry-After", "2")
				w.Header().Set("X-Faas-Request-Id", errorCorrelationID)
				w.WriteHeader(503)
				_, _ = io.WriteString(w, `{"status":503,"code":"service_unavailable","title":"Try later"}`)
			}))
			t.Cleanup(server.Close)
			t.Setenv("FAAS_API", server.URL)
			out, stderr, restore := main.SwapIO(t)
			code := main.Run([]string{"--http-retries", "3", "--json", kind, "list", "--timeout", "500ms"})
			text := stderr()
			restore()
			var problem struct {
				Code      string
				RequestID string `json:"request_id"`
				CLI       json.RawMessage
			}
			if code != 3 || out.Len() != 0 || json.Unmarshal([]byte(text), &problem) != nil || problem.Code != "service_unavailable" || problem.RequestID != errorCorrelationID || problem.CLI != nil || calls.Load() != 1 {
				t.Fatalf("list error lost real Problem/correlation: %d %s %s calls=%d", code, out, text, calls.Load())
			}
		})
	}
}

func TestListTimeoutPreservesSuccessAndLastFlagWins(t *testing.T) {
	for _, kind := range []string{"build", "invocation"} {
		for _, mode := range []string{"page", "all", "stream", "file"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
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
				args := []string{"--json", kind, "list", "--timeout=0", "--timeout=1h"}
				if mode != "page" {
					args = append(args, "--all")
				}
				if mode == "stream" {
					args = append(args, "--stream")
				}
				if mode == "file" {
					args = append(args, "--output", path)
				}
				out, stderr, restore := main.SwapIO(t)
				code := main.Run(args)
				text := stderr()
				restore()
				if code != 0 || text != "" || calls.Load() != 1 {
					t.Fatalf("successful read changed: %d %s %s", code, out, text)
				}
				if mode == "stream" {
					if out.Len() != 0 {
						t.Fatal(out.String())
					}
					return
				}
				if !json.Valid(out.Bytes()) {
					t.Fatalf("invalid stdout: %s", out)
				}
				if mode == "file" {
					data, err := os.ReadFile(path)
					if err != nil || len(data) != 0 {
						t.Fatalf("file=%q err=%v", data, err)
					}
				}
				if mode == "all" && kind == "build" && !bytes.Equal(bytes.TrimSpace(out.Bytes()), []byte("[]")) {
					t.Fatal(out.String())
				}
			})
		}
	}
}

func TestListTimeoutDiscovery(t *testing.T) {
	setupJSONHelp(t)
	for _, kind := range []string{"build", "invocation"} {
		out, stderr, restore := main.SwapIO(t)
		code := main.Run([]string{"--json", "help", kind, "list"})
		text := stderr()
		restore()
		var document struct {
			Flags []struct {
				Name, Value, Description string
				TakesValue               bool `json:"takes_value"`
			}
		}
		if code != 0 || text != "" || json.Unmarshal(out.Bytes(), &document) != nil {
			t.Fatalf("help: %d %s %s", code, out, text)
		}
		found := false
		for _, flag := range document.Flags {
			if flag.Name == "timeout" {
				found = true
				if !flag.TakesValue || flag.Value != "DURATION" || !strings.Contains(flag.Description, "overall") {
					t.Fatal(flag)
				}
			}
		}
		if !found {
			t.Fatalf("%s list timeout missing", kind)
		}
	}
}

type listTimeoutCancelWriter struct {
	bytes.Buffer
	cancel context.CancelFunc
}

func (w *listTimeoutCancelWriter) Write(data []byte) (int, error) {
	n, err := w.Buffer.Write(data)
	w.cancel()
	return n, err
}

func TestListTimeoutTerminalCancellationRetainsPartialProgress(t *testing.T) {
	setupListStream(t)
	previous := *main.JsonOutput
	*main.JsonOutput = true
	t.Cleanup(func() { *main.JsonOutput = previous })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, stderr, restore := main.SwapIO(t)
	writer := &listTimeoutCancelWriter{cancel: cancel}
	*main.OsStdout = writer
	code := main.RunListTimeoutTerminalStream(ctx)
	text := stderr()
	restore()
	var problem struct{ Pagination *main.ListStreamProgress }
	if code != 130 || writer.String() != "1\n" || json.Unmarshal([]byte(text), &problem) != nil || problem.Pagination == nil || !problem.Pagination.Partial || problem.Pagination.PagesCompleted != 1 || problem.Pagination.ItemsEmitted != 1 {
		t.Fatalf("terminal cancellation lost emitted records: %d %s %s", code, writer.String(), text)
	}
}

func TestListTimeoutIncludesRetryableEnrichmentWait(t *testing.T) {
	setupListStream(t)
	var apps, lists atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/apps" {
			apps.Add(1)
			w.Header().Set("Content-Type", "application/problem+json")
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(503)
			_, _ = io.WriteString(w, `{"status":503,"code":"service_unavailable","title":"Try later"}`)
			return
		}
		lists.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"invocations": []any{}})
	}))
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	out, stderr, restore := main.SwapIO(t)
	code := main.Run([]string{"--http-retries", "3", "invocation", "list", "--timeout", "500ms"})
	text := stderr()
	restore()
	if code != 3 || out.Len() != 0 || text == "" || apps.Load() != 1 || lists.Load() != 0 {
		t.Fatalf("retry wait escaped the shared deadline: %d %s %s apps=%d lists=%d", code, out, text, apps.Load(), lists.Load())
	}
}
