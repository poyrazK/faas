package mcpreadiness

import (
	"errors"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/oci"
)

type readinessRoundTripFunc func(*http.Request) (*http.Response, error)

func (f readinessRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// A wrapped process-wide transport must neither panic during route construction
// nor replace the readiness probe's public-egress and redirect fences.
func TestReadinessWrappedDefaultTransportKeepsEgressGuards(t *testing.T) {
	previous := http.DefaultTransport
	called := false
	http.DefaultTransport = readinessRoundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("global transport must not execute a readiness probe")
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	h := New(nil, nil, nil)
	defer h.Client.CloseIdleConnections()
	for _, endpoint := range []string{"http://127.0.0.1:1", "http://169.254.169.254/", "http://[::1]:1"} {
		response, err := h.Client.Get(endpoint)
		if response != nil {
			_ = response.Body.Close()
		}
		if !errors.Is(err, oci.ErrEgressDenied) {
			t.Fatalf("private endpoint %s was not denied: %v", endpoint, err)
		}
	}
	if called {
		t.Fatal("probe used the process-wide transport instead of its egress fence")
	}
	if h.Client.Timeout <= 0 || h.Client.CheckRedirect == nil || !errors.Is(h.Client.CheckRedirect(nil, nil), http.ErrUseLastResponse) {
		t.Fatal("probe lost its bounded deadline or redirect fence")
	}
}
