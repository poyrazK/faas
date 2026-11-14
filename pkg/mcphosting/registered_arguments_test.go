package mcphosting

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestRegisteredArgumentsCannotLoadExternalSchemas(t *testing.T) {
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		_, _ = w.Write([]byte(`{"type":"object"}`))
	}))
	defer server.Close()
	if validateRegisteredArguments(map[string]any{"$ref": server.URL + "/schema"}, map[string]any{}) == nil {
		t.Fatal("external schema reference accepted")
	}
	if reads.Load() != 0 {
		t.Fatal("schema validation fetched an untrusted URL")
	}
}
