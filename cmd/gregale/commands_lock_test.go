package main_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ADR-20261026145832: exclusive keyed work is a --lock on the job run, cron
// or broker trigger, not a separate operations noun.
func TestLockOnJobRunCronAndTrigger(t *testing.T) {
	var requests, bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, r.Method+" "+r.URL.Path)
		bodies = append(bodies, string(body))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/jobs/export/operations":
			_, _ = fmt.Fprint(w, `{"id":"op-1","joined":false,"status_url":"/v1/operations/op-1"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/crons":
			_, _ = fmt.Fprint(w, `{"id":"0123456789abcdef0123456789abcdef","schedule":"*/5 * * * *","path":"/sync","enabled":true}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/triggers":
			_, _ = fmt.Fprint(w, `{"id":"trg-1","kind":"kafka"}`)
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v1/account/operation-trigger-bindings/"):
			parts := strings.Split(r.URL.Path, "/")
			_, _ = fmt.Fprintf(w, `{"source":%q,"trigger_id":%q,"policy":"sync"}`, parts[len(parts)-2], parts[len(parts)-1])
		default:
			_, _ = fmt.Fprint(w, `{}`)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")

	for _, tc := range []struct {
		args       []string
		wantLast   string
		wantInBody string
	}{
		{[]string{"jobs", "run", "export", "--lock", "exports", "--lock-key", `"customer-42"`, "--tasks", "2"}, "POST /v1/jobs/export/operations", `"customer-42"`},
		{[]string{"crons", "add", "--app", "demo", "--schedule", "*/5 * * * *", "--path", "/sync", "--lock", "sync", "--lock-key", `"nightly"`}, "PUT /v1/account/operation-trigger-bindings/cron/0123456789abcdef0123456789abcdef", `"nightly"`},
		{[]string{"triggers", "create", "--app", "demo", "--kind", "kafka", "--slug", "orders", "--config", `{"brokers":["k:9092"],"topic":"orders"}`, "--lock=orders", "--lock-key=7"}, "PUT /v1/account/operation-trigger-bindings/broker/trg-1", `7`},
	} {
		requests, bodies = nil, nil
		code, stdout, stderr := runCLI(t, tc.args...)
		if code != 0 || len(requests) == 0 || requests[len(requests)-1] != tc.wantLast || !strings.Contains(bodies[len(bodies)-1], tc.wantInBody) {
			t.Errorf("%v = %d, requests %v, last body %q\n%s%s", tc.args, code, requests, lastOf(bodies), stdout, stderr)
		}
	}

	for _, args := range [][]string{
		{"jobs", "run", "export", "--lock", "exports"},
		{"crons", "add", "--app", "demo", "--schedule", "*/5 * * * *", "--lock-key", `"k"`},
		{"triggers", "create", "--app", "demo", "--kind", "kafka", "--lock", "orders", "--lock-key", `{"a":1}`},
		{"jobs", "run", "export", "--lock", "exports", "--lock-key", `"k"`, "--parallelism", "3"},
	} {
		requests = nil
		if code, _, _ := runCLI(t, args...); code == 0 || len(requests) != 0 {
			t.Errorf("%v = %d with requests %v; want a local usage error", args, code, requests)
		}
	}
}

func lastOf(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[len(values)-1]
}
