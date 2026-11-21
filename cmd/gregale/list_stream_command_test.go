package main_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func setupListStream(t *testing.T) {
	t.Helper()
	main.SetupCLIRegression(t)
	t.Chdir(t.TempDir())
	t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))
	t.Setenv("FAAS_JSON", "0")
}

func TestStreamListCommandContracts(t *testing.T) {
	for _, kind := range []string{"invocation", "build"} {
		for _, mode := range []string{"stream", "env-stream", "empty", "api-error", "repeat", "default-page", "default-all", "default-error", "stream-false"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				setupListStream(t)
				var cursors []string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					wantPath := "/v1/invocations"
					if kind == "build" {
						wantPath = "/v1/builds"
					}
					if r.Method != "GET" || r.URL.Path != wantPath || r.URL.Query().Get("limit") != "2" {
						t.Errorf("request=%s %s", r.Method, r.URL)
					}
					if kind == "build" && (r.URL.Query().Get("app") != "demo" || r.URL.Query().Get("status") != "failed") {
						t.Errorf("lost build filters: %s", r.URL)
					}
					cursor := r.URL.Query().Get("before")
					cursors = append(cursors, cursor)
					n := len(cursors)
					if n == 2 && (mode == "api-error" || mode == "default-error") {
						w.Header().Set("Content-Type", "application/problem+json")
						w.Header().Set("X-Faas-Request-Id", errorCorrelationID)
						w.WriteHeader(503)
						_, _ = w.Write([]byte(`{"status":503,"code":"service_unavailable","title":"Try later"}`))
						return
					}
					next := ""
					if n == 1 {
						next = "next| + cursor"
					}
					if mode == "repeat" && n == 2 {
						next = cursor
					}
					rows := []map[string]string{{"id": fmt.Sprintf("row-%d", n)}}
					if mode == "empty" && n == 1 {
						rows = []map[string]string{}
					}
					key := "invocations"
					if kind == "build" {
						key = "items"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{key: rows, "next_before": next})
				}))
				t.Cleanup(server.Close)
				t.Setenv("FAAS_API", server.URL)
				args := []string{"--json", kind, "list", "--limit", "2", "--cursor", "start| + cursor"}
				if mode == "env-stream" {
					t.Setenv("FAAS_JSON", "1")
					args = args[1:]
				}
				if kind == "build" {
					args = append(args, "--app", "demo", "--status", "failed")
				}
				if mode != "default-page" {
					args = append(args, "--all")
				}
				if !strings.HasPrefix(mode, "default-") && mode != "stream-false" {
					args = append(args, "--stream")
				}
				if mode == "stream-false" {
					args = append(args, "--stream=false")
				}
				out, stderr, restore := main.SwapIO(t)
				code := main.Run(args)
				text := stderr()
				restore()
				if mode == "api-error" || mode == "default-error" || mode == "repeat" {
					wantCode := 3
					if mode == "repeat" {
						wantCode = 1
					}
					var problem struct {
						Code       string
						RequestID  string `json:"request_id"`
						ExitCode   int    `json:"exit_code"`
						Pagination *main.ListStreamProgress
					}
					if code != wantCode || json.Unmarshal([]byte(text), &problem) != nil || problem.ExitCode != wantCode {
						t.Fatalf("exit=%d out=%s err=%s", code, out, text)
					}
					if mode != "repeat" && (problem.Code != "service_unavailable" || problem.RequestID != errorCorrelationID) {
						t.Fatalf("API error changed: %s", text)
					}
					if mode == "default-error" {
						if out.Len() != 0 || problem.Pagination != nil {
							t.Fatalf("buffered failure changed: %s %s", out, text)
						}
					} else {
						var row struct{ ID string }
						if json.Unmarshal(out.Bytes(), &row) != nil || row.ID != "row-1" || problem.Pagination == nil || !reflect.DeepEqual(*problem.Pagination, main.ListStreamProgress{PagesCompleted: 1, ItemsEmitted: 1, Partial: true}) {
							t.Fatalf("partial stream: %s %s", out, text)
						}
					}
				} else if code != 0 || text != "" {
					t.Fatalf("exit=%d out=%s err=%s", code, out, text)
				} else if strings.HasPrefix(mode, "default-") || mode == "stream-false" {
					if mode == "default-page" || kind == "invocation" {
						var envelope map[string]json.RawMessage
						if json.Unmarshal(out.Bytes(), &envelope) != nil {
							t.Fatalf("lost envelope: %s", out)
						}
						key := "invocations"
						if kind == "build" {
							key = "items"
						}
						var rows []any
						if json.Unmarshal(envelope[key], &rows) != nil {
							t.Fatalf("lost rows: %s", out)
						}
						expected := 2
						if mode == "default-page" {
							expected = 1
							if string(envelope["next_cursor"]) != `"next| + cursor"` {
								t.Fatalf("lost cursor: %s", out)
							}
						}
						if len(rows) != expected {
							t.Fatal(out.String())
						}
					} else {
						var rows []any
						if json.Unmarshal(out.Bytes(), &rows) != nil || len(rows) != 2 {
							t.Fatalf("lost build array: %s", out)
						}
					}
				} else {
					var ids []string
					scan := bufio.NewScanner(bytes.NewReader(out.Bytes()))
					for scan.Scan() {
						var row struct{ ID string }
						if json.Unmarshal(scan.Bytes(), &row) != nil {
							t.Fatal(scan.Text())
						}
						ids = append(ids, row.ID)
					}
					if err := scan.Err(); err != nil {
						t.Fatal(err)
					}
					want := []string{"row-1", "row-2"}
					if mode == "empty" {
						want = []string{"row-2"}
					}
					if !reflect.DeepEqual(ids, want) {
						t.Fatalf("ids=%v output=%s", ids, out)
					}
				}
				wantCursors := []string{"start| + cursor", "next| + cursor"}
				if mode == "default-page" {
					wantCursors = wantCursors[:1]
				}
				if !reflect.DeepEqual(cursors, wantCursors) {
					t.Fatalf("cursors=%q", cursors)
				}
			})
		}
	}
}

func TestStreamListInvalidOptionsAreLocal(t *testing.T) {
	setupListStream(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	for _, kind := range []string{"invocation", "build"} {
		for _, prefix := range [][]string{nil, {"--json"}} {
			args := append(append([]string{}, prefix...), kind, "list", "--stream")
			if len(prefix) == 0 {
				args = append(args, "--all")
			}
			out, stderr, restore := main.SwapIO(t)
			code := main.Run(args)
			text := stderr()
			restore()
			if code != 1 || out.Len() != 0 || !strings.Contains(text, "--stream requires --json and --all") {
				t.Fatalf("exit=%d out=%s err=%s", code, out, text)
			}
		}
	}
	if requests.Load() != 0 {
		t.Fatal(requests.Load())
	}
}

type observedListWriter struct {
	mu    sync.Mutex
	out   bytes.Buffer
	first chan struct{}
	once  sync.Once
}

func (w *observedListWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	n, err := w.out.Write(p)
	w.mu.Unlock()
	w.once.Do(func() { close(w.first) })
	return n, err
}
func (w *observedListWriter) String() string { w.mu.Lock(); defer w.mu.Unlock(); return w.out.String() }

func TestStreamListEmitsBeforeNextPageCompletes(t *testing.T) {
	setupListStream(t)
	writer := &observedListWriter{first: make(chan struct{})}
	release := make(chan struct{})
	var releaseOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("before") == "" {
			_, _ = w.Write([]byte(`{"invocations":[{"id":"first"}],"next_before":"next"}`))
			return
		}
		select {
		case <-writer.first:
		default:
			t.Error("fetched next page before emitting first")
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = w.Write([]byte(`{"invocations":[{"id":"second"}]}`))
	}))
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	_, stderr, restore := main.SwapIO(t)
	t.Cleanup(restore)
	*main.OsStdout = writer
	result := make(chan int, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		result <- main.Run([]string{"--json", "invocation", "list", "--all", "--stream"})
	}()
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("stream did not finish")
		}
	})
	select {
	case <-writer.first:
	case <-time.After(5 * time.Second):
		t.Fatal("first row was buffered behind later page")
	}
	var row struct{ ID string }
	if json.Unmarshal([]byte(writer.String()), &row) != nil || row.ID != "first" {
		t.Fatal(writer.String())
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case code := <-result:
		if code != 0 || stderr() != "" {
			t.Fatalf("exit=%d err=%s", code, stderr())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream stalled")
	}
}

func TestStreamListBrokenPipeStopsRequests(t *testing.T) {
	setupListStream(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"invocations":[{"id":"first"},{"id":"second"}],"next_before":"next"}`))
	}))
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	_, stderr, restore := main.SwapIO(t)
	defer restore()
	writer := &brokenListWriter{}
	*main.OsStdout = writer
	code := main.Run([]string{"--json", "invocation", "list", "--all", "--stream"})
	text := stderr()
	var problem struct{ Pagination *main.ListStreamProgress }
	// A raw syscall.Errno follows the existing net.Error/exit-3 mapping.
	if code != 3 || calls.Load() != 1 || json.Unmarshal([]byte(text), &problem) != nil || problem.Pagination == nil || problem.Pagination.ItemsEmitted != 1 || problem.Pagination.PagesCompleted != 0 || !problem.Pagination.Partial {
		t.Fatalf("exit=%d calls=%d out=%s err=%s", code, calls.Load(), writer.String(), text)
	}
}
