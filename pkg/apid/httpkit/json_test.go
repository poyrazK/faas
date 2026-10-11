package httpkit

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteJSON(rec, http.StatusCreated, map[string]string{"ok": "yes"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != `{"ok":"yes"}` {
		t.Fatalf("body = %q", body)
	}
}

// chunked builds a request whose ContentLength is unknown, so only the
// MaxBytesReader (not the up-front ContentLength check) can enforce the cap.
func chunked(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	r.ContentLength = -1
	return r
}

func TestDecodeJSONSized(t *testing.T) {
	type payload struct {
		Slug string `json:"slug"`
	}
	tests := []struct {
		name    string
		req     func() *http.Request
		max     int64
		want    string
		wantErr func(error) bool
	}{
		{
			name: "valid",
			req: func() *http.Request {
				return httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"slug":"a"}`))
			},
			max:  64,
			want: "a",
		},
		{
			name: "unknown field",
			req: func() *http.Request {
				return httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"nope":1}`))
			},
			max:     64,
			wantErr: func(err error) bool { return err != nil && strings.Contains(err.Error(), "unknown field") },
		},
		{
			name: "trailing value",
			req: func() *http.Request {
				return httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"slug":"a"} {}`))
			},
			max:     64,
			wantErr: func(err error) bool { return err != nil && strings.Contains(err.Error(), "single JSON value") },
		},
		{
			name: "content length over cap",
			req: func() *http.Request {
				return httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"slug":"aaaaaaaa"}`))
			},
			max:     8,
			wantErr: isMaxBytes(8),
		},
		{
			name:    "chunked over cap after valid prefix",
			req:     func() *http.Request { return chunked(`{"slug":"a"}` + strings.Repeat(" ", 64)) },
			max:     16,
			wantErr: isMaxBytes(16),
		},
		{
			name:    "nil body",
			req:     func() *http.Request { r := httptest.NewRequest(http.MethodPost, "/", nil); r.Body = nil; return r },
			max:     64,
			wantErr: func(err error) bool { return errors.Is(err, io.EOF) },
		},
		{
			name: "non-positive cap falls back to default",
			req: func() *http.Request {
				return chunked(`{"slug":"` + strings.Repeat("x", int(DefaultJSONBodyMaxBytes)) + `"}`)
			},
			max:     0,
			wantErr: isMaxBytes(DefaultJSONBodyMaxBytes),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got payload
			err := DecodeJSONSized(tt.req(), &got, tt.max)
			if tt.wantErr != nil {
				if !tt.wantErr(err) {
					t.Fatalf("err = %v, did not match", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if got.Slug != tt.want {
				t.Fatalf("slug = %q, want %q", got.Slug, tt.want)
			}
		})
	}
}

func isMaxBytes(limit int64) func(error) bool {
	return func(err error) bool {
		var mbe *http.MaxBytesError
		return errors.As(err, &mbe) && mbe.Limit == limit
	}
}

func TestDecodeJSONLimit(t *testing.T) {
	tests := []struct {
		name       string
		req        func() *http.Request
		max        int64
		wantOK     bool
		wantStatus int
		wantCode   string
	}{
		{
			name:   "valid",
			req:    func() *http.Request { return httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"a":1}`)) },
			max:    64,
			wantOK: true,
		},
		{
			name: "content length over plan cap",
			req: func() *http.Request {
				return httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"a":123456789}`))
			},
			max:        8,
			wantStatus: http.StatusRequestEntityTooLarge,
			wantCode:   api.CodePlanSourceBytes,
		},
		{
			name:       "chunked over plan cap",
			req:        func() *http.Request { return chunked(`{"a":` + strings.Repeat("1", 64) + `}`) },
			max:        16,
			wantStatus: http.StatusRequestEntityTooLarge,
			wantCode:   api.CodePlanSourceBytes,
		},
		{
			name:       "chunked trailing bytes over plan cap",
			req:        func() *http.Request { return chunked(`{"a":1}` + strings.Repeat(" ", 64)) },
			max:        16,
			wantStatus: http.StatusRequestEntityTooLarge,
			wantCode:   api.CodePlanSourceBytes,
		},
		{
			name:       "empty body",
			req:        func() *http.Request { return httptest.NewRequest(http.MethodPost, "/", strings.NewReader("")) },
			max:        64,
			wantStatus: http.StatusBadRequest,
			wantCode:   api.CodeValidation,
		},
		{
			name:       "nil body",
			req:        func() *http.Request { r := httptest.NewRequest(http.MethodPost, "/", nil); r.Body = nil; return r },
			max:        64,
			wantStatus: http.StatusBadRequest,
			wantCode:   api.CodeValidation,
		},
		{
			name:       "unknown field",
			req:        func() *http.Request { return httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"b":1}`)) },
			max:        64,
			wantStatus: http.StatusBadRequest,
			wantCode:   api.CodeValidation,
		},
		{
			name: "trailing value",
			req: func() *http.Request {
				return httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"a":1} {"a":2}`))
			},
			max:        64,
			wantStatus: http.StatusBadRequest,
			wantCode:   api.CodeValidation,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			var dst struct {
				A int `json:"a"`
			}
			ok := DecodeJSONLimit(rec, tt.req(), &dst, tt.max)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (body %s)", ok, tt.wantOK, rec.Body.String())
			}
			if tt.wantOK {
				return
			}
			assertProblem(t, rec, tt.wantStatus, tt.wantCode)
		})
	}
}

func assertProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) api.Problem {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, status, rec.Body.String())
	}
	var p api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode problem: %v (body %s)", err, rec.Body.String())
	}
	if p.Code != code {
		t.Fatalf("code = %q, want %q", p.Code, code)
	}
	return p
}
