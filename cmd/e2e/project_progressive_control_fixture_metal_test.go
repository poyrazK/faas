//go:build metal

package e2e_test

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// Use an allocated, closed loopback port; connection failure is unavailable
// evidence, whereas a guest 5xx would be a confirmed application regression.
func (f *projectWorkerNativeFixture) disableNativeProbes(t *testing.T) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	mustProjectWorkerNative(t, err)
	address := listener.Addr().String()
	mustProjectWorkerNative(t, listener.Close())
	mustProjectWorkerNative(t, f.h.SetAPIDEnv("FAAS_ROUTE_PROBE_URL", "http://"+address))
	mustProjectWorkerNative(t, f.h.RestartAPID())
}

type lostNativeControlResponse struct {
	transport http.RoundTripper
	dropped   bool
}

func (d *lostNativeControlResponse) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := d.transport.RoundTrip(req)
	if err != nil {
		return response, err
	}
	if !d.dropped && req.Method == http.MethodPost && (strings.HasSuffix(req.URL.Path, "/abort") || strings.HasSuffix(req.URL.Path, "/recover")) && response.StatusCode == http.StatusOK {
		d.dropped = true
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		return nil, io.ErrUnexpectedEOF
	}
	return response, nil
}

func (f *projectWorkerNativeFixture) controlWithLostResponse(t *testing.T, attempt, action string, req api.ProjectReleaseControlRequest) {
	t.Helper()
	client := f.owner.HTTPClient()
	original := client.Transport
	drop := &lostNativeControlResponse{transport: original}
	client.Transport = drop
	_, err := f.owner.ControlProjectRelease(t.Context(), attempt, action, req)
	client.Transport = original
	if err == nil || !drop.dropped {
		t.Fatal("accepted control response was not lost")
	}
	before, err := f.store.ProjectCanaryCheckpoint(t.Context(), f.accountID, attempt)
	mustProjectWorkerNative(t, err)
	mustProjectWorkerNative(t, f.h.RestartAPID())
	mustProjectWorkerNative(t, f.h.RestartSchedd())
	replay, err := f.owner.ControlProjectRelease(t.Context(), attempt, action, req)
	mustProjectWorkerNative(t, err)
	control := replay.Apply.SafeRelease.Verification.Control
	if control == nil || control.RequestID != req.RequestID || control.Action != action || control.ExpectedGeneration != req.ExpectedGeneration || before.Control == nil || !control.RequestedAt.Equal(before.Control.RequestedAt) {
		t.Fatal("control replay changed durable decision")
	}
	changed := req
	changed.ExpectedGeneration++
	if _, err = f.owner.ControlProjectRelease(t.Context(), attempt, action, changed); err == nil {
		t.Fatal("changed control replay was accepted")
	}
}

func (f *projectWorkerNativeFixture) assertNewWorkBlocked(t *testing.T) {
	t.Helper()
	body, status := projectWorkerNativeRequest(t, f.h.EdgeURL()+"/", f.apps["frontend"].Slug+".apps.test.example", http.MethodGet)
	var problem struct {
		Title string `json:"title"`
	}
	mustProjectWorkerNative(t, json.Unmarshal(body, &problem))
	if status != http.StatusServiceUnavailable || problem.Title != "Project release unavailable" {
		t.Fatalf("unassigned work served while recovery blocked: %d", status)
	}
}
