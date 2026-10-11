// adr: 521
package state

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemOperationHistoryOwnershipPagingAndRetention(t *testing.T) {
	m := NewMemStore()
	account, tenant, app := uuid.NewString(), uuid.NewString(), uuid.NewString()
	now := time.Now().UTC().Truncate(time.Microsecond)
	put := func(id string, when time.Time, state api.OperationState, expiry time.Time) {
		m.operationMemoryLocked().operations[id] = Operation{AccountID: account, PlatformTenantID: tenant, AppID: app, Scope: "default",
			OperationResponse: api.OperationResponse{ID: id, Name: "export", Generation: 1, State: state, CreatedAt: when, UpdatedAt: when, ExpiresAt: expiry,
				Result: []byte(`{"private":"secret-result"}`), Artifacts: []api.OperationResultArtifact{{URI: "private-location"}}, Progress: &api.OperationProgress{Stage: "generating", Total: 10},
				CompletionDelivery: api.OperationDeliveryResponse{State: "pending", LastError: "secret-error", Attempts: 2}}, ExecutionCapabilityDigest: "secret-capability"}
	}
	// Same timestamp forces UUID tie-breaking rather than relying on time alone.
	ids := []string{"11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222", "33333333-3333-3333-3333-333333333333"}
	for _, id := range ids {
		put(id, now, api.OperationSucceeded, now.Add(time.Hour))
	}
	put(uuid.NewString(), now, api.OperationFailed, now.Add(-time.Hour)) // omit expired settled work
	active := uuid.NewString()
	put(active, now.Add(-time.Hour), api.OperationRunning, now.Add(-time.Minute)) // keep active work
	for _, field := range []string{"account", "tenant", "app", "scope"} {
		id := uuid.NewString()
		put(id, now, api.OperationAccepted, now.Add(time.Hour))
		op := m.operationData.operations[id]
		switch field {
		case "account":
			op.AccountID = uuid.NewString()
		case "tenant":
			op.PlatformTenantID = uuid.NewString()
		case "app":
			op.AppID = uuid.NewString()
		case "scope":
			op.Scope = "staging"
		}
		m.operationData.operations[id] = op
	}
	opts := api.OperationListOptions{AppID: app, Scope: "default", Limit: 1}
	page, err := m.ListPlatformTenantOperations(context.Background(), account, tenant, opts)
	if err != nil || len(page.Operations) != 1 || page.Operations[0].ID != ids[2] || page.NextCursor == "" {
		t.Fatalf("first page: %+v %v", page, err)
	}
	data, _ := json.Marshal(page)
	for _, secret := range []string{"secret", "private-location", "result", "capability", "last_error"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("summary leaked %s: %s", secret, data)
		}
	}
	page.Operations[0].Progress.Completed = 999
	op := m.operationData.operations[ids[2]]
	if op.Progress.Completed == 999 {
		t.Fatal("caller mutated stored progress")
	}
	op.UpdatedAt = now.Add(time.Hour)
	m.operationData.operations[ids[2]] = op
	put(uuid.NewString(), now.Add(time.Minute), api.OperationAccepted, now.Add(time.Hour)) // new insert cannot shift old pages
	opts.Cursor = page.NextCursor
	seen := []string{}
	for {
		page, err = m.ListPlatformTenantOperations(context.Background(), account, tenant, opts)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range page.Operations {
			seen = append(seen, row.ID)
		}
		if page.NextCursor == "" {
			break
		}
		opts.Cursor = page.NextCursor
	}
	if strings.Join(seen, ",") != strings.Join([]string{ids[1], ids[0], active}, ",") {
		t.Fatalf("unstable traversal: %v", seen)
	}
}

func TestOperationHistoryRejectsUnscopedAndForeignCursors(t *testing.T) {
	account, tenant, app := uuid.NewString(), uuid.NewString(), uuid.NewString()
	opts, cursor, err := prepareOperationHistory(account, tenant, api.OperationListOptions{AppID: app, Scope: "default", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	page := operationHistoryPage([]api.OperationSummary{{ID: uuid.NewString(), CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}, {}}, 1, cursor)
	opts.Cursor = page.NextCursor
	for _, variant := range []string{"app", "tenant", "account", "scope", "name", "state", "limit", "cursor", "missing scope", "all scope"} {
		t.Run(variant, func(t *testing.T) {
			copy, a, owner := opts, account, tenant
			switch variant {
			case "app":
				copy.AppID = uuid.NewString()
			case "tenant":
				owner = uuid.NewString()
			case "account":
				a = uuid.NewString()
			case "scope":
				copy.Scope = "staging"
			case "name":
				copy.Name = "another"
			case "state":
				copy.State = api.OperationRunning
			case "limit":
				copy.Limit = api.OperationHistoryPageMax + 1
			case "cursor":
				copy.Cursor = "invalid!"
			case "missing scope":
				copy.Scope = ""
			case "all scope":
				copy.Scope = api.EnvScopeAllSentinel
			}
			if _, _, err := prepareOperationHistory(a, owner, copy); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("accepted invalid selection: %v", err)
			}
		})
	}
	raw, _ := base64.RawURLEncoding.DecodeString(opts.Cursor)
	opts.Cursor = base64.RawURLEncoding.EncodeToString(append([]byte(`{"v":2,`), raw[1:]...))
	if _, _, err := prepareOperationHistory(account, tenant, opts); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal("duplicate cursor members accepted")
	}
}
