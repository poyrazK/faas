package faas

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

const testGregaleReleaseID = "a91f2c10-48f2-4d07-b683-37e0caa78241"

func gregaleTestResponse(status int, release string) *http.Response {
	header := make(http.Header)
	if release != "" {
		header.Set(GregaleReleaseHeader, release)
	}
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader("")),
	}
}

func TestGregaleClientReleaseTransportCapturesAndPinsManagedRequests(t *testing.T) {
	var received []string
	transport, err := NewGregaleClientReleaseTransport(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		received = append(received, req.Header.Get(GregaleReleaseHeader))
		if len(received) == 1 {
			return gregaleTestResponse(http.StatusOK, testGregaleReleaseID), nil
		}
		return gregaleTestResponse(http.StatusNoContent, testGregaleReleaseID), nil
	}), GregaleClientReleaseOptions{ManagedOrigins: []string{"https://api.example.com/v1/"}})
	if err != nil {
		t.Fatal(err)
	}

	first, _ := http.NewRequest(http.MethodGet, "https://api.example.com/bootstrap", nil)
	first.Header.Set("X-Caller", "unchanged")
	response, err := transport.RoundTrip(first)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if got := first.Header.Get(GregaleReleaseHeader); got != "" {
		t.Fatalf("caller request was mutated with release %q", got)
	}
	if got := first.Header.Get("X-Caller"); got != "unchanged" {
		t.Fatalf("caller header = %q, want unchanged", got)
	}

	second, _ := http.NewRequest(http.MethodPost, "https://api.example.com/checkout", nil)
	response, err = transport.RoundTrip(second)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(received) != 2 || received[0] != "" || received[1] != testGregaleReleaseID {
		t.Fatalf("release headers = %#v, want first unpinned then pinned", received)
	}
	if release, ok := transport.Release(); !ok || release != testGregaleReleaseID {
		t.Fatalf("Release() = %q, %v; want captured release", release, ok)
	}
}

func TestGregaleClientReleaseTransportScopesPinsToConfiguredOrigins(t *testing.T) {
	const initial = "11111111-2222-4333-8444-555555555555"
	var gotExternal, gotManaged string
	transport, err := NewGregaleClientReleaseTransport(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "elsewhere.example":
			gotExternal = req.Header.Get(GregaleReleaseHeader)
		case "api.example.com":
			gotManaged = req.Header.Get(GregaleReleaseHeader)
		}
		return gregaleTestResponse(http.StatusOK, testGregaleReleaseID), nil
	}), GregaleClientReleaseOptions{
		ManagedOrigins: []string{"https://api.example.com"},
		InitialRelease: initial,
	})
	if err != nil {
		t.Fatal(err)
	}

	external, _ := http.NewRequest(http.MethodGet, "https://elsewhere.example/", nil)
	external.Header.Set(GregaleReleaseHeader, "caller-value")
	response, err := transport.RoundTrip(external)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	managed, _ := http.NewRequest(http.MethodGet, "https://api.example.com/", nil)
	response, err = transport.RoundTrip(managed)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()

	if gotExternal != "caller-value" || external.Header.Get(GregaleReleaseHeader) != "caller-value" {
		t.Fatalf("external release = %q (caller now %q), want caller value preserved", gotExternal, external.Header.Get(GregaleReleaseHeader))
	}
	if gotManaged != initial {
		t.Fatalf("managed release = %q, want initial release %q", gotManaged, initial)
	}
	if release, ok := transport.Release(); !ok || release != initial {
		t.Fatalf("external response changed client release to %q, %v", release, ok)
	}
}

func TestGregaleClientReleaseTransportPreservesExplicitPins(t *testing.T) {
	const initial = "11111111-2222-4333-8444-555555555555"
	var received []http.Header
	transport, err := NewGregaleClientReleaseTransport(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		received = append(received, req.Header.Clone())
		return gregaleTestResponse(http.StatusNoContent, ""), nil
	}), GregaleClientReleaseOptions{
		ManagedOrigins: []string{"https://api.example.com"},
		InitialRelease: initial,
	})
	if err != nil {
		t.Fatal(err)
	}

	withRevision, _ := http.NewRequest(http.MethodGet, "https://api.example.com/revision", nil)
	withRevision.Header.Set(GregaleRevisionHeader, "deployment-id")
	if _, err := transport.RoundTrip(withRevision); err != nil {
		t.Fatal(err)
	}
	withRelease, _ := http.NewRequest(http.MethodGet, "https://api.example.com/release", nil)
	withRelease.Header.Set(GregaleReleaseHeader, "caller-release")
	if _, err := transport.RoundTrip(withRelease); err != nil {
		t.Fatal(err)
	}

	if got := received[0].Get(GregaleRevisionHeader); got != "deployment-id" {
		t.Errorf("explicit revision = %q, want unchanged", got)
	}
	if got := received[0].Get(GregaleReleaseHeader); got != "" {
		t.Errorf("release was added alongside exact revision: %q", got)
	}
	if got := received[1].Get(GregaleReleaseHeader); got != "caller-release" {
		t.Errorf("explicit release = %q, want unchanged", got)
	}
	if release, _ := transport.Release(); release != initial {
		t.Errorf("explicit pin changed transport state to %q", release)
	}
}

func TestGregaleClientReleaseTransportReturnsExpiredPinWithoutFallback(t *testing.T) {
	const initial = "11111111-2222-4333-8444-555555555555"
	var calls atomic.Int32
	var received []string
	transport, err := NewGregaleClientReleaseTransport(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		received = append(received, req.Header.Get(GregaleReleaseHeader))
		if calls.Add(1) == 1 {
			return gregaleTestResponse(http.StatusGone, testGregaleReleaseID), nil
		}
		return gregaleTestResponse(http.StatusOK, ""), nil
	}), GregaleClientReleaseOptions{
		ManagedOrigins: []string{"https://api.example.com"},
		InitialRelease: initial,
	})
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		request, _ := http.NewRequest(http.MethodGet, "https://api.example.com/", nil)
		response, roundTripErr := transport.RoundTrip(request)
		if roundTripErr != nil {
			t.Fatal(roundTripErr)
		}
		if i == 0 && response.StatusCode != http.StatusGone {
			t.Fatalf("first status = %d, want 410", response.StatusCode)
		}
		response.Body.Close()
	}
	if calls.Load() != 2 {
		t.Fatalf("transport made %d calls, want exactly two caller requests and no retry", calls.Load())
	}
	if len(received) != 2 || received[0] != initial || received[1] != initial {
		t.Fatalf("release headers = %#v, expired pin must not fall back", received)
	}
	if release, _ := transport.Release(); release != initial {
		t.Fatalf("expired response changed release to %q", release)
	}
}

func TestGregaleClientReleaseTransportClearStartsNewDiscovery(t *testing.T) {
	const nextRelease = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	var received []string
	transport, err := NewGregaleClientReleaseTransport(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		received = append(received, req.Header.Get(GregaleReleaseHeader))
		return gregaleTestResponse(http.StatusOK, nextRelease), nil
	}), GregaleClientReleaseOptions{
		ManagedOrigins: []string{"https://api.example.com"},
		InitialRelease: testGregaleReleaseID,
	})
	if err != nil {
		t.Fatal(err)
	}
	transport.ClearRelease()
	if release, ok := transport.Release(); ok || release != "" {
		t.Fatalf("Release after clear = %q, %v; want unknown", release, ok)
	}
	request, _ := http.NewRequest(http.MethodGet, "https://api.example.com/", nil)
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(received) != 1 || received[0] != "" {
		t.Fatalf("release headers after clear = %#v, want one unpinned discovery", received)
	}
	if release, ok := transport.Release(); !ok || release != nextRelease {
		t.Fatalf("Release after rediscovery = %q, %v", release, ok)
	}
}

func TestGregaleClientReleaseTransportSerializesInitialDiscovery(t *testing.T) {
	started := make(chan struct{})
	finishFirst := make(chan struct{})
	var calls atomic.Int32
	var secondRelease string
	transport, err := NewGregaleClientReleaseTransport(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-finishFirst
			return gregaleTestResponse(http.StatusOK, testGregaleReleaseID), nil
		}
		secondRelease = req.Header.Get(GregaleReleaseHeader)
		return gregaleTestResponse(http.StatusOK, testGregaleReleaseID), nil
	}), GregaleClientReleaseOptions{ManagedOrigins: []string{"https://api.example.com"}})
	if err != nil {
		t.Fatal(err)
	}

	firstDone := make(chan error, 1)
	first, _ := http.NewRequest(http.MethodGet, "https://api.example.com/first", nil)
	go func() {
		response, roundTripErr := transport.RoundTrip(first)
		if response != nil {
			response.Body.Close()
		}
		firstDone <- roundTripErr
	}()
	<-started

	waitContext := &gregaleWaitSignalContext{Context: context.Background(), waiting: make(chan struct{})}
	second, _ := http.NewRequestWithContext(waitContext, http.MethodGet, "https://api.example.com/second", nil)
	secondDone := make(chan error, 1)
	go func() {
		response, roundTripErr := transport.RoundTrip(second)
		if response != nil {
			response.Body.Close()
		}
		secondDone <- roundTripErr
	}()
	<-waitContext.waiting
	if got := calls.Load(); got != 1 {
		t.Fatalf("discovery made %d underlying requests before release selection, want 1", got)
	}
	close(finishFirst)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 || secondRelease != testGregaleReleaseID {
		t.Fatalf("underlying calls = %d, second release = %q; want 2 and captured release", got, secondRelease)
	}
}

func TestGregaleClientReleaseTransportWaiterCanBeCanceled(t *testing.T) {
	started := make(chan struct{})
	finishFirst := make(chan struct{})
	var calls atomic.Int32
	transport, err := NewGregaleClientReleaseTransport(roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		calls.Add(1)
		close(started)
		<-finishFirst
		return gregaleTestResponse(http.StatusOK, testGregaleReleaseID), nil
	}), GregaleClientReleaseOptions{ManagedOrigins: []string{"https://api.example.com"}})
	if err != nil {
		t.Fatal(err)
	}

	firstDone := make(chan error, 1)
	first, _ := http.NewRequest(http.MethodGet, "https://api.example.com/first", nil)
	go func() {
		response, roundTripErr := transport.RoundTrip(first)
		if response != nil {
			response.Body.Close()
		}
		firstDone <- roundTripErr
	}()
	<-started

	baseContext, cancel := context.WithCancel(context.Background())
	waitContext := &gregaleWaitSignalContext{Context: baseContext, waiting: make(chan struct{})}
	second, _ := http.NewRequestWithContext(waitContext, http.MethodGet, "https://api.example.com/second", nil)
	secondDone := make(chan error, 1)
	go func() {
		_, roundTripErr := transport.RoundTrip(second)
		secondDone <- roundTripErr
	}()
	<-waitContext.waiting
	cancel()
	if err := <-secondDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter error = %v, want context.Canceled", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("canceled waiter reached base transport; calls = %d", got)
	}
	close(finishFirst)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
}

func TestNewGregaleClientReleaseTransportValidatesOptions(t *testing.T) {
	tests := []struct {
		name    string
		options GregaleClientReleaseOptions
	}{
		{name: "no origins"},
		{name: "non-http origin", options: GregaleClientReleaseOptions{ManagedOrigins: []string{"ws://api.example.com"}}},
		{name: "invalid initial release", options: GregaleClientReleaseOptions{ManagedOrigins: []string{"https://api.example.com"}, InitialRelease: "release-182"}},
		{name: "origin with user information", options: GregaleClientReleaseOptions{ManagedOrigins: []string{"https://user@api.example.com"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewGregaleClientReleaseTransport(nil, test.options); err == nil {
				t.Fatal("constructor succeeded, want validation error")
			}
		})
	}
}

type gregaleWaitSignalContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *gregaleWaitSignalContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}
