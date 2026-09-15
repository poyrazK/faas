package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCmdLogsDocumentedArgumentOrder(t *testing.T) {
	for _, args := range [][]string{
		{"myapp", "--deployment", "dep-1", "--grep", "-ERROR", "--level", "error", "--follow"},
		{"--deployment", "dep-1", "--grep", "-ERROR", "--level", "error", "--follow", "myapp"},
		{"myapp", "--deployment=dep-1", "--grep=-ERROR", "--level=error", "--follow=true"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			called := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				if r.URL.Path != "/v1/apps/myapp/logs" {
					t.Errorf("path = %s", r.URL.Path)
				}
				for k, want := range map[string]string{"deployment": "dep-1", "grep": "-ERROR", "level": "error", "follow": "1"} {
					if got := r.URL.Query().Get(k); got != want {
						t.Errorf("%s = %q, want %q", k, got, want)
					}
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, "event: end\ndata: {}\n\n")
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "test-token")
			if code := cmdLogs(args); code != 0 {
				t.Fatalf("exit=%d", code)
			}
			if !called {
				t.Fatal("log request not sent")
			}
		})
	}
}

func TestCmdLogsArchiveRequest(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.URL.Path != "/v1/apps/myapp/logs" {
			t.Errorf("path = %s", r.URL.Path)
		}
		for key, want := range map[string]string{
			"archive":  "1",
			"instance": "inst-abc",
			"date":     "2026-09-14",
		} {
			if got := r.URL.Query().Get(key); got != want {
				t.Errorf("%s = %q, want %q", key, got, want)
			}
		}
		for _, unexpected := range []string{"follow", "deployment", "grep", "since", "level"} {
			if r.URL.Query().Has(unexpected) {
				t.Errorf("unexpected query parameter %q", unexpected)
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: end\ndata: {\"reason\":\"archive_complete\"}\n\n")
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")

	if code := cmdLogs([]string{"myapp", "--archive", "--instance", "inst-abc", "--date", "2026-09-14"}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if !called {
		t.Fatal("archive log request not sent")
	}
}

func TestCmdLogsArchiveValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"missing date", []string{"myapp", "--archive", "--instance", "inst-abc"}, "must be used together"},
		{"implicit archive rejected", []string{"myapp", "--instance", "inst-abc", "--date", "2026-09-14"}, "must be used together"},
		{"invalid date", []string{"myapp", "--archive", "--instance", "inst-abc", "--date", "2026-9-14"}, "YYYY-MM-DD"},
		{"live option conflict", []string{"myapp", "--archive", "--instance", "inst-abc", "--date", "2026-09-14", "--follow"}, "cannot be combined"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stderr, restore := captureStderr(t)
			defer restore()
			if code := cmdLogs(tc.args); code != 2 {
				t.Fatalf("exit=%d, want 2", code)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr=%q, want substring %q", stderr.String(), tc.want)
			}
		})
	}
}

func TestCmdLogsDegradedReason(t *testing.T) {
	for _, tc := range []struct{ name, payload, want string }{
		{"no instance", `{"code":"not_found","error":"rpc error: code = NotFound desc = state: not found"}`, "No running instance"},
		{"unavailable", `{"error":"rpc error: code = Unavailable"}`, "scheduler is temporarily unavailable"},
		{"malformed", `not JSON`, "scheduler is temporarily unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprintf(w, "event: degraded\ndata: %s\n\nevent: end\ndata: {}\n\n", tc.payload)
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "test-token")
			stderr, restore := captureStderr(t)
			defer restore()
			if code := cmdLogs([]string{"myapp"}); code != 3 {
				t.Fatalf("exit=%d, want3", code)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr=%q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

func TestCmdLogsDegradedJSONIsRFC7807(t *testing.T) {
	resetJSONOut(t)
	jsonOutput = true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: degraded\ndata: {\"code\":\"not_found\"}\n\n")
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	stderr, restore := captureStderr(t)
	if code := cmdLogs([]string{"myapp"}); code != 3 {
		t.Fatalf("exit=%d, want 3", code)
	}
	restore()
	var problem map[string]any
	if err := json.Unmarshal([]byte(stderr.String()), &problem); err != nil {
		t.Fatalf("stderr is not JSON: %v; raw=%q", err, stderr.String())
	}
	if problem["code"] != "app_logs_unavailable" || problem["status"] != float64(http.StatusServiceUnavailable) {
		t.Fatalf("problem = %#v", problem)
	}
}
