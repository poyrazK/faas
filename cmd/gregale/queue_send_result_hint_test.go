package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// prod hunt 2026-11-21: a pushed row completed before `queue receive`
// started, so receive reported nothing and the receipt named no way to read
// the row's result. The receipt now names `invocation get ID`.
func TestQueueSendReceiptNamesTheResultCommand(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"1305dc3f-6b9d-4d61-957d-d2a7cb78d0d9","state":"queued"}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	code, stdout, stderr := runCLI(t, "queue", "send", "web", "--payload", `{"job":1}`)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "invocation get 1305dc3f-6b9d-4d61-957d-d2a7cb78d0d9") {
		t.Fatalf("receipt names no result command:\n%s", stdout)
	}
}
