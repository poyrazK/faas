package main_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-20261026145832: dlq list lists the unified ledger from every source;
// --source and --trigger narrow it.
func TestDLQListFiltersBySourceAndTrigger(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/demo/dlq" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"events":[
			{"id":"e-inv","source":"invocation","source_id":"i1","last_failed_at":"2026-10-26T10:00:00Z"},
			{"id":"e-trg","source":"trigger_record","source_id":"t1","trigger_id":"trg-a","last_failed_at":"2026-10-26T10:00:00Z"},
			{"id":"e-trg-b","source":"trigger_record","source_id":"t2","trigger_id":"trg-b","last_failed_at":"2026-10-26T10:00:00Z"},
			{"id":"e-web","source":"webhook_delivery","source_id":"w1","last_failed_at":"2026-10-26T10:00:00Z"}
		],"next_before":"cursor-2"}`)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")

	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"dlq", "list", "demo"}, []string{"e-inv", "e-trg", "e-trg-b", "e-web"}},
		{[]string{"dlq", "list", "demo", "--source", "trigger"}, []string{"e-trg", "e-trg-b"}},
		{[]string{"dlq", "list", "demo", "--source=webhook_delivery"}, []string{"e-web"}},
		{[]string{"dlq", "list", "demo", "--trigger", "trg-b"}, []string{"e-trg-b"}},
	} {
		code, stdout, stderr := runCLI(t, append([]string{"--json"}, tc.args...)...)
		if code != 0 {
			t.Fatalf("%v = %d %s", tc.args, code, stderr)
		}
		var resp api.DeadLetterEventsResponse
		if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
			t.Fatalf("%v: %v\n%s", tc.args, err, stdout)
		}
		var ids []string
		for _, event := range resp.Events {
			ids = append(ids, event.ID)
		}
		if strings.Join(ids, " ") != strings.Join(tc.want, " ") || resp.NextBefore != "cursor-2" {
			t.Errorf("%v listed %v (next %q), want %v (next cursor-2)", tc.args, ids, resp.NextBefore, tc.want)
		}
	}

	code, stdout, _ := runCLI(t, "dlq", "list", "demo", "--trigger", "trg-none")
	if code != 0 || !strings.Contains(stdout, "(no dead-letter events)") || !strings.Contains(stdout, "later pages may match") {
		t.Errorf("an empty filtered page must point at the next page: %d\n%s", code, stdout)
	}
	if code, _, _ := runCLI(t, "dlq", "list", "demo", "--source", "queue"); code == 0 {
		t.Error("an unknown --source must be a usage error")
	}
}
