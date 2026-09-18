//go:build linux

package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleEventPublishRequestFramesJSON(t *testing.T) {
	var got []byte
	req := httptest.NewRequest(http.MethodPost, eventPublishPath, bytes.NewBufferString(`{"id":"evt-1","source":"billing","type":"invoice.paid","data":{"amount":42}}`))
	rec := httptest.NewRecorder()
	handleEventPublishRequest(rec, req, func(body []byte) error {
		got = append([]byte(nil), body...)
		return nil
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
			handleEventPublishRequest(rec, req, func([]byte) error { t.Fatal("send called"); return nil })
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
		})
	}
}
