package appevents_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appevents"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// countingPrepareStore counts the per-event reads a batch's preparation makes.
type countingPrepareStore struct {
	MemStore      *state.MemStore
	schemaLookups int
	acceptLookups int
}

func (c *countingPrepareStore) LookupEventSchemaForPublish(ctx context.Context, accountID, source, typ, version string) (bool, json.RawMessage, error) {
	c.schemaLookups++
	return c.MemStore.LookupEventSchemaForPublish(ctx, accountID, source, typ, version)
}

func (c *countingPrepareStore) LookupPublishedEventAcceptance(ctx context.Context, account string, payload []byte) (state.PublishedEventAcceptance, error) {
	c.acceptLookups++
	return c.MemStore.LookupPublishedEventAcceptance(ctx, account, payload)
}

func publishBatch(t *testing.T, service appevents.Service, acct state.Account, events []string) api.PublishEventBatchResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	service.PublishEventBatch(rec, httptest.NewRequest(http.MethodPost, "/v1/events:publish-batch", strings.NewReader(`{"events":[`+strings.Join(events, ",")+`]}`)), acct)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch=%d %s", rec.Code, rec.Body)
	}
	var response api.PublishEventBatchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

// A batch reads the schema registry once per source, type and version, and
// looks up prior acceptance only for an event it would otherwise reject.
func TestPublishEventBatchPreparesWithoutPerEventReads(t *testing.T) {
	mem := state.NewMemStore()
	counting := &countingPrepareStore{MemStore: mem}
	acct, err := mem.CreateAccount(t.Context(), "batch-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	service := appevents.Service{Host: auditHost{store: statetest.NewMem(counting)}}
	event := func(id, typ, data string) string {
		return fmt.Sprintf(`{"id":%q,"source":"orders","type":%q,"data":%s}`, id, typ, data)
	}
	var batch []string
	for i := range 50 {
		batch = append(batch, event(fmt.Sprintf("a-%d", i), "order.created", `{"n":1}`), event(fmt.Sprintf("b-%d", i), "order.paid", `{"n":1}`))
	}
	response := publishBatch(t, service, acct, batch)
	for _, result := range response.Results {
		if result.Status != "accepted" {
			t.Fatalf("result=%+v", result)
		}
	}
	if counting.schemaLookups != 2 || counting.acceptLookups != 0 {
		t.Fatalf("schema lookups=%d acceptance lookups=%d, want 2 and 0", counting.schemaLookups, counting.acceptLookups)
	}

	// Retries are duplicates found by acceptance itself.
	counting.schemaLookups, counting.acceptLookups = 0, 0
	response = publishBatch(t, service, acct, batch[:3])
	for _, result := range response.Results {
		if result.Status != "duplicate" {
			t.Fatalf("retry result=%+v", result)
		}
	}
	if counting.acceptLookups != 0 {
		t.Fatalf("acceptance lookups=%d on valid retries", counting.acceptLookups)
	}

	// Once a schema is required, a retained retry is still a duplicate, a
	// changed retry a conflict, and a new event rejected.
	if _, err = mem.PutEventSchema(t.Context(), state.EventSchema{AccountID: acct.ID, Source: "orders", Type: "order.created", Version: "v1", Schema: json.RawMessage(`{"type":"object"}`)}); err != nil {
		t.Fatal(err)
	}
	counting.schemaLookups, counting.acceptLookups = 0, 0
	response = publishBatch(t, service, acct, []string{batch[0], event("a-1", "order.created", `{"n":2}`), event("new", "order.created", `{"n":1}`)})
	if got := response.Results[0]; got.Status != "duplicate" {
		t.Fatalf("retained retry=%+v", got)
	}
	if got := response.Results[1]; got.Status != "rejected" || got.Problem == nil || got.Problem.Status != http.StatusConflict {
		t.Fatalf("changed retry=%+v", got)
	}
	if got := response.Results[2]; got.Status != "rejected" || got.Retryable || got.Problem == nil || got.Problem.Status != http.StatusUnprocessableEntity {
		t.Fatalf("new unversioned event=%+v", got)
	}
	if counting.schemaLookups != 1 || counting.acceptLookups != 3 {
		t.Fatalf("schema lookups=%d acceptance lookups=%d, want 1 and 3", counting.schemaLookups, counting.acceptLookups)
	}
}
