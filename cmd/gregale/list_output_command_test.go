package main_test

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func TestListFileExportCommandContracts(t *testing.T) {
	for _, kind := range []string{"invocation", "build"} {
		for _, mode := range []string{"json", "environment", "human", "stream-human", "empty", "credential-path", "control-path", "invalid-path"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				setupListStream(t)
				dir := t.TempDir()
				name := "rows.jsonl"
				if mode == "credential-path" {
					name = main.TestAPIKey('a') + ".jsonl"
				} else if mode == "control-path" {
					name = "rows\nreport.jsonl"
				} else if mode == "invalid-path" {
					name = string([]byte{'r', 0xff}) + ".jsonl"
				}
				path := filepath.Join(dir, name)
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					n := int(calls.Add(1))
					wantPath := "/v1/invocations"
					key := "invocations"
					if kind == "build" {
						wantPath = "/v1/builds"
						key = "items"
					}
					if r.Method != "GET" || r.URL.Path != wantPath || r.URL.Query().Get("limit") != "2" {
						t.Errorf("request=%s %s", r.Method, r.URL)
					}
					if kind == "build" && (r.URL.Query().Get("app") != "demo" || r.URL.Query().Get("status") != "failed") {
						t.Errorf("filters lost: %s", r.URL)
					}
					wantCursor := []string{"start| + cursor", "middle| + cursor", "last"}[n-1]
					if r.URL.Query().Get("before") != wantCursor {
						t.Errorf("cursor lost: %s", r.URL)
					}
					if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
						t.Errorf("destination visible during traversal: %v", err)
					}
					next := ""
					rows := []map[string]string{}
					if mode != "empty" {
						switch n {
						case 1:
							next = "middle| + cursor"
							rows = []map[string]string{{"id": "first"}}
						case 2:
							next = "last"
						case 3:
							rows = []map[string]string{{"id": "last"}}
						}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{key: rows, "next_before": next})
				}))
				t.Cleanup(server.Close)
				t.Setenv("FAAS_API", server.URL)
				args := []string{kind, "list", "--all", "--limit", "2", "--before", "unused", "--cursor", "start| + cursor", "--output", path}
				if kind == "build" {
					args = append(args, "--app", "demo", "--status", "failed")
				}
				if mode == "human" || mode == "stream-human" {
					if mode == "stream-human" {
						args = append(args, "--stream")
					}
				} else if mode == "environment" {
					t.Setenv("FAAS_JSON", "1")
				} else {
					args = append([]string{"--json"}, args...)
				}
				out, stderr, restore := main.SwapIO(t)
				code := main.Run(args)
				text := stderr()
				restore()
				if code != 0 || text != "" {
					t.Fatalf("exit=%d stdout=%s stderr=%s", code, out, text)
				}
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var ids []string
				scan := bufio.NewScanner(bytes.NewReader(data))
				for scan.Scan() {
					var row struct{ ID string }
					if json.Unmarshal(scan.Bytes(), &row) != nil {
						t.Fatal(scan.Text())
					}
					ids = append(ids, row.ID)
				}
				wantIDs := []string{"first", "last"}
				pages := 3
				if mode == "empty" {
					wantIDs = nil
					pages = 1
				}
				if scan.Err() != nil || !reflect.DeepEqual(ids, wantIDs) || int(calls.Load()) != pages {
					t.Fatalf("rows=%q calls=%d err=%v", ids, calls.Load(), scan.Err())
				}
				digest := sha256.Sum256(data)
				checksum := hex.EncodeToString(digest[:])
				if mode == "human" || mode == "stream-human" {
					if !strings.Contains(out.String(), "Exported 2 items from 3 pages") || !strings.Contains(out.String(), checksum) {
						t.Fatal(out.String())
					}
				} else {
					var receipt main.ListOutputReceipt
					decoder := json.NewDecoder(out)
					if decoder.Decode(&receipt) != nil || decoder.Decode(new(any)) != io.EOF || !receipt.Published || receipt.Format != "jsonl" || receipt.ItemsEmitted != len(ids) || receipt.PagesCompleted != pages || receipt.BytesWritten != int64(len(data)) || receipt.SHA256 != checksum {
						t.Fatalf("receipt=%+v stdout=%s", receipt, out)
					}
					wantPath := path
					if mode == "credential-path" || mode == "invalid-path" {
						wantPath = ""
					}
					if receipt.Path != wantPath {
						t.Fatalf("path=%q want=%q", receipt.Path, wantPath)
					}
				}
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != 0600 {
					t.Fatalf("mode=%v err=%v", info, err)
				}
				entries, _ := os.ReadDir(dir)
				if len(entries) != 1 {
					t.Fatalf("temporary output retained: %v", entries)
				}
			})
		}
	}
}

func TestListFileExportRefusesBeforeRequests(t *testing.T) {
	for _, kind := range []string{"invocation", "build"} {
		for _, mode := range []string{"no-all", "empty", "stdout", "existing", "directory", "symlink", "missing-parent", "bad-limit"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				setupListStream(t)
				t.Setenv("FAAS_TOKEN", "")
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); w.WriteHeader(500) }))
				t.Cleanup(server.Close)
				t.Setenv("FAAS_API", server.URL)
				dir := t.TempDir()
				path := filepath.Join(dir, "rows.jsonl")
				var err error
				switch mode {
				case "existing":
					err = os.WriteFile(path, []byte("keep"), 0600)
				case "directory":
					err = os.Mkdir(path, 0700)
				case "symlink":
					err = os.Symlink("untouched", path)
				case "missing-parent":
					path = filepath.Join(dir, "missing", "rows.jsonl")
				case "empty":
					path = ""
				case "stdout":
					path = "-"
				}
				if err != nil {
					t.Fatal(err)
				}
				args := []string{"--json", kind, "list", "--output=" + path}
				if mode != "no-all" {
					args = append(args, "--all")
				}
				if mode == "bad-limit" {
					args = append(args, "--limit=0")
				}
				out, stderr, restore := main.SwapIO(t)
				code := main.Run(args)
				text := stderr()
				restore()
				if code != 1 || out.Len() != 0 || !json.Valid([]byte(text)) || requests.Load() != 0 {
					t.Fatalf("exit=%d requests=%d stdout=%s stderr=%s", code, requests.Load(), out, text)
				}
				if mode == "existing" {
					b, _ := os.ReadFile(path)
					if string(b) != "keep" {
						t.Fatal("existing file changed")
					}
				}
			})
		}
	}
}

func TestListFileExportFailuresKeepDestination(t *testing.T) {
	for _, kind := range []string{"invocation", "build"} {
		for _, mode := range []string{"api", "cycle", "destination-race", "parent-race", "auth"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				setupListStream(t)
				dir := t.TempDir()
				path := filepath.Join(dir, "rows.jsonl")
				moved := dir + "-moved"
				t.Cleanup(func() { _ = os.RemoveAll(moved) })
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					n := calls.Add(1)
					key := "invocations"
					if kind == "build" {
						key = "items"
					}
					if n == 2 && mode == "api" {
						w.Header().Set("Content-Type", "application/problem+json")
						w.Header().Set("X-Faas-Request-Id", errorCorrelationID)
						w.WriteHeader(503)
						_, _ = io.WriteString(w, `{"status":503,"code":"service_unavailable","title":"Try later"}`)
						return
					}
					if n == 2 && mode == "destination-race" {
						if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
							t.Error(err)
						}
					}
					if n == 2 && mode == "parent-race" {
						if err := os.Rename(dir, moved); err != nil {
							t.Error(err)
						}
						if err := os.Mkdir(dir, 0700); err != nil {
							t.Error(err)
						}
					}
					next := ""
					if n == 1 || mode == "cycle" {
						next = "next"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{key: []map[string]string{{"id": fmt.Sprint(n)}}, "next_before": next})
				}))
				t.Cleanup(server.Close)
				t.Setenv("FAAS_API", server.URL)
				if mode == "auth" {
					t.Setenv("FAAS_TOKEN", "")
				}
				out, stderr, restore := main.SwapIO(t)
				code := main.Run([]string{"--json", kind, "list", "--all", "--output", path})
				text := stderr()
				restore()
				var problem map[string]json.RawMessage
				wantCode := 1
				if mode == "api" {
					wantCode = 3
				}
				wantCalls := int32(2)
				if mode == "auth" {
					wantCode = 2
					wantCalls = 0
				}
				if code != wantCode || out.Len() != 0 || calls.Load() != wantCalls || json.Unmarshal([]byte(text), &problem) != nil || problem["pagination"] != nil {
					t.Fatalf("exit=%d calls=%d stdout=%s stderr=%s", code, calls.Load(), out, text)
				}
				if mode == "api" && (string(problem["code"]) != `"service_unavailable"` || string(problem["request_id"]) != `"`+errorCorrelationID+`"`) {
					t.Fatalf("lost API correlation: %s", text)
				}
				if mode == "destination-race" {
					b, _ := os.ReadFile(path)
					if string(b) != "keep" {
						t.Fatalf("racing destination overwritten: %q", b)
					}
				} else if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("failed destination exists: %v", err)
				}
				for _, parent := range []string{dir, moved} {
					entries, _ := os.ReadDir(parent)
					for _, entry := range entries {
						if strings.HasPrefix(entry.Name(), ".gregale-list-export-") {
							t.Fatalf("private bytes retained: %s", entry.Name())
						}
					}
				}
			})
		}
	}
}

type listReceiptFailure struct{}

func (listReceiptFailure) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestListFileExportReceiptFailureRetainsPublishedFile(t *testing.T) {
	setupListStream(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, `{"invocations":[{"id":"complete"}]}`)
	}))
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	path := filepath.Join(t.TempDir(), "rows.jsonl")
	args := []string{"--json", "invocation", "list", "--all", "--output", path}
	_, stderr, restore := main.SwapIO(t)
	*main.OsStdout = listReceiptFailure{}
	code := main.Run(args)
	text := stderr()
	restore()
	before, err := os.ReadFile(path)
	if err != nil || len(before) == 0 || code == 0 || text == "" || calls.Load() != 1 {
		t.Fatalf("published receipt failure: exit=%d calls=%d file=%q err=%v stderr=%s", code, calls.Load(), before, err, text)
	}
	out, _, restore := main.SwapIO(t)
	code = main.Run(args)
	restore()
	after, _ := os.ReadFile(path)
	if code != 1 || out.Len() != 0 || calls.Load() != 1 || !bytes.Equal(before, after) {
		t.Fatal("retry overwrote or replayed the completed export")
	}
}
