package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// prod hunt 2026-11-21: `gregale queue dlq <slug>` printed the pre-rename
// usage (tail, dead-letter) because only `queue dlq list` was rewritten.
// list is dlq's only verb, so the bare form lists.
func TestQueueDlqWithoutListLists(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/queues/dead_letter") {
			hits++
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rows":[]}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	for _, args := range [][]string{{"queue", "dlq", "web"}, {"queue", "dlq", "list", "web"}} {
		before := hits
		if code, _, stderr := runCLI(t, args...); code != 0 || hits != before+1 {
			t.Fatalf("%v: exit %d, dead-letter reads %d: %s", args, code, hits-before, stderr)
		}
	}
	if code, _, _ := runCLI(t, "queue", "dlqq", "web"); code == 0 {
		t.Fatal("unknown queue verb exited 0")
	}
}
