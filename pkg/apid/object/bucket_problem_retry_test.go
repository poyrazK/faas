package object

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A deletion refused while other work settles, or while a bucket-wide
// inventory runs, is retryable: it answers 503 with Retry-After and its own
// code instead of the generic 409 "busy, conflicts, or bucket limit"
// (production-us 2026-11-07: the first deletion on a GCS bucket got ~50 s of
// opaque 409s). A real conflict keeps 409.
func TestBucketProblemAnswersTransientRefusalsAsRetryable(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		err        error
		status     int
		code       string
		retryAfter string
	}{
		{"inventory transition", errors.Join(state.ErrConflict, state.ErrObjectBucketTransition), 503, "object_storage_bucket_transition", strconv.Itoa(api.ObjectBucketTransitionRetryAfterSeconds)},
		{"unsettled work", errors.Join(state.ErrConflict, state.ErrObjectBucketBusy), 503, "object_storage_busy", "1"},
		{"plain conflict", state.ErrConflict, 409, "object_storage_conflict", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := httptest.NewRecorder()
			BucketProblem(w, tc.err)
			var problem api.Problem
			if err := json.Unmarshal(w.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if w.Code != tc.status || problem.Code != tc.code || w.Header().Get("Retry-After") != tc.retryAfter {
				t.Fatalf("got %d %q Retry-After=%q, want %d %q %q", w.Code, problem.Code, w.Header().Get("Retry-After"), tc.status, tc.code, tc.retryAfter)
			}
		})
	}
}
