// spec: §4.1
package gateway

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestContentObserver_MultipleCacheControlValues(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []string
		want   string
	}{
		{"private second", []string{"public", "private"}, api.ContentOutcomeUncacheable},
		{"no-store second", []string{"max-age=60", "no-store"}, api.ContentOutcomeUncacheable},
		{"mixed case third", []string{"public", "max-age=60", "No-StOrE"}, api.ContentOutcomeUncacheable},
		{"public split fields", []string{"public", "max-age=60"}, api.ContentOutcomeFirst},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observer := newContentObserver(10)
			r := httptest.NewRequest(http.MethodGet, "/catalog", nil)
			tee := newContentTee(httptest.NewRecorder(), observer, "app-1", r)
			tee.Header()["Cache-Control"] = tc.values
			if _, err := tee.Write([]byte("origin response")); err != nil {
				t.Fatal(err)
			}
			if got := tee.finish(time.Now()); got != tc.want {
				t.Fatalf("outcome = %q, want %q", got, tc.want)
			}
			if tc.want == api.ContentOutcomeUncacheable && len(observer.entries) != 0 {
				t.Fatal("uncacheable response influenced future cache recommendations")
			}
		})
	}
}

func TestContentObserver_FailedWriteIsUncacheable(t *testing.T) {
	for _, tc := range []struct {
		name string
		n    int
		err  error
	}{
		{"closed connection", 0, io.ErrClosedPipe},
		{"partial write", 2, io.ErrClosedPipe},
		{"short write", 2, nil},
		{"full write with error", 4, io.ErrClosedPipe},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observer := newContentObserver(10)
			r := httptest.NewRequest(http.MethodGet, "/catalog", nil)
			live := &cacheSafetyWriteFailure{ResponseWriter: httptest.NewRecorder(), fail: true, n: tc.n, err: tc.err}
			tee := newContentTee(live, observer, "app-1", r)
			if n, err := tee.Write([]byte("body")); n != tc.n || !errors.Is(err, tc.err) {
				t.Fatalf("write = (%d, %v), want (%d, %v)", n, err, tc.n, tc.err)
			}
			live.fail = false
			if _, err := tee.Write([]byte("later write")); err != nil {
				t.Fatal(err)
			}
			if got := tee.finish(time.Now()); got != api.ContentOutcomeUncacheable || len(observer.entries) != 0 {
				t.Fatalf("incomplete response influenced cache recommendations: outcome=%q entries=%d", got, len(observer.entries))
			}
		})
	}
}
