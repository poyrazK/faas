// adr: 521
package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemOperationOperatorHistoryCursorRolesAndRetention(t *testing.T) {
	store := NewMemStore()
	account, tenant, app := uuid.NewString(), uuid.NewString(), uuid.NewString()
	now := time.Now().UTC().Truncate(time.Microsecond)
	data := store.operationMemoryLocked()
	for i := range 5 {
		id := uuid.NewString()
		op := Operation{AccountID: account, PlatformTenantID: tenant, AppID: app, Scope: "production", OperationResponse: api.OperationResponse{ID: id, Name: "export", State: api.OperationSucceeded, CreatedAt: now.Add(time.Duration(i) * time.Second), ExpiresAt: now.Add(time.Hour)}}
		switch i {
		case 0:
			op.AccountID = uuid.NewString()
		case 1:
			op.Scope = "staging"
		case 2:
			op.ExpiresAt = now.Add(-time.Hour)
		}
		data.operations[id] = op
	}
	opts := api.OperationListOptions{AppID: app, Scope: "production", TenantID: tenant, Limit: 1}
	page, err := store.ListAccountOperations(context.Background(), account, opts)
	if err != nil || len(page.Operations) != 1 || page.NextCursor == "" {
		t.Fatalf("page %+v %v", page, err)
	}
	customer := opts
	customer.Cursor = page.NextCursor
	if _, err := store.ListPlatformTenantOperations(context.Background(), account, tenant, customer); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("operator cursor crossed into customer view: %v", err)
	}
	self, err := store.ListPlatformTenantOperations(context.Background(), account, tenant, opts)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(self)
	if strings.Contains(string(raw), "platform_tenant_id") {
		t.Fatal("customer projection broadened")
	}
	opts.Cursor = self.NextCursor
	if _, err := store.ListAccountOperations(context.Background(), account, opts); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("customer cursor crossed into operator view: %v", err)
	}
	opts.Cursor = page.NextCursor
	opts.TenantID = ""
	if _, err := store.ListAccountOperations(context.Background(), account, opts); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("filter mutation accepted: %v", err)
	}
	opts.Cursor = ""
	opts.TenantID = "invalid"
	if _, err := store.ListAccountOperations(context.Background(), account, opts); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("malformed tenant accepted: %v", err)
	}
	opts.TenantID = strings.ReplaceAll(tenant, "-", "")
	page, err = store.ListAccountOperations(context.Background(), account, opts)
	if err != nil || len(page.Operations) != 1 {
		t.Fatalf("canonical tenant selector: %+v %v", page, err)
	}
}

func TestMemOperationExecutionProjectionAndCleanup(t *testing.T) {
	store := NewMemStore()
	account, id, invID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	now := time.Now().UTC()
	data := store.operationMemoryLocked()
	data.operations[id] = Operation{AccountID: account, OperationResponse: api.OperationResponse{ID: id, State: api.OperationSucceeded, ExpiresAt: now.Add(time.Hour)}}
	data.executions[invID] = id
	data.generations[invID] = 2
	store.invocations[invID] = Invocation{ID: invID, State: InvocationCompleted, Attempts: 3, CreatedAt: now, CompletedAt: &now, Payload: []byte(`{"secret":"input"}`), Headers: []byte(`{"capability":"secret"}`)}
	page, err := store.OperationExecutions(context.Background(), account, id, 0, 100)
	if err != nil || len(page.Executions) != 1 || page.Executions[0].Generation != 2 || page.Executions[0].Attempts != 3 {
		t.Fatalf("execution %+v %v", page, err)
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "capability") {
		t.Fatal("private invocation data leaked")
	}
	expectedCompleted := now
	*page.Executions[0].CompletedAt = now.Add(time.Hour)
	if !store.invocations[invID].CompletedAt.Equal(expectedCompleted) {
		t.Fatal("caller mutated retained invocation")
	}
	for _, query := range [][2]int{{-1, 1}, {0, -1}, {0, 101}, {1 << 32, 1}} {
		if _, err := store.OperationExecutions(context.Background(), account, id, query[0], query[1]); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("invalid bounds %v: %v", query, err)
		}
	}
	store.forgetOperationLocked(id)
	if len(data.generations) != 0 || len(data.executions) != 0 {
		t.Fatal("execution metadata leaked after owner cleanup")
	}
}
