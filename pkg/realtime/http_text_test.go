package realtime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Message callbacks carry a text frame's data as a string in "text"; "data"
// stays base64 for compatibility and binary frames get no "text".
func TestHTTPHooksMessageCallbackCarriesTextForTextFrames(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = nil
		_ = json.Unmarshal(body, &got)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	send := func(data []byte, binary bool) {
		t.Helper()
		event := Event{ID: "evt", Type: EventMessage, ConnectionID: "rt", Data: data, Binary: binary, CallbackURL: server.URL, CallbackPath: "/realtime/message"}
		if err := (HTTPHooks{}).Message(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	send([]byte(`{"hello":"world"}`), false)
	if got["text"] != `{"hello":"world"}` || got["data"] != "eyJoZWxsbyI6IndvcmxkIn0=" {
		t.Fatalf("text frame callback = %v", got)
	}
	send([]byte{0xff, 0x00}, true)
	if _, ok := got["text"]; ok {
		t.Fatalf("binary frame callback has text: %v", got)
	}
	send([]byte{0xff, 0xfe}, false)
	if _, ok := got["text"]; ok {
		t.Fatalf("invalid UTF-8 callback has text: %v", got)
	}
}
