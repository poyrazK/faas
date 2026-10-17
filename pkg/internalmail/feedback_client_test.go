package internalmail

import (
	"bytes"
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

func TestFeedbackClientPreservesOriginalBytesAndDoesNotAcknowledgeFailure(t *testing.T) {
	var enabled atomic.Bool
	enabled.Store(true)
	socket := filepath.Join(t.TempDir(), "maild.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("original private signed report\r\n")
	var status atomic.Int32
	status.Store(http.StatusServiceUnavailable)
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, err := io.ReadAll(r.Body)
		if err != nil || !bytes.Equal(got, raw) || r.URL.Path != FeedbackPath+"/provider" || r.Header.Get("Authorization") != "Bearer "+feedbackToken || r.Header.Get("Content-Type") != "message/rfc822" {
			t.Error("collector changed report bytes or used an incorrect route")
		}
		w.Header().Set("Location", "https://untrusted.example.test")
		w.WriteHeader(int(status.Load()))
		_, _ = io.WriteString(w, "private diagnostic")
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	client, err := NewFeedbackClient(socket, "provider", feedbackToken, enabled.Load)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []int{400, 401, 404, 409, 413, 422, 429, 500, 503, 302} {
		status.Store(int32(code))
		if err := client.Submit(t.Context(), bytes.NewReader(raw)); err == nil || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), feedbackToken) {
			t.Fatal("uncommitted feedback acknowledged or diagnostics leaked", code, err)
		}
	}
	status.Store(200)
	if err := client.Submit(t.Context(), bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	enabled.Store(false)
	if err := client.Submit(t.Context(), forbiddenRead{t}); !errors.Is(err, ErrDisabled) {
		t.Fatal("disabled client read input", err)
	}
	if _, err := NewFeedbackClient("relative.sock", "../provider", "short", enabled.Load); !errors.Is(err, ErrDisabled) {
		t.Fatal("disabled constructor validated credentials", err)
	}
	enabled.Store(true)
	if _, err := NewFeedbackClient(socket, "../provider", feedbackToken, enabled.Load); !errors.Is(err, ErrInvalid) {
		t.Fatal("untrusted source path accepted", err)
	}
	if err := client.Submit(t.Context(), strings.NewReader(strings.Repeat("x", api.InternalMailMaxFeedbackBytes+1))); !errors.Is(err, ErrInvalid) {
		t.Fatal("oversized feedback accepted", err)
	}
}
