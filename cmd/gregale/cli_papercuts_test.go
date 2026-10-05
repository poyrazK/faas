package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Prod hunt #3: `invoke --payload not-json` failed with "marshal request:
// json: error calling MarshalJSON for type json.RawMessage: invalid
// character 'o' in literal null".
func TestResolvePayloadRejectsInvalidJSON(t *testing.T) {
	if _, err := resolvePayload("not-json"); err == nil || !strings.Contains(err.Error(), "--payload must be valid JSON") {
		t.Fatalf("resolvePayload(not-json) err = %v, want a --payload validation error", err)
	}
	for _, ok := range []string{"", `{"name":"hunt3"}`, "[1,2]", `"text"`, "  "} {
		if _, err := resolvePayload(ok); err != nil {
			t.Errorf("resolvePayload(%q) = %v, want nil", ok, err)
		}
	}
}

// `invocations wait <id> --timeout 60s` was rejected as a usage error because
// the flag package stops at the first positional.
func TestInvocationsWaitAcceptsFlagsAfterID(t *testing.T) {
	resetJSONOut(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/invocations/inv-1") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"inv-1","state":"completed"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	_, readStderr, restore := swapIO(t)
	defer restore()
	code := cmdInvocationsWait([]string{"inv-1", "--timeout", "5s", "--interval", "10ms"})
	stderr := readStderr()
	if strings.Contains(stderr, "usage: gregale invocations wait") {
		t.Fatalf("flags after the id were rejected: %s", stderr)
	}
	if code != 0 {
		t.Fatalf("invocations wait exit = %d, stderr=%s", code, stderr)
	}
}
