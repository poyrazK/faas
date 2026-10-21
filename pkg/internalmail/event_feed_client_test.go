package internalmail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestPGEventFeedHTTPRejectsCallerSelectedProgress(t *testing.T) {
	f := eventFeedFixture(t)
	feedTarget(t, f, "auth")
	publishFeed(t, f)
	h, err := Handler(f.service, testToken)
	if err != nil {
		t.Fatal(err)
	}
	base, _ := EventConsumerRoute("auth", "effects")
	call := func(path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+producerToken)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, body := range []string{`{"limit":0}`, `{"limit":101}`, `{"cursor":"` + uuid.NewString() + `"}`, `{} {}`, `[]`} {
		if w := call(base+"/pull", body); w.Code != 400 {
			t.Fatal("invalid pull accepted", w.Code, body)
		}
	}
	if w := call(base+"/pull?after=arbitrary", `{}`); w.Code != 400 {
		t.Fatal("query selected checkpoint", w.Code)
	}
	if _, err := f.store.eventConsumerStatus(t.Context(), "auth", "effects"); !errors.Is(err, ErrNotFound) {
		t.Fatal("invalid request registered consumer")
	}
	w := call(base+"/pull", `{}`)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	var batch EventFeedBatch
	if json.Unmarshal(w.Body.Bytes(), &batch) != nil {
		t.Fatal("invalid batch response")
	}
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"receipt":"` + batch.Receipt + `","generation":1,"cursor":"` + batch.NextCursor + `"}`, 400},
		{`{"receipt":"` + batch.Receipt + `","generation":2}`, 409},
		{`{"receipt":"` + uuid.NewString() + `","generation":1}`, 404},
	} {
		if w := call(base+"/ack", tc.body); w.Code != tc.status {
			t.Fatal("invalid progress request accepted", w.Code, tc.status)
		}
	}
	if status := feedStatus(t, f, "auth", "effects"); status.Cursor != "" || status.PendingReceipt != batch.Receipt {
		t.Fatal("invalid acknowledgment advanced cursor")
	}
	input, _ := json.Marshal(EventFeedAckInput{Receipt: batch.Receipt, Generation: batch.Generation})
	if w := call(base+"/ack", string(input)); w.Code != 200 {
		t.Fatal("valid acknowledgment rejected", w.Code)
	}
	other := pullFeed(t, f, "auth", "independent", 100)
	if _, err := f.service.AckEventFeed(t.Context(), "auth", "effects", EventFeedAckInput{Receipt: other.Receipt, Generation: other.Generation}); !errors.Is(err, ErrNotFound) {
		t.Fatal("receipt crossed consumer scope", err)
	}
	if bytes.Contains(w.Body.Bytes(), []byte("Secret verification")) {
		t.Fatal("response leaked payload")
	}
}

func TestPGEventFeedClientLostResponsesAndHandlerRecovery(t *testing.T) {
	f := eventFeedFixture(t)
	feedTarget(t, f, "auth")
	publishFeed(t, f)
	h, err := Handler(f.service, testToken)
	if err != nil {
		t.Fatal(err)
	}
	var dropPull, dropAck atomic.Bool
	var requests atomic.Int32
	dropPull.Store(true)
	socket := filepath.Join(t.TempDir(), "maild.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		drop := strings.HasSuffix(r.URL.Path, "/pull") && dropPull.Swap(false) || strings.HasSuffix(r.URL.Path, "/ack") && dropAck.Swap(false)
		if !drop {
			h.ServeHTTP(w, r)
			return
		}
		recorder := httptest.NewRecorder()
		h.ServeHTTP(recorder, r)
		if recorder.Code != 200 {
			t.Errorf("commit failed: HTTP %d", recorder.Code)
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	client, err := NewEventFeedClient(socket, "auth", producerToken, func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Pull(t.Context(), "effects", 100); err == nil || requests.Load() != 1 {
		t.Fatal("lost pull was acknowledged or retried", err)
	}
	saved := feedStatus(t, f, "auth", "effects")
	if saved.PendingReceipt == "" || saved.Cursor != "" {
		t.Fatal("pull not durable before response")
	}
	b, err := client.Pull(t.Context(), "effects", 1)
	if err != nil || b.Receipt != saved.PendingReceipt || len(b.Events) != 2 {
		t.Fatal("lost response changed batch", b, err)
	}
	effects := map[string]bool{}
	calls := map[string]int{}
	fail := true
	handle := func(_ context.Context, e DeliveryEvent) error {
		calls[e.ID]++
		if fail && e.ID == b.Events[1].ID {
			return errors.New("fixture consumer unavailable")
		}
		effects[e.ID] = true
		return nil
	}
	if _, err := client.ConsumeOnce(t.Context(), "effects", 100, handle); err == nil {
		t.Fatal("failed handler acknowledged batch")
	}
	if current := feedStatus(t, f, "auth", "effects"); current.Cursor != "" || current.PendingReceipt != b.Receipt {
		t.Fatal("failed handler moved checkpoint")
	}
	fail = false
	dropAck.Store(true)
	if _, err := client.ConsumeOnce(t.Context(), "effects", 100, handle); err == nil {
		t.Fatal("lost ack response was acknowledged")
	}
	if len(effects) != 2 || calls[b.Events[0].ID] != 2 {
		t.Fatal("retry did not reuse stable deduplication identities")
	}
	if current := feedStatus(t, f, "auth", "effects"); current.Cursor != b.NextCursor || current.PendingReceipt != "" {
		t.Fatal("ack not durable before response")
	}
	ack, err := client.Ack(t.Context(), "effects", EventFeedAckInput{Receipt: b.Receipt, Generation: b.Generation})
	if err != nil || ack.Cursor != b.NextCursor {
		t.Fatal("lost ack could not replay", err)
	}
	if n, err := client.ConsumeOnce(t.Context(), "effects", 100, handle); err != nil || n != 0 {
		t.Fatal("completed consumer replayed events", n, err)
	}
}

func TestEventFeedClientDefaultOffBoundsRedirectsAndPrivateErrors(t *testing.T) {
	if _, err := NewEventFeedClient("bad", "bad", "bad", func() bool { return false }); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	var gate atomic.Bool
	client := &EventFeedClient{enabled: gate.Load}
	if _, err := client.Pull(t.Context(), "bad", 0); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	if _, err := client.ConsumeOnce(t.Context(), "bad", 0, func(context.Context, DeliveryEvent) error { t.Fatal("disabled client called handler"); return nil }); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	socket := filepath.Join(t.TempDir(), "maild.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	var status atomic.Int32
	status.Store(302)
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+producerToken {
			t.Error("wrong private credential")
		}
		w.Header().Set("Location", "https://untrusted.example.test")
		w.WriteHeader(int(status.Load()))
		_, _ = io.WriteString(w, `{"private_diagnostic":"never include in errors"}`)
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	t.Setenv("HTTP_PROXY", "http://untrusted.example.test:1234")
	gate.Store(true)
	client, err = NewEventFeedClient(socket, "auth", producerToken, gate.Load)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []int{302, 401, 404, 409, 413, 429, 500, 503} {
		status.Store(int32(code))
		before := requests.Load()
		_, err := client.Pull(t.Context(), "effects", 1)
		if err == nil || strings.Contains(err.Error(), "private_diagnostic") || strings.Contains(err.Error(), producerToken) || requests.Load() != before+1 {
			t.Fatal("client followed redirect, retried or exposed diagnostics", code, err)
		}
	}
	before := requests.Load()
	gate.Store(false)
	if _, err := client.Ack(t.Context(), "bad", EventFeedAckInput{}); !errors.Is(err, ErrDisabled) || requests.Load() != before {
		t.Fatal("disabled client performed IO", err)
	}
	gate.Store(true)
	client.client.Transport = operatorRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", api.InternalMailMaxOperatorResponseBytes+1)))}, nil
	})
	if _, err := client.Pull(t.Context(), "effects", 1); !errors.Is(err, ErrInvalid) {
		t.Fatal("oversize response accepted", err)
	}
}
