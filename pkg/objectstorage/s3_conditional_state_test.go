package objectstorage

// adr: 678

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestS3ConditionalStateWriteNeverAutomaticallyRetries(t *testing.T) {
	for _, tc := range []struct {
		name, code, etag string
		status           int
		want             error
	}{
		{"create", "", `"new"`, http.StatusOK, nil},
		{"replace", "", `"new"`, http.StatusOK, nil},
		{"precondition", "PreconditionFailed", "", http.StatusPreconditionFailed, ErrPreconditionFailed},
		{"race", "ConditionalRequestConflict", "", http.StatusConflict, ErrConditionalConflict},
		{"unavailable", "SlowDown", "", http.StatusServiceUnavailable, ErrUnavailable},
		{"missing-ack", "", "", http.StatusOK, ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			expected := ""
			if tc.name == "replace" {
				expected = `"old"`
			}
			provider := listingFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPut || r.Header.Get("If-Match") != expected || expected == "" && r.Header.Get("If-None-Match") != "*" {
					t.Error("wrong conditional headers")
				}
				if r.Header.Get("Cache-Control") != "no-store" {
					t.Error("state can be cached")
				}
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "application/xml")
				if tc.etag != "" {
					w.Header().Set("ETag", tc.etag)
				}
				w.WriteHeader(tc.status)
				if tc.code != "" {
					_, _ = io.WriteString(w, "<Error><Code>"+tc.code+"</Code><Message>private-detail</Message></Error>")
				}
			})
			p, ok := provider.(ConditionalStateProvider)
			if !ok {
				t.Fatal("missing conditional-state capability")
			}
			etag, err := p.WriteStateObject(t.Context(), "private", "state/manifest", []byte(`{}`), expected)
			if !errors.Is(err, tc.want) || etag != tc.etag || calls.Load() != 1 {
				t.Fatal(etag, err, calls.Load())
			}
			if err != nil && strings.Contains(err.Error(), "private-detail") {
				t.Fatal("provider detail leaked")
			}
		})
	}
}

func TestS3ConditionalStateReadBindsBodyAndETagAndBoundsBytes(t *testing.T) {
	for _, tc := range []struct {
		name, body, etag string
		limit            int64
		want             error
	}{
		{"valid", `{"count":1}`, `"same-read"`, 128, nil},
		{"oversized", strings.Repeat("x", 129), `"same-read"`, 128, ErrUnavailable},
		{"missing-etag", `{}`, "", 128, ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			provider := listingFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet {
					t.Error("read used separate metadata request")
				}
				w.Header().Set("Content-Length", strconv.Itoa(len(tc.body)))
				if tc.etag != "" {
					w.Header().Set("ETag", tc.etag)
				}
				_, _ = io.WriteString(w, tc.body)
			})
			p, ok := provider.(ConditionalStateProvider)
			if !ok {
				t.Fatal("missing conditional-state capability")
			}
			body, version, err := p.ReadStateObject(t.Context(), "private", "state/manifest", tc.limit)
			if !errors.Is(err, tc.want) || calls.Load() != 1 {
				t.Fatal(string(body), version, err, calls.Load())
			}
			if err == nil && (string(body) != tc.body || version != tc.etag) {
				t.Fatal(string(body), version)
			}
		})
	}
}

func TestS3ConditionalStateMissingObject(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `<Error><Code>NoSuchKey</Code></Error>`)
	}))
	defer upstream.Close()
	config := testBackend()
	config.Endpoint = upstream.URL
	provider, err := NewS3(config, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := provider.(ConditionalStateProvider)
	if !ok {
		t.Fatal("missing capability")
	}
	if _, _, err := p.ReadStateObject(t.Context(), "private", "missing", 128); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
