package main_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// Hunt 33 H33-24 and H33-61: `postgres create --help` shows the flags before
// the name, but only name-first worked; `postgres bindings delete` refused
// the --yes every other delete takes.
func TestCmdPostgresAcceptsDocumentedArgumentOrder(t *testing.T) {
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodDelete {
			_, _ = w.Write([]byte(`{"id":"b1","state":"deleting"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"d1","name":"h33-db","state":"provisioning","region":"us-east-2"}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	previousOut := *main.OsStdout
	t.Cleanup(func() { *main.OsStdout = previousOut })
	var out bytes.Buffer
	*main.OsStdout = &out
	for _, args := range [][]string{
		{"create", "--region", "us-east-2", "h33-db", "--class", "development"},
		{"create", "h33-db", "--region", "us-east-2"},
		{"bindings", "delete", "b1", "--yes"},
	} {
		if code := main.CmdPostgres(args); code != 0 {
			t.Fatalf("%v exit %d", args, code)
		}
	}
	if len(requests) != 3 || !strings.HasPrefix(requests[0], "POST ") || !strings.HasPrefix(requests[2], "DELETE ") {
		t.Fatalf("requests = %v", requests)
	}
}
