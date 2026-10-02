package main

import (
	"context"
	"encoding/json"
	"errors"
	"filippo.io/age"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	commitwork "github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/commitmanaged"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCommitPostgresToAPIHandoff(t *testing.T) {
	t.Setenv("FAAS_COMMIT_API_ENABLED", "true")
	e := setupPGHandler(t, api.PlanPro)
	created := e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "commit-worker"}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("app: %d %s", created.Code, created.Body.String())
	}
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &app); err != nil {
		t.Fatal(err)
	}
	otherAppResponse := e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "commit-other"}, nil)
	var otherApp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(otherAppResponse.Body.Bytes(), &otherApp); err != nil || otherAppResponse.Code != http.StatusCreated {
		t.Fatalf("other app: %d %s (%v)", otherAppResponse.Code, otherAppResponse.Body.String(), err)
	}
	if _, err := state.NewPgStore(e.pool).UpsertExclusiveWorkPolicy(t.Context(), e.acct.ID, exclusivework.Policy{
		Name: "orders", Scope: "account", Contention: "queue", MemberAppIDs: []string{app.ID, otherApp.ID}, LeaseSeconds: 15, MaxAttemptSeconds: 60, MaxAttempts: 3, RetryAfterSeconds: 7,
	}); err != nil {
		t.Fatal(err)
	}
	missingPolicy := e.do(t, http.MethodPost, "/v1/apps/commit-worker/commit-sources", map[string]string{"name": "missing-policy"}, nil)
	if missingPolicy.Code != http.StatusBadRequest {
		t.Fatalf("source without managed policy: %d", missingPolicy.Code)
	}
	source := e.do(t, http.MethodPost, "/v1/apps/commit-worker/commit-sources", map[string]string{"name": "orders", "operation_policy": "orders"}, nil)
	if source.Code != http.StatusCreated {
		t.Fatalf("source: %d %s", source.Code, source.Body.String())
	}
	var src struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(source.Body.Bytes(), &src); err != nil {
		t.Fatal(err)
	}
	conflict := e.do(t, http.MethodPost, "/v1/apps/commit-other/commit-sources", map[string]string{"name": "orders", "operation_policy": "orders"}, nil)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("source destination conflict: %d %s", conflict.Code, conflict.Body.String())
	}
	var problem struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(conflict.Body.Bytes(), &problem); err != nil || problem.Code != "commit_source_name_conflict" {
		t.Fatalf("source conflict problem: %s (%v)", conflict.Body.String(), err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	previousRecipient := outboundCredentialRecipient
	outboundCredentialRecipient = func() *age.X25519Recipient { return identity.Recipient() }
	t.Cleanup(func() { outboundCredentialRecipient = previousRecipient })
	credential := e.do(t, http.MethodPut, "/v1/commit-sources/"+src.ID+"/connection", map[string]string{"connection_url": "postgres://relay:private-password@db.example.com/customer?sslmode=verify-full"}, nil)
	if credential.Code != http.StatusNoContent || credential.Body.Len() != 0 {
		t.Fatalf("credential registration: %d", credential.Code)
	}
	customer := pgtest.OpenDatabase(t)
	ctx := context.Background()
	if _, err := customer.Exec(ctx, commitwork.Schema); err != nil {
		t.Fatal(err)
	}
	if _, err := customer.Exec(ctx, `INSERT INTO public.gregale_commit_binding(source_id) VALUES($1::uuid)`, src.ID); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(e.h)
	defer server.Close()
	acceptor := &commitwork.HTTPAcceptor{URL: server.URL + "/v1/commit-sources/" + src.ID + "/events", Token: e.key}
	event := commitwork.Event{ID: uuid.NewString(), Type: "order.created", Data: []byte(`{"order_id":"one"}`)}
	tx, err := customer.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := commitwork.Insert(ctx, tx, event); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	relay := commitwork.Relay{Pool: customer, Acceptor: acceptor}
	if n, err := relay.Tick(ctx); err != nil || n != 1 {
		t.Fatalf("handoff: %d %v", n, err)
	}
	first, err := acceptor.Accept(ctx, event)
	if err != nil {
		t.Fatal(err)
	}
	second, err := acceptor.Accept(ctx, event)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first.OperationID == "" || first.InvocationID != "" {
		t.Fatalf("unstable receipt: %+v %+v", first, second)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE apps SET streaming_enabled=false WHERE account_id=$1::uuid`, e.acct.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE accounts SET plan='free' WHERE id=$1::uuid`, e.acct.ID); err != nil {
		t.Fatal(err)
	}
	downgradedReplay, err := acceptor.Accept(ctx, event)
	if err != nil || downgradedReplay != first {
		t.Fatalf("plan downgrade lost accepted receipt: %+v (%v)", downgradedReplay, err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE accounts SET plan='pro' WHERE id=$1::uuid`, e.acct.ID); err != nil {
		t.Fatal(err)
	}
	pause := e.do(t, http.MethodPatch, "/v1/commit-sources/"+src.ID, map[string]bool{"enabled": false}, nil)
	if pause.Code != http.StatusOK {
		t.Fatalf("pause: %d %s", pause.Code, pause.Body.String())
	}
	replayed, err := acceptor.Accept(ctx, event)
	if err != nil || replayed != first {
		t.Fatalf("paused replay: %+v %v", replayed, err)
	}
	newEvent := event
	newEvent.ID = uuid.NewString()
	if _, err := acceptor.Accept(ctx, newEvent); err == nil {
		t.Fatal("paused source accepted new event")
	}
	resume := e.do(t, http.MethodPatch, "/v1/commit-sources/"+src.ID, map[string]bool{"enabled": true}, nil)
	if resume.Code != http.StatusOK {
		t.Fatalf("resume: %d %s", resume.Code, resume.Body.String())
	}
	if _, err := acceptor.Accept(ctx, newEvent); err != nil {
		t.Fatalf("resumed acceptance: %v", err)
	}
	status := e.do(t, http.MethodGet, "/v1/commit-sources/"+src.ID+"/events/"+event.ID, nil, nil)
	if status.Code != http.StatusOK {
		t.Fatalf("receipt lookup: %d %s", status.Code, status.Body.String())
	}
	invocation := e.do(t, http.MethodGet, "/v1/invocations/"+first.OperationID, nil, nil)
	if invocation.Code != http.StatusNotFound {
		t.Fatalf("managed operation created a legacy invocation: %d %s", invocation.Code, invocation.Body.String())
	}
	operation := e.do(t, http.MethodGet, "/v1/operations/"+first.OperationID, nil, nil)
	var op api.CommitOperationResponse
	if err := json.Unmarshal(operation.Body.Bytes(), &op); err != nil || operation.Code != http.StatusOK || op.State != "pending" || op.ReceiptID != first.ID {
		t.Fatalf("operation status: %d %+v %v", operation.Code, op, err)
	}
	managedEvent := event
	managedEvent.ID = uuid.NewString()
	tx, err = customer.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := commitwork.Insert(ctx, tx, managedEvent); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	manager := commitmanaged.Manager{Store: state.NewPgStore(e.pool), Identities: []*age.X25519Identity{identity}, Open: func(ctx context.Context, _ string, _ commitwork.NetworkPolicy) (*pgxpool.Pool, error) {
		return pgxpool.NewWithConfig(ctx, customer.Config().Copy())
	}}
	if err := manager.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	managedReceipt, err := state.NewPgStore(e.pool).CommitReceiptByEvent(ctx, e.acct.ID, src.ID, managedEvent.ID)
	if err != nil {
		t.Fatalf("managed handoff: %v", err)
	}
	managedOperation, err := state.NewPgStore(e.pool).ExclusiveOperationByID(ctx, e.acct.ID, managedReceipt.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	policy := managedOperation.Policy
	if policy.MaxAttempts != 3 || policy.RetryAfterSeconds != 7 || managedReceipt.InvocationID != "" {
		t.Fatalf("managed handoff lost Operations policy: %+v", policy)
	}
	health := e.do(t, http.MethodGet, "/v1/commit-sources/"+src.ID, nil, nil)
	if health.Code != http.StatusOK {
		t.Fatalf("source health: %d", health.Code)
	}
	var snapshot api.CommitSourceResponse
	if err := json.Unmarshal(health.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.RelayStatus != "healthy" || snapshot.LastCheckedAt == nil || snapshot.PendingEvents == nil || *snapshot.PendingEvents != 0 || snapshot.BlockedEvents == nil || *snapshot.BlockedEvents != 0 {
		t.Fatalf("source health: %+v", snapshot)
	}
	blockedID := uuid.NewString()
	// The schema permits this string, but it is not a valid CloudEvents type.
	if _, err := customer.Exec(ctx, `INSERT INTO gregale_outbox(event_id,event_type,payload) VALUES($1::uuid,' ','{}')`, blockedID); err != nil {
		t.Fatal(err)
	}
	if err := manager.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	blocked := e.do(t, http.MethodGet, "/v1/commit-sources/"+src.ID+"/blocked-events", nil, nil)
	if blocked.Code != http.StatusOK {
		t.Fatalf("blocked listing: %d %s", blocked.Code, blocked.Body.String())
	}
	var list struct {
		Items []state.CommitBlockedEvent `json:"items"`
	}
	if err := json.Unmarshal(blocked.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || list.Items[0].EventID != blockedID {
		t.Fatalf("blocked events: %+v", list.Items)
	}
	if _, err := customer.Exec(ctx, `UPDATE gregale_outbox SET event_type='order.created' WHERE event_id=$1::uuid`, blockedID); err != nil {
		t.Fatal(err)
	}
	replay := e.do(t, http.MethodPost, "/v1/commit-sources/"+src.ID+"/events/"+blockedID+"/replay", nil, nil)
	if replay.Code != http.StatusAccepted {
		t.Fatalf("replay request: %d %s", replay.Code, replay.Body.String())
	}
	if err := manager.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := state.NewPgStore(e.pool).CommitReceiptByEvent(ctx, e.acct.ID, src.ID, blockedID); err != nil {
		t.Fatalf("blocked replay acceptance: %v", err)
	}
	// A second source using the same database cannot acquire this outbox.
	otherResponse := e.do(t, http.MethodPost, "/v1/apps/commit-worker/commit-sources", map[string]string{"name": "wrong-source", "operation_policy": "orders"}, nil)
	var other api.CommitSourceResponse
	if err := json.Unmarshal(otherResponse.Body.Bytes(), &other); err != nil || otherResponse.Code != http.StatusCreated {
		t.Fatalf("other source: %d %v", otherResponse.Code, err)
	}
	otherConnection := e.do(t, http.MethodPut, "/v1/commit-sources/"+other.ID+"/connection", map[string]string{"connection_url": "postgres://relay:private-password@db.example.com/customer?sslmode=verify-full"}, nil)
	if otherConnection.Code != http.StatusNoContent {
		t.Fatalf("other connection: %d", otherConnection.Code)
	}
	if err := manager.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	otherHealth := e.do(t, http.MethodGet, "/v1/commit-sources/"+other.ID, nil, nil)
	var rejected api.CommitSourceResponse
	if err := json.Unmarshal(otherHealth.Body.Bytes(), &rejected); err != nil || rejected.RelayStatus != "source_binding_unqualified" {
		t.Fatalf("duplicate database source: %+v %v", rejected, err)
	}
	if _, err := state.NewPgStore(e.pool).CommitReceiptByEvent(ctx, e.acct.ID, other.ID, managedEvent.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("another source accepted the event: %v", err)
	}

}
