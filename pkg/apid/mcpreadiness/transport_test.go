package mcpreadiness

import (
	"net/http"
	"testing"
)

type replacedTransport struct{}

func (replacedTransport) RoundTrip(*http.Request) (*http.Response, error) {
	panic("readiness probes must not use a replaced process-wide transport")
}

// apid builds this handler while serving; a process that replaced
// http.DefaultTransport (an apidserver test does) must not panic it, and the
// probe client must stay on its own egress-guarded transport.
func TestNewToleratesReplacedDefaultTransport(t *testing.T) {
	previous := http.DefaultTransport
	http.DefaultTransport = replacedTransport{}
	t.Cleanup(func() { http.DefaultTransport = previous })

	h := New(nil, nil, nil)
	transport, ok := h.Client.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil || transport.DialContext == nil {
		t.Fatalf("probe transport = %#v, want an egress-guarded *http.Transport", h.Client.Transport)
	}
}
