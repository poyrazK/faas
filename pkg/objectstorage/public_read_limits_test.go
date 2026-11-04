// adr: 20261023223000

package objectstorage

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPublicReadLimiterBoundsEachBucket(t *testing.T) {
	l := newPublicReadLimiters()
	allowed := 0
	for range api.PublicObjectReadBurstPerBucket * 2 {
		if l.allow("hot") {
			allowed++
		}
	}
	if allowed < api.PublicObjectReadBurstPerBucket || allowed > api.PublicObjectReadBurstPerBucket+5 {
		t.Fatalf("allowed %d reads in one burst", allowed)
	}
	if !l.allow("quiet") {
		t.Fatal("one bucket's burst throttled another bucket")
	}
	for i := range api.MaxPublicObjectReadLimiters + 1 {
		l.allow(string(rune(i)))
	}
	if len(l.buckets) > api.MaxPublicObjectReadLimiters {
		t.Fatal("limiter map grew past its bound", len(l.buckets))
	}
}

func TestPublicReadRefusalStatus(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{
		{&state.ObjectStorageLimitError{Kind: "requests", Cause: state.ErrObjectBudget}, http.StatusTooManyRequests},
		{state.ErrObjectUsageStale, http.StatusServiceUnavailable},
		{errors.New("store down"), http.StatusServiceUnavailable},
	} {
		w := httptest.NewRecorder()
		writePublicReadRefusal(w, tc.err)
		if w.Code != tc.want {
			t.Fatalf("%v -> %d, want %d", tc.err, w.Code, tc.want)
		}
	}
}

func TestPublicCacheControlRevalidates(t *testing.T) {
	if publicCacheControl != "public, max-age=300, must-revalidate" {
		t.Fatalf("Cache-Control = %q", publicCacheControl)
	}
}
