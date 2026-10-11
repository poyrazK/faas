package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/object"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

// Prod hunt #9: with object storage switched off by the operator,
// `gregale add bucket` answered "Object storage is unavailable; retry later."
// That reads as a transient outage; retrying can never succeed.
func TestCreateBucketWhenObjectStorageDisabledSaysNotEnabled(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "bucket-off")
	rec := e.do(t, http.MethodPost, "/v1/apps/bucket-off/buckets", map[string]any{"name": "assets"}, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var problem api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != "object_storage_unavailable" || !strings.Contains(problem.Detail, "not enabled on this Gregale installation") || strings.Contains(problem.Detail, "retry later") {
		t.Fatalf("problem = %+v", problem)
	}
	if !errors.Is(object.ErrObjectStorageDisabled, objectstorage.ErrUnavailable) {
		t.Fatal("disabled sentinel must still match objectstorage.ErrUnavailable")
	}
}
