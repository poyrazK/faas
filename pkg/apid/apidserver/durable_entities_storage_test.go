// adr: 712
package apidserver

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
)

func TestDurableEntityAPIStorageCapPreservesAuthorizedReceiptReplay(t *testing.T) {
	e, _, dispatch, bucket := entityAPIFixture(t, false)
	path := "/v1/apps/entity-counter/entities/invoke"
	first := entityAPIResult(t, e.do(t, http.MethodPost, path, entityRequest("original"), nil))
	engine, err := durableentity.Open(t.Context(), bucket, durableentity.Options{RetainedBytesLimit: 1})
	if err != nil {
		t.Fatal(err)
	}
	e.s.durable.Engine = engine
	replayed := entityAPIResult(t, e.do(t, http.MethodPost, path, entityRequest("original"), nil))
	if !replayed.Replayed || replayed.Version != first.Version || dispatch.calls.Load() != 1 {
		t.Fatal("cap suppressed replay", replayed)
	}
	rec := e.do(t, http.MethodPost, path, entityRequest("rejected"), nil)
	var problem api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || rec.Code != http.StatusConflict || problem.Code != "durable_entity_storage_limit" || problem.Limit == nil || *problem.Limit != 1 || problem.Observed == nil || *problem.Observed <= 1 {
		t.Fatal(rec.Code, rec.Body.String(), err)
	}
	if strings.Contains(rec.Body.String(), "private-provider-detail") {
		t.Fatal("private provider detail exposed")
	}
	result := entityAPIResult(t, e.do(t, http.MethodPost, path, entityRequest("original"), nil))
	if !result.Replayed || result.Version != 1 || dispatch.calls.Load() != 2 {
		t.Fatal("failed transition changed state", result)
	}
}
