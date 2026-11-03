//go:build linux

package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestHandleEventPublishRequestFramesJSON(t *testing.T) {
	var got []byte
	req := httptest.NewRequest(http.MethodPost, eventPublishPath, bytes.NewBufferString(`{"id":"evt-1","source":"billing","type":"invoice.paid","data":{"amount":42}}`))
	rec := httptest.NewRecorder()
	handleEventPublishRequest(rec, req, func(body []byte) (api.EventPublishVerdict, error) {
		got = append([]byte(nil), body...)
		return api.EventPublishVerdict{Status: http.StatusAccepted}, nil
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	if string(got) != `{"id":"evt-1","source":"billing","type":"invoice.paid","data":{"amount":42}}` {
		t.Fatalf("framed body = %q", got)
	}
}

func TestHandleEventPublishRequestRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name   string
		method string
		body   string
		status int
	}{
		{name: "method", method: http.MethodGet, body: "{}", status: http.StatusMethodNotAllowed},
		{name: "empty", method: http.MethodPost, body: "", status: http.StatusBadRequest},
		{name: "invalid json", method: http.MethodPost, body: "not-json", status: http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, eventPublishPath, bytes.NewBufferString(tc.body))
			rec := httptest.NewRecorder()
			handleEventPublishRequest(rec, req, func([]byte) (api.EventPublishVerdict, error) {
				t.Fatal("send called")
				return api.EventPublishVerdict{}, nil
			})
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
		})
	}
}

// adr: 20261031102000 — the endpoint answers with vmmd's verdict instead of
// an unconditional 202.
func TestHandleEventPublishRequestReturnsTheVerdict(t *testing.T) {
	cases := []struct {
		name       string
		verdict    api.EventPublishVerdict
		err        error
		status     int
		code       string
		retryAfter string
	}{
		{name: "rate limited", verdict: api.EventPublishVerdict{Status: http.StatusTooManyRequests, Code: api.CodeEventPublishRateLimited, RetryAfterSeconds: 2},
			status: http.StatusTooManyRequests, code: api.CodeEventPublishRateLimited, retryAfter: "2"},
		{name: "paused", verdict: api.EventPublishVerdict{Status: http.StatusServiceUnavailable, Code: api.CodeEventsPaused, RetryAfterSeconds: 30},
			status: http.StatusServiceUnavailable, code: api.CodeEventsPaused, retryAfter: "30"},
		{name: "invalid", verdict: api.EventPublishVerdict{Status: http.StatusBadRequest, Code: "invalid_event"},
			status: http.StatusBadRequest, code: "invalid_event"},
		{name: "unconfirmed", err: fmt.Errorf("%w: read timeout", errEventPublishUnconfirmed),
			status: http.StatusServiceUnavailable, code: "event_publish_unconfirmed", retryAfter: "1"},
		{name: "unreachable", err: errors.New("connect: no such device"),
			status: http.StatusServiceUnavailable, code: "event_publish_unavailable", retryAfter: "1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, eventPublishPath, bytes.NewBufferString(`{"id":"evt-1"}`))
			rec := httptest.NewRecorder()
			handleEventPublishRequest(rec, req, func([]byte) (api.EventPublishVerdict, error) { return tc.verdict, tc.err })
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), `"`+tc.code+`"`) {
				t.Fatalf("response = %d %s, want %d %s", rec.Code, rec.Body.String(), tc.status, tc.code)
			}
			if got := rec.Header().Get("Retry-After"); got != tc.retryAfter {
				t.Fatalf("Retry-After = %q, want %q", got, tc.retryAfter)
			}
		})
	}
}

func TestEventPublishVerdictWire(t *testing.T) {
	frame := encodeEventPublishVerdictFrame([]byte(`{"id":"evt-1"}`))
	if frame[0] != eventPublishVerdictType || binary.BigEndian.Uint32(frame[1:5]) != 14 || string(frame[5:]) != `{"id":"evt-1"}` {
		t.Fatalf("frame = %v", frame)
	}
	reply := func(b []byte) func([]byte) error {
		return func(p []byte) error {
			if len(b) < len(p) {
				return errors.New("short reply")
			}
			copy(p, b)
			b = b[len(p):]
			return nil
		}
	}
	ok := append([]byte{0, 14}, []byte(`{"status":202}`)...)
	if verdict, err := readEventPublishVerdict(reply(ok)); err != nil || verdict.Status != http.StatusAccepted {
		t.Fatalf("verdict = %+v, %v", verdict, err)
	}
	for name, b := range map[string][]byte{
		"empty":     nil,
		"zero":      {0, 0},
		"too long":  {0xff, 0xff},
		"truncated": {0, 14, '{'},
		"no status": append([]byte{0, 2}, []byte(`{}`)...),
	} {
		if _, err := readEventPublishVerdict(reply(b)); !errors.Is(err, errEventPublishUnconfirmed) {
			t.Fatalf("%s reply = %v, want unconfirmed", name, err)
		}
	}
}
