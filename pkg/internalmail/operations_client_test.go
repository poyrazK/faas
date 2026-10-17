package internalmail

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestOperatorClientUnixIsolationAndAmbiguousWrite(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "maild.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var status, calls atomic.Int32
	status.Store(200)
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != OperationsPath+"/retention/preview" || r.Header.Get("Authorization") != "Bearer "+operatorToken {
			t.Error("incorrect private request")
		}
		w.Header().Set("Location", "https://untrusted.example.test")
		w.WriteHeader(int(status.Load()))
		_, _ = io.WriteString(w, `{"private_diagnostic":"must-not-appear-in-errors"}`)
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	var enabled atomic.Bool
	enabled.Store(true)
	client, err := NewOperatorClient(socket, operatorToken, enabled.Load)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []int{302, 400, 401, 404, 409, 413, 500, 503} {
		status.Store(int32(code))
		before := calls.Load()
		if _, err := client.Request(t.Context(), http.MethodPost, OperationsPath+"/retention/preview", struct{}{}); err == nil || strings.Contains(err.Error(), "private_diagnostic") || strings.Contains(err.Error(), operatorToken) {
			t.Fatal("error leaked or non-commit accepted", code, err)
		}
		if calls.Load() != before+1 {
			t.Fatal("client retried ambiguous write or followed redirect")
		}
	}
	status.Store(200)
	raw, err := client.Request(t.Context(), http.MethodPost, OperationsPath+"/retention/preview", struct{}{})
	if err != nil || !json.Valid(raw) {
		t.Fatal(err)
	}
	before := calls.Load()
	for _, path := range []string{APIPath + "/messages", "//external.test", "https://external.test", "/internal/v1/mail/operations/../reports"} {
		if _, err := client.Request(t.Context(), http.MethodPost, path, nil); !errors.Is(err, ErrInvalid) {
			t.Fatal("operator client permitted other route", err)
		}
	}
	enabled.Store(false)
	if _, err := client.Request(t.Context(), http.MethodPost, OperationsPath+"/retention/preview", make(chan int)); !errors.Is(err, ErrDisabled) {
		t.Fatal("disabled client processed request", err)
	}
	if calls.Load() != before {
		t.Fatal("disabled/invalid client contacted socket")
	}
	if _, err := NewOperatorClient("bad", "bad", enabled.Load); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
}

func TestOperatorClientBoundsResponseAndDoesNotLeakTransportError(t *testing.T) {
	client, err := NewOperatorClient(filepath.Join(t.TempDir(), "missing.sock"), operatorToken, func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Request(t.Context(), http.MethodGet, OperationsPath+"/messages", nil); err == nil || strings.Contains(err.Error(), "missing.sock") || strings.Contains(err.Error(), operatorToken) {
		t.Fatal("transport error exposed private configuration", err)
	}
	client.client.Transport = operatorRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", api.InternalMailMaxOperatorResponseBytes+1)))}, nil
	})
	if _, err := client.Request(t.Context(), http.MethodGet, OperationsPath+"/messages", nil); err == nil {
		t.Fatal("unbounded response accepted")
	}
}

type operatorRoundTrip func(*http.Request) (*http.Response, error)

func (f operatorRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
