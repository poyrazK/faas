package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// uuidStringOf normalises either a canonical UUID (with hyphens) or a
// raw 32-char hex string into the canonical UUID form. The MemStore's
// newID returns hex; the PgStore returns canonical UUIDs. MemStore's
// parseSubjectID converts the hex back to canonical UUID bytes when
// storing the Subject, so ListEvents(subject=<hex>) returns rows whose
// Subject.String() always reports the canonical form regardless of
// which store produced it. (Same helper as pkg/sched/events_test.go.)
func uuidStringOf(s string) string {
	if strings.Contains(s, "-") {
		return s
	}
	if len(s) != 32 {
		return s
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return s
	}
	return uuid.UUID(b).String()
}

// --- tests -----------------------------------------------------------------

// TestAuditEvents_KeyMintEmitsEvent (IAM-4 / ADR-035) drives POST
// /v1/keys and asserts the events table got a row with kind=
// "key.created", data.key_id == the new key's id, and data.scopes ==
// the requested scope set. The shape mirrors schedd's
// TestEngineTransition_AppendsEvent — drive a happy-path mutation,
// assert the audit row landed.
func TestAuditEvents_KeyMintEmitsEvent(t *testing.T) {
	e := setup(t, api.PlanPro)
	body := api.CreateKeyRequest{Label: "audit-test", Scopes: []string{api.ScopeAppsRead}}
	rec := e.do(t, http.MethodPost, "/v1/keys", body, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/keys: code=%d body=%s", rec.Code, rec.Body.String())
	}

	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var found *state.Event
	for i := range rows {
		if rows[i].Kind == "key.created" {
			found = &rows[i]
			break
		}
	}
	found = mustAuditEvent(t, found, fmt.Sprintf("no key.created event row; rows=%+v", rows))
	if found.Subject == nil || found.Subject.String() != uuidStringOf(e.acct.ID) {
		t.Errorf("Subject = %v, want %s", found.Subject, uuidStringOf(e.acct.ID))
	}
	var data map[string]any
	if err := json.Unmarshal(found.Data, &data); err != nil {
		t.Fatalf("Data not valid JSON: %v", err)
	}
	if data["scopes"] == nil {
		t.Errorf("Data.scopes missing: %+v", data)
	}
	// data.key_id should be a 32-hex char (the new key's ID).
	kid, _ := data["key_id"].(string)
	if len(kid) != 32 {
		t.Errorf("Data.key_id = %q, want 32 hex chars", kid)
	}
}

// TestAuditEvents_KeyDeleteEmitsEvent drives DELETE /v1/keys/{id}
// and asserts a key.revoked row landed.
//
// IAM-5 (issue #189): the audit kind is `key.revoked` (not the
// legacy `key.deleted`) because the operation is now a soft
// revoke — the row stays in the table for audit lineage
// (rotated_from_id chain). The dashboard's "Keys" page filters
// by reason in data.reason ("manual" vs "rotation" vs
// "expired").
func TestAuditEvents_KeyDeleteEmitsEvent(t *testing.T) {
	e := setup(t, api.PlanPro)

	// Mint a second key we can delete.
	body := api.CreateKeyRequest{Label: "to-delete", Scopes: []string{api.ScopeAppsRead}}
	rec := e.do(t, http.MethodPost, "/v1/keys", body, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/keys: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var created api.APIKeyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Delete it.
	delRec := e.do(t, http.MethodDelete, "/v1/keys/"+created.ID, nil, nil)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("DELETE /v1/keys/%s: code=%d body=%s", created.ID, delRec.Code, delRec.Body.String())
	}

	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var found *state.Event
	for i := range rows {
		if rows[i].Kind == "key.revoked" {
			found = &rows[i]
			break
		}
	}
	found = mustAuditEvent(t, found, fmt.Sprintf("no key.revoked event row; rows=%+v", rows))
	var data map[string]any
	if err := json.Unmarshal(found.Data, &data); err != nil {
		t.Fatalf("Data not valid JSON: %v", err)
	}
	if data["key_id"] != created.ID {
		t.Errorf("Data.key_id = %v, want %s", data["key_id"], created.ID)
	}
	// IAM-5 (issue #189): the soft-revoke path stamps
	// data.reason = "manual"; rotation stamps "rotation"; the
	// lazy-expiry gate (state.AuthenticateKey) is the only
	// emitter of "expired". The dashboard's "Keys" page filters
	// by this field, so the contract is load-bearing. Pin it
	// here so a future refactor can't silently drop the field.
	if reason, _ := data["reason"].(string); reason != "manual" {
		t.Errorf("Data.reason = %q, want %q", reason, "manual")
	}
}

// TestAuditEvents_AccountDeletionScheduledEmitsEvent (IAM-4) drives
// DELETE /v1/account and asserts the events row carries kind =
// "account.deletion_scheduled" with data.via == "rest". The dashboard
// form path (data.via == "dashboard") is covered by
// TestAuditEvents_DashboardDeleteEmitsEventWithViaDashboard below —
// the seam is the scheduleDeletion(ctx, acct, via) parameter at
// handlers_account.go:90 which both call sites pass into.
func TestAuditEvents_AccountDeletionScheduledEmitsEvent(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, http.MethodDelete, "/v1/account", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE /v1/account: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var found *state.Event
	for i := range rows {
		if rows[i].Kind == "account.deletion_scheduled" {
			found = &rows[i]
			break
		}
	}
	found = mustAuditEvent(t, found, fmt.Sprintf("no account.deletion_scheduled event row; rows=%+v", rows))
	var data map[string]any
	if err := json.Unmarshal(found.Data, &data); err != nil {
		t.Fatalf("Data not valid JSON: %v", err)
	}
	if data["via"] != "rest" {
		t.Errorf("Data.via = %v, want rest", data["via"])
	}
}

// TestAuditEvents_ListEndpointRespectsKindPrefixFilter drives the
// GET /v1/audit-events customer surface with kind_prefix="key." and
// asserts only key.* rows come back. Also covers the list endpoint
// shape (limit echo, newest-first ordering).
func TestAuditEvents_ListEndpointRespectsKindPrefixFilter(t *testing.T) {
	e := setup(t, api.PlanPro)
	// Mint two keys → 2x key.created rows.
	for _, label := range []string{"first", "second"} {
		rec := e.do(t, http.MethodPost, "/v1/keys", api.CreateKeyRequest{Label: label, Scopes: []string{api.ScopeAppsRead}}, nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("POST /v1/keys: code=%d body=%s", rec.Code, rec.Body.String())
		}
	}

	// Query BEFORE scheduling deletion: a deleted_pending account is
	// gated by the billing-past-due middleware so the read returns 402.
	rec := e.do(t, http.MethodGet, "/v1/audit-events?kind_prefix=key.", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/audit-events: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var list api.ListAuditEventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list response: %v body=%s", err, rec.Body.String())
	}
	if list.Limit != 50 {
		t.Errorf("Limit = %d, want 50", list.Limit)
	}
	if len(list.Events) != 2 {
		t.Fatalf("got %d events, want 2 (key.created x2); events=%+v", len(list.Events), list.Events)
	}
	for i, ev := range list.Events {
		if ev.Kind != "key.created" {
			t.Errorf("events[%d].Kind = %q, want key.created", i, ev.Kind)
		}
		if !strings.HasPrefix(ev.Kind, "key.") {
			t.Errorf("events[%d].Kind = %q, missing key. prefix", i, ev.Kind)
		}
	}
}

type failingCustomerEventLister struct {
	state.Store
	err error
}

func (s failingCustomerEventLister) ListCustomerEvents(context.Context, state.CustomerEventFilter) ([]state.Event, error) {
	return nil, s.err
}

func TestAuditEvents_ListStoreFailureLogsCauseButKeepsClientErrorGeneric(t *testing.T) {
	const accountID = "00000000-0000-4000-8000-000000000001"
	queryErr := errors.New("sentinel store query failure")
	var logs bytes.Buffer
	srv := &server{
		store: failingCustomerEventLister{Store: state.NewMemStore(), err: queryErr},
		log:   slog.New(slog.NewJSONHandler(&logs, nil)),
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/audit-events?kind_prefix=build", nil)
	rec := httptest.NewRecorder()
	srv.listAuditEvents(rec, req, state.Account{ID: accountID})

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "could not list audit events") || strings.Contains(rec.Body.String(), queryErr.Error()) {
		t.Fatalf("client response should stay generic and not expose store details: %s", rec.Body.String())
	}
	for _, want := range []string{
		"list audit events query failed",
		queryErr.Error(),
		accountID,
		`"kind_prefix_set":true`,
	} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log missing %q: %s", want, logs.String())
		}
	}
}

// TestAuditEvents_ListEndpointRespectsSince drives GET /v1/audit-events
// with a `since` cutoff and asserts rows strictly older are excluded.
// We seed two events ~10ms apart, query with `since` at the second
// event's at, and expect exactly one row.
func TestAuditEvents_ListEndpointRespectsSince(t *testing.T) {
	e := setup(t, api.PlanPro)
	// First key.created row.
	rec := e.do(t, http.MethodPost, "/v1/keys", api.CreateKeyRequest{Label: "first", Scopes: []string{api.ScopeAppsRead}}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/keys: code=%d body=%s", rec.Code, rec.Body.String())
	}
	// Sleep so the two events have measurably different timestamps.
	time.Sleep(20 * time.Millisecond)
	cutoff := time.Now().UTC()
	time.Sleep(20 * time.Millisecond)
	rec = e.do(t, http.MethodPost, "/v1/keys", api.CreateKeyRequest{Label: "second", Scopes: []string{api.ScopeAppsRead}}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/keys: code=%d body=%s", rec.Code, rec.Body.String())
	}

	sinceParam := cutoff.Format(time.RFC3339Nano)
	listRec := e.do(t, http.MethodGet, "/v1/audit-events?since="+sinceParam, nil, nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("GET /v1/audit-events?since=…: code=%d body=%s", listRec.Code, listRec.Body.String())
	}
	var list api.ListAuditEventsResponse
	if err := json.Unmarshal(listRec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v body=%s", err, listRec.Body.String())
	}
	// We expect exactly the "second" event to come back; the "first"
	// was emitted before cutoff so it must be excluded.
	if len(list.Events) != 1 {
		t.Fatalf("got %d events, want 1 (only events at >= cutoff); events=%+v", len(list.Events), list.Events)
	}
	if list.Events[0].Kind != "key.created" {
		t.Errorf("event[0].Kind = %q, want key.created", list.Events[0].Kind)
	}
}

// TestAuditEvents_ListEndpointCrossAccountInvisible proves that the
// customer-facing list is scoped to the caller: acct A mints a key
// (emits auth.* / key.* rows scoped to A's account_id); acct B's GET
// returns [].
func TestAuditEvents_ListEndpointCrossAccountInvisible(t *testing.T) {
	store := state.NewMemStore()
	// Acct A.
	acctA, err := store.CreateAccount(context.Background(), "a@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	// Acct B.
	acctB, err := store.CreateAccount(context.Background(), "b@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	// Drive a key.created row through acctA's path by appending
	// directly — the test goal is to prove ListEvents is subject-pinned,
	// not to re-test the createKey emission (already covered above).
	id := "0123456789abcdef0123456789abcdef"
	if err := store.AppendEvent(context.Background(), "apid", "key.created", &acctA.ID, []byte(`{"key_id":"`+id+`"}`)); err != nil {
		t.Fatal(err)
	}
	rowsA, _ := store.ListEvents(context.Background(), acctA.ID, 0)
	rowsB, _ := store.ListEvents(context.Background(), acctB.ID, 0)
	if len(rowsA) != 1 {
		t.Errorf("acctA events = %d, want 1", len(rowsA))
	}
	if len(rowsB) != 0 {
		t.Errorf("acctB events = %d, want 0 (cross-account isolation)", len(rowsB))
	}
}

// TestAuditEvents_GetEndpointReturnsRow drives GET /v1/audit-events/{id}
// for a freshly minted key's audit row and asserts the single-event
// wire shape (id, at, actor, kind, subject, data) comes back correctly.
func TestAuditEvents_GetEndpointReturnsRow(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, http.MethodPost, "/v1/keys", api.CreateKeyRequest{Label: "get-me", Scopes: []string{api.ScopeAppsRead}}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/keys: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rows, _ := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	var target *state.Event
	for i := range rows {
		if rows[i].Kind == "key.created" {
			target = &rows[i]
			break
		}
	}
	target = mustAuditEvent(t, target, "no key.created row to fetch")
	idStr := strconv.FormatInt(target.ID, 10)
	getRec := e.do(t, http.MethodGet, "/v1/audit-events/"+idStr, nil, nil)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /v1/audit-events/%s: code=%d body=%s", idStr, getRec.Code, getRec.Body.String())
	}
	var got api.AuditEventResponse
	if err := json.Unmarshal(getRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v body=%s", err, getRec.Body.String())
	}
	if got.Kind != "key.created" {
		t.Errorf("Kind = %q, want key.created", got.Kind)
	}
	if got.Subject != uuidStringOf(e.acct.ID) {
		t.Errorf("Subject = %q, want %s", got.Subject, uuidStringOf(e.acct.ID))
	}
	if got.Actor != "apid" {
		t.Errorf("Actor = %q, want apid", got.Actor)
	}
}

// TestAuditEvents_GetEndpointCrossAccount404 proves the get path
// 404s a cross-account id probe — a customer cannot enumerate
// another account's audit row count by id-probing.
func TestAuditEvents_GetEndpointCrossAccount404(t *testing.T) {
	store := state.NewMemStore()
	acctA, _ := store.CreateAccount(context.Background(), "a@example.com", api.PlanPro)
	acctB, _ := store.CreateAccount(context.Background(), "b@example.com", api.PlanPro)
	if err := store.AppendEvent(context.Background(), "apid", "key.created", &acctA.ID, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	rowsA, _ := store.ListEvents(context.Background(), acctA.ID, 0)
	if len(rowsA) != 1 {
		t.Fatalf("setup: acctA rows = %d, want 1", len(rowsA))
	}
	// Mount a server and ask as acctB.
	ptB, hashB, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(context.Background(), acctB.ID, hashB, "test", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	srv := newServer(store,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		"gregale.dev", noopNotifier{}).WithOpsMetrics(context.Background(), wire.NewOpsMetrics("apid"))
	h := srv.handler()
	idStr := strconv.FormatInt(rowsA[0].ID, 10)
	req := httptest.NewRequest(http.MethodGet, "/v1/audit-events/"+idStr, nil)
	req.Header.Set("Authorization", "Bearer "+ptB)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("cross-account GET code = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// TestAuditEvents_ListEndpointReturnsCronFired is the cmd/apid
// half of issue #291's cron-fire audit gap (companion to
// pkg/sched/cron_loop_test.go::TestCronDispatch_EmitsCronFiredAudit
// for the MemStore write path and
// pkg/state/pgstore_cron_test.go::TestPg_CronFiredAuditRoundTrip for
// the PgStore round-trip). It pins that the existing kind_prefix
// filter on GET /v1/audit-events includes the new "cron." family
// unchanged — meaning the dashboard's "cron history" query keeps
// working after this PR without handler churn.
//
// Strategy: mint a `cron.fired` row via store.AppendEvent directly
// (the schedd side is covered by the MemStore test; this one
// exercises only the apid read surface). Query with
// kind_prefix=cron. and assert the row comes back with the
// correct shape. We also query without a prefix to confirm the
// row lands alongside all other audit rows — that ordering is
// load-bearing for the unified history endpoint.
func TestAuditEvents_ListEndpointReturnsCronFired(t *testing.T) {
	e := setup(t, api.PlanPro)

	// Mint a cron.fired row directly via AppendEvent. The shape
	// matches pkg/sched/loop.go's emit (status=ok, invocation_id +
	// instance_id populated, last_fired_at_before present). Subject
	// is the test account — listAuditEvents is per-account scoped.
	payload, err := json.Marshal(map[string]any{
		"cron_id":              "cron-list-test",
		"app_id":               "app-list-test",
		"schedule":             "*/5 * * * *",
		"path":                 "/tick",
		"fired_at":             "2026-07-27T14:32:01Z",
		"last_fired_at_before": "2026-07-27T14:27:01Z",
		"status":               "ok",
		"invocation_id":        "inv-list-test",
		"instance_id":          "ins-list-test",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := e.store.AppendEvent(context.Background(), "schedd", "cron.fired", &e.acct.ID, payload); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	// 1) kind_prefix=cron. returns the new row alongside the
	//    (zero, post-mint) cron-emitted rows the handler keeps
	//    around. The cron.fired row must be in the response.
	rec := e.do(t, http.MethodGet, "/v1/audit-events?kind_prefix=cron.", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/audit-events?kind_prefix=cron.: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var list api.ListAuditEventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v body=%s", err, rec.Body.String())
	}
	if list.Limit != 50 {
		t.Errorf("Limit = %d, want 50", list.Limit)
	}
	var fired *api.AuditEventResponse
	for i := range list.Events {
		if list.Events[i].Kind == "cron.fired" {
			fired = &list.Events[i]
			break
		}
	}
	fired = mustAuditEventResponse(t, fired, fmt.Sprintf("no cron.fired row in list response (kind_prefix=cron.); events=%+v", list.Events))
	if fired.Actor != "schedd" {
		t.Errorf("fired.Actor = %q, want schedd", fired.Actor)
	}
	if fired.Subject != uuidStringOf(e.acct.ID) {
		t.Errorf("fired.Subject = %q, want %s", fired.Subject, uuidStringOf(e.acct.ID))
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(fired.Data), &data); err != nil {
		t.Fatalf("Data not valid JSON: %v (data=%q)", err, fired.Data)
	}
	if data["status"] != "ok" {
		t.Errorf("Data.status = %v, want ok", data["status"])
	}
	if data["invocation_id"] != "inv-list-test" {
		t.Errorf("Data.invocation_id = %v, want inv-list-test", data["invocation_id"])
	}
	if data["instance_id"] != "ins-list-test" {
		t.Errorf("Data.instance_id = %v, want ins-list-test", data["instance_id"])
	}

	// 2) Without a prefix the row is in the unified history. Pin
	//    that the list endpoint's no-filter path still surfaces it
	//    (no implicit filter on the kind column was added) — this
	//    is what the dashboard's "all events for account" tab does.
	rec = e.do(t, http.MethodGet, "/v1/audit-events", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/audit-events: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var unified api.ListAuditEventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &unified); err != nil {
		t.Fatalf("decode unified list: %v", err)
	}
	var seen bool
	for _, ev := range unified.Events {
		if ev.Kind == "cron.fired" {
			seen = true
			break
		}
	}
	if !seen {
		t.Errorf("cron.fired not in unfiltered list response; events=%+v", unified.Events)
	}
}

// TestAuditEvents_ListEndpointRespectsStatelessAdvisoryKindPrefix
// (Wave 0 PR-C / ADR-047) pins the customer surface for the new
// "stateless.advisory" audit kind. Mints one row directly via
// AppendEvent (the gRPC receiver path is covered by
// advisory_receiver_test.go; this one exercises only the apid read
// surface), then asserts:
//
//   - ?kind_prefix=stateless. returns the row with the right
//     actor=apid (matches advisory_receiver.go's audit.Emit call
//     site) and the events.data JSON shape (instance, app_id,
//     events[0].path, events[0].mask).
//   - Without ?kind_prefix, the row is in the unified history (no
//     implicit kind filter was added to the list endpoint).
//   - ?include_anonymous=true surfaces a stateless.advisory row
//     with subject=NULL (the rare defensive case where the app row
//     was deleted between wake and advisory).
//
// Mirrors TestAuditEvents_ListEndpointReturnsCronFired's shape —
// same scaffolding (store.AppendEvent seed + GET /v1/audit-events
// reads), different kind, same dashboard contract.
func TestAuditEvents_ListEndpointRespectsStatelessAdvisoryKindPrefix(t *testing.T) {
	e := setup(t, api.PlanPro)
	ownedAnonApp, err := e.store.CreateApp(context.Background(), state.App{
		AccountID: e.acct.ID, Slug: "audit-anon-owned", Type: state.AppTypeFunction,
		Runtime: "node22", RAMMB: 256, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create owned app: %v", err)
	}
	otherAccount, err := e.store.CreateAccount(context.Background(), "audit-other@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("create other account: %v", err)
	}
	otherApp, err := e.store.CreateApp(context.Background(), state.App{
		AccountID: otherAccount.ID, Slug: "audit-anon-other", Type: state.AppTypeFunction,
		Runtime: "node22", RAMMB: 256, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create other app: %v", err)
	}

	// Seed an anonymous-row variant (subject=NULL) for the
	// include_anonymous path. Order matters: account-scoped first,
	// then anonymous, so a sort.Slice regression surfaces here.
	accountedPayload, err := json.Marshal(map[string]any{
		"instance": "i-accounted",
		"app_id":   "a-accounted",
		"count":    1,
		"events": []map[string]any{
			{"path": "/data/foo", "mask": "create,modify", "pid": 42, "ts_unix_ms": 1700000000000},
		},
	})
	if err != nil {
		t.Fatalf("marshal accounted payload: %v", err)
	}
	if err := e.store.AppendEvent(context.Background(), "apid", "stateless.advisory", &e.acct.ID, accountedPayload); err != nil {
		t.Fatalf("AppendEvent accounted: %v", err)
	}
	anonPayload, err := json.Marshal(map[string]any{
		"instance": "i-anon",
		"app_id":   ownedAnonApp.ID,
		"count":    1,
		"events": []map[string]any{
			{"path": "/data/orphan", "mask": "create", "pid": 99, "ts_unix_ms": 1700000001000},
		},
	})
	if err != nil {
		t.Fatalf("marshal anon payload: %v", err)
	}
	if err := e.store.AppendEvent(context.Background(), "apid", "stateless.advisory", nil, anonPayload); err != nil {
		t.Fatalf("AppendEvent anon: %v", err)
	}
	otherPayload, _ := json.Marshal(map[string]any{"app_id": otherApp.ID, "instance": "cross-tenant"})
	if err := e.store.AppendEvent(context.Background(), "apid", "stateless.advisory", nil, otherPayload); err != nil {
		t.Fatalf("AppendEvent cross-tenant anon: %v", err)
	}

	// 1) kind_prefix=stateless. returns ONLY stateless.advisory rows.
	//    The accounted row is in scope (subject == acct.ID); the
	//    anonymous row is not (subject=NULL, include_anonymous=false).
	rec := e.do(t, http.MethodGet, "/v1/audit-events?kind_prefix=stateless.", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/audit-events?kind_prefix=stateless.: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var list api.ListAuditEventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v body=%s", err, rec.Body.String())
	}
	if list.Limit != 50 {
		t.Errorf("Limit = %d, want 50", list.Limit)
	}
	// Account-scoped row present, anonymous row absent (default
	// include_anonymous=false).
	var accounted *api.AuditEventResponse
	for i := range list.Events {
		if list.Events[i].Kind == "stateless.advisory" {
			if list.Events[i].Subject == "" {
				continue // anonymous row — must not appear here
			}
			accounted = &list.Events[i]
			break
		}
	}
	accounted = mustAuditEventResponse(t, accounted, fmt.Sprintf("no accounted stateless.advisory row in list response; events=%+v", list.Events))
	if accounted.Actor != "apid" {
		t.Errorf("accounted.Actor = %q, want apid", accounted.Actor)
	}
	if accounted.Subject != uuidStringOf(e.acct.ID) {
		t.Errorf("accounted.Subject = %q, want %s", accounted.Subject, uuidStringOf(e.acct.ID))
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(accounted.Data), &data); err != nil {
		t.Fatalf("Data not valid JSON: %v (data=%q)", err, accounted.Data)
	}
	if data["instance"] != "i-accounted" {
		t.Errorf("Data.instance = %v, want i-accounted", data["instance"])
	}
	if data["app_id"] != "a-accounted" {
		t.Errorf("Data.app_id = %v, want a-accounted", data["app_id"])
	}
	eventsList, ok := data["events"].([]any)
	if !ok || len(eventsList) == 0 {
		t.Fatalf("Data.events shape = %T (value=%v), want non-empty []any", data["events"], data["events"])
	}
	first, _ := eventsList[0].(map[string]any)
	if first["path"] != "/data/foo" {
		t.Errorf("Data.events[0].path = %v, want /data/foo", first["path"])
	}
	if first["mask"] != "create,modify" {
		t.Errorf("Data.events[0].mask = %v, want create,modify", first["mask"])
	}

	// 2) include_anonymous=true surfaces BOTH rows, newest first.
	//    The two rows have the same At (memstore.AppendEvent uses
	//    time.Now() and the seeded events land within microseconds);
	//    sort.Slice is stable on equal keys, so we just assert both
	//    are present rather than pin a specific order.
	rec = e.do(t, http.MethodGet, "/v1/audit-events?kind_prefix=stateless.&include_anonymous=true", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/audit-events?include_anonymous=true: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var merged api.ListAuditEventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &merged); err != nil {
		t.Fatalf("decode merged: %v", err)
	}
	var accountedSeen, anonSeen bool
	for _, ev := range merged.Events {
		if ev.Kind != "stateless.advisory" {
			continue
		}
		if ev.Subject == "" {
			if eventDataHasAppID([]byte(ev.Data), otherApp.ID) {
				t.Fatalf("cross-tenant anonymous event leaked: %+v", ev)
			}
			anonSeen = true
		} else {
			accountedSeen = true
		}
	}
	if !accountedSeen {
		t.Errorf("include_anonymous=true missed accounted row; events=%+v", merged.Events)
	}
	if !anonSeen {
		t.Errorf("include_anonymous=true missed anonymous row; events=%+v", merged.Events)
	}

	// 3) Without a prefix the stateless.advisory row is in the
	//    unified history — no implicit kind filter was added to
	//    the list endpoint.
	rec = e.do(t, http.MethodGet, "/v1/audit-events", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/audit-events: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var unified api.ListAuditEventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &unified); err != nil {
		t.Fatalf("decode unified: %v", err)
	}
	var seen bool
	for _, ev := range unified.Events {
		if ev.Kind == "stateless.advisory" {
			seen = true
			break
		}
	}
	if !seen {
		t.Errorf("stateless.advisory not in unfiltered list response; events=%+v", unified.Events)
	}

	// 4) ?app_id=<uuid> filters the overscan window to events whose
	//    data.app_id matches. The dashboard's app_detail.html
	//    "Stateless advisories" link uses this combo with
	//    kind_prefix=stateless.advisory to drill into a single app.
	//    Must return ONLY the accounted row (a-accounted) and not
	//    the anonymous row (a-deleted) — because a-deleted has
	//    data.app_id = "a-deleted", not "a-accounted".
	rec = e.do(t, http.MethodGet, "/v1/audit-events?kind_prefix=stateless.&app_id=a-accounted", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/audit-events?app_id=a-accounted: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var perApp api.ListAuditEventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &perApp); err != nil {
		t.Fatalf("decode per-app: %v", err)
	}
	if len(perApp.Events) != 1 {
		t.Fatalf("app_id=a-accounted: got %d rows, want 1; events=%+v", len(perApp.Events), perApp.Events)
	}
	if perApp.Events[0].Subject != uuidStringOf(e.acct.ID) {
		t.Errorf("app_id filter Subject = %q, want %s", perApp.Events[0].Subject, uuidStringOf(e.acct.ID))
	}
	var perAppData map[string]any
	if err := json.Unmarshal([]byte(perApp.Events[0].Data), &perAppData); err != nil {
		t.Fatalf("perApp[0].Data not JSON: %v", err)
	}
	if perAppData["app_id"] != "a-accounted" {
		t.Errorf("perApp[0].Data.app_id = %v, want a-accounted", perAppData["app_id"])
	}

	// 5) ?app_id=<unknown-uuid> returns zero rows (post-SQL filter
	//    excludes every event).
	rec = e.do(t, http.MethodGet, "/v1/audit-events?kind_prefix=stateless.&app_id=a-nobody", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/audit-events?app_id=a-nobody: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var empty api.ListAuditEventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &empty); err != nil {
		t.Fatalf("decode empty: %v", err)
	}
	if len(empty.Events) != 0 {
		t.Errorf("app_id=a-nobody: got %d rows, want 0; events=%+v", len(empty.Events), empty.Events)
	}
}

// TestAuditEvents_CliAuthExchangeEmitsAuthLoginAndKeyCreated drives
// the full device-code flow (POST /v1/cli-auth/code → claim → POST
// /v1/cli-auth/exchange) and asserts BOTH audit rows landed:
//   - key.created with data.key_id == minted key's id and data.scopes
//     carrying the default scope set
//   - auth.login with data.method == "cli_code"
//
// A single test exercises both Emit calls in the happy-path exchange
// because they share the only success branch where the CLI's principal
// lands on the account (handlers_cli_auth.go:155-164). Failure paths
// are covered separately by the existing TestExchangeCliAuthCode_*
// family — we only need to prove the audit emit fires when the
// handler returns 200.
func TestAuditEvents_CliAuthExchangeEmitsAuthLoginAndKeyCreated(t *testing.T) {
	srv, store := newCliAuthTestServer(t)

	// Seed an account so the claim binds to it.
	acct, err := store.CreateAccount(t.Context(), "cli-audit@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}

	// Step 1: CLI mints a code.
	minted := mintCliAuthCodeForTest(t, srv)

	// Step 2 + 3: simulate the dashboard claim (we use the store
	// directly to keep the test focused on the audit emission in
	// step 4 — the claim path is exercised by TestPostCliAuthPage_*).
	if err := store.ClaimCliAuthCode(t.Context(), mustHashCode(t, minted.Code), acct.ID); err != nil {
		t.Fatalf("claim: %v", err)
	}

	// Step 4: CLI polls. This is the success branch where both Emit
	// calls fire.
	body, _ := json.Marshal(api.CliAuthExchangeRequest{Code: minted.Code})
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/cli-auth/exchange", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("exchange: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp api.CliAuthExchangeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode exchange response: %v", err)
	}
	if !api.ValidAPIKeyFormat(resp.Plaintext) {
		t.Fatalf("plaintext %q is not a valid api key", resp.Plaintext)
	}

	// Now assert both rows landed and are subject-pinned to acct.ID.
	rows, err := store.ListEvents(t.Context(), acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}

	var keyCreated, authLogin *state.Event
	for i := range rows {
		switch rows[i].Kind {
		case "key.created":
			keyCreated = &rows[i]
		case "auth.login":
			authLogin = &rows[i]
		}
	}
	keyCreated = mustAuditEvent(t, keyCreated, fmt.Sprintf("no key.created row; rows=%+v", rows))
	authLogin = mustAuditEvent(t, authLogin, fmt.Sprintf("no auth.login row; rows=%+v", rows))

	// Subject pinning: both rows scoped to acct.ID.
	wantSubject := uuidStringOf(acct.ID)
	if keyCreated.Subject == nil || keyCreated.Subject.String() != wantSubject {
		t.Errorf("key.created Subject = %v, want %s", keyCreated.Subject, wantSubject)
	}
	if authLogin.Subject == nil || authLogin.Subject.String() != wantSubject {
		t.Errorf("auth.login Subject = %v, want %s", authLogin.Subject, wantSubject)
	}

	// key.created carries the new key_id AND the default scopes.
	var keyData map[string]any
	if err := json.Unmarshal(keyCreated.Data, &keyData); err != nil {
		t.Fatalf("key.created Data not JSON: %v", err)
	}
	keyID, _ := keyData["key_id"].(string)
	if len(keyID) != 32 {
		t.Errorf("key.created Data.key_id = %q, want 32 hex chars", keyID)
	}
	scopes, _ := keyData["scopes"].([]any)
	if len(scopes) == 0 {
		t.Errorf("key.created Data.scopes missing: %+v", keyData)
	}

	// auth.login carries method=cli_code (the only method this path
	// can produce — there's no session-cookie or password variant).
	var loginData map[string]any
	if err := json.Unmarshal(authLogin.Data, &loginData); err != nil {
		t.Fatalf("auth.login Data not JSON: %v", err)
	}
	if loginData["method"] != "cli_code" {
		t.Errorf("auth.login Data.method = %v, want cli_code", loginData["method"])
	}

	// Ordering invariant (load-bearing — handbook §6 / ADR-035):
	// key.created must precede auth.login so the customer's timeline
	// reads "first I minted a key, then I logged in" rather than the
	// reverse. The handler emits in that order, the events table
	// appends in arrival order, so we check by ID monotonicity.
	if authLogin.ID <= keyCreated.ID {
		t.Errorf("auth.login.ID=%d must be greater than key.created.ID=%d (event ordering)",
			authLogin.ID, keyCreated.ID)
	}
}

// TestAuditEvents_DashboardDeleteEmitsEventWithViaDashboard covers
// the dashboard form path (POST /dashboard/account/delete). The
// same business-logic core (s.scheduleDeletion) is used by both this
// route and the REST DELETE /v1/account; the only thing that varies
// is the `via` parameter, which the handler passes through to
// audit.Emit. So the regression we're guarding against is "the
// dashboard path forgot to thread `via` from the route handler down
// to scheduleDeletion" — a bug class that would silently mark
// every dashboard deletion as data.via == "rest".
//
// We build a session-authed handler inline rather than reuse
// newAuthedDashboardServer because that helper doesn't return the
// MemStore (we need it to read events back out without a GET round
// trip). All other seams (mgr.Issue, middleware.CookieNameAuthenticated,
// sessionCookie, sessionCookieLifetime) are imported from the
// existing handlers_dashboard / handlers_auth / middleware / session
// packages.
func TestAuditEvents_DashboardDeleteEmitsEventWithViaDashboard(t *testing.T) {
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "del@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	mgr, err := session.NewEphemeralManager(sessionCookieLifetime)
	if err != nil {
		t.Fatalf("session manager: %v", err)
	}
	// Step-up gated: dashboardDelete sits behind
	// sessionAuth → requireStepUpHandler(5m). The cookie
	// must carry a fresh StepUpAt stamp so the gate passes
	// through to scheduleDeletion (which emits the audit
	// row this test asserts on). IssueWithSessionAndBindingHashAndStepUp
	// requires a sessions row first; the empty binding hash
	// is fine because there's no (IP, UA) cross-check at
	// this endpoint (only the dashboard delete path, which
	// is unix-socket-shaped at the test layer).
	stepSid := "del-stepup-sid"
	if _, err := store.CreateSession(t.Context(), stepSid, acct.ID, "", ""); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sid, err := mgr.IssueWithSessionAndBindingHashAndStepUp(stepSid, acct.ID, "", time.Now(), false)
	if err != nil {
		t.Fatalf("issue stepped-up session: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := newServerWithDeps(store, log, "gregale.dev", noopNotifier{}, "",
		noopMailer{}, stubGithubdClient{}, mgr, nil, 15*time.Minute, "").handler()

	// Drive GET /dashboard/account to mint a fresh sealed envelope
	// (the faas_csrf cookie + csrf_token form-value pair are bound
	// to (action="delete", subject=acct.ID) at render time).
	csrfCookie, deleteToken, _ := renderDashboardAccount(t, h, &http.Cookie{Name: sessionCookie, Value: sid})
	if deleteToken == "" {
		t.Fatal("rendered /dashboard/account is missing the delete csrf_token")
	}

	// POST /dashboard/account/delete.
	form := url.Values{}
	form.Set(middleware.FormFieldName, deleteToken)
	req := httptest.NewRequest(http.MethodPost, "/dashboard/account/delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sid})
	req.AddCookie(&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: csrfCookie})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("POST /dashboard/account/delete: code=%d body=%s", rec.Code, rec.Body.String())
	}

	// Assert the audit row landed with data.via == "dashboard".
	// The REST path test (TestAuditEvents_AccountDeletionScheduledEmitsEvent)
	// already guards data.via == "rest"; together the two tests
	// prove the via parameter is threaded correctly from both call
	// sites through s.scheduleDeletion into the audit payload.
	rows, err := store.ListEvents(t.Context(), acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var found *state.Event
	for i := range rows {
		if rows[i].Kind == "account.deletion_scheduled" {
			found = &rows[i]
			break
		}
	}
	found = mustAuditEvent(t, found, fmt.Sprintf("no account.deletion_scheduled row from dashboard path; rows=%+v", rows))
	var data map[string]any
	if err := json.Unmarshal(found.Data, &data); err != nil {
		t.Fatalf("Data not JSON: %v", err)
	}
	if data["via"] != "dashboard" {
		t.Errorf("Data.via = %v, want dashboard (REST path TestAuditEvents_AccountDeletionScheduledEmitsEvent covers \"rest\")",
			data["via"])
	}
	if found.Subject == nil || found.Subject.String() != uuidStringOf(acct.ID) {
		t.Errorf("Subject = %v, want %s", found.Subject, uuidStringOf(acct.ID))
	}
}

// TestAuditEvents_FailingStoreDoesNotRollback proves the audit seam
// is best-effort: a failing AppendEvent must NOT roll back the
// mutation that produced the audit emit. Mirrors schedd's
// TestEngineTransition_EventWriteFailureDoesNotRollback exactly —
// build a thin wrapper that errors on AppendEvent, drive POST
// /v1/keys, assert the handler returned 201 (not 5xx) and the
// audit_write_failures counter incremented.
func TestAuditEvents_FailingStoreDoesNotRollback(t *testing.T) {
	e := setup(t, api.PlanPro)
	wrapped := &failingAuditStore{Store: e.store}
	// Re-mount with the wrapped store. The audit helper reads s.audit
	// ops lazily so we keep e.ops the same.
	srv := newServer(wrapped,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		"gregale.dev", noopNotifier{}).WithOpsMetrics(context.Background(), e.ops)
	h := srv.handler()
	body, _ := json.Marshal(api.CreateKeyRequest{Label: "audit-fail", Scopes: []string{api.ScopeAppsRead}})
	req := httptest.NewRequest(http.MethodPost, "/v1/keys", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/keys with failing audit store: code=%d body=%s", rec.Code, rec.Body.String())
	}
	// Confirm the underlying row landed (the handler's success path
	// is NOT gated on audit emit — that's the load-bearing invariant).
	keys, err := e.store.ListAPIKeys(context.Background(), e.acct.ID)
	if err != nil {
		t.Fatalf("ListAPIKeys: %v", err)
	}
	if len(keys) < 2 {
		t.Errorf("API keys = %d, want ≥2 (audit failure must not roll back create)", len(keys))
	}
	// Counter incremented.
	if got := testutil.ToFloat64(e.ops.AuditWriteFailures(e.acct.ID)); got < 1 {
		t.Errorf("audit_write_failures = %v, want ≥1", got)
	}
}

// --- helpers ---------------------------------------------------------------

// failingAuditStore wraps state.Store and errors on AppendEvent only.
// Same pattern as pkg/sched/events_test.go::failingEventStore.
type failingAuditStore struct {
	state.Store
	failures int64
}

func (f *failingAuditStore) AppendEvent(ctx context.Context, actor, kind string, subject *string, data []byte) error {
	f.failures++
	return &failingAuditErr{}
}

// AppendEventWithTrace (PR-#TBD / C2) — C4's audit emit path
// routes through this method; increment the same failures
// counter so the test's metric assertions stay aligned.
func (f *failingAuditStore) AppendEventWithTrace(ctx context.Context, actor, kind string, subject *string, data []byte, traceID *string) error {
	f.failures++
	return &failingAuditErr{}
}

type failingAuditErr struct{}

func (*failingAuditErr) Error() string { return "simulated AppendEvent failure" }

// --- issue #291 customer-facing audit emission ---------------------------
//
// Each test below drives one mutation route end-to-end and asserts the
// events row landed via the auditor.Emit seam with the documented data
// payload. The pattern mirrors TestAuditEvents_KeyMintEmitsEvent above:
// drive the wire path, read events back via e.store.ListEvents, find the
// row by Kind, JSON-decode Data, assert the field set.
//
// All 13 subtests run against the in-process MemStore; the auditor seam
// is store-agnostic (cmd/apid/audit.go:79), so PG parity is inherited
// from the existing TestAuditEvents_* suite.

var digestFor = "sha256:" + repeat("a", 64)

// seedAppForAudit POST /v1/apps and returns the api.AppResponse so the
// test caller has the ID + slug for downstream routes. Pro plan gives
// the default 5 deployed-apps, 5 concurrency, and 256 MB — a no-quota
// choice so the audit test stays focused on emissions, not gating.
func seedAppForAudit(t *testing.T, e testEnv, slug string) api.AppResponse {
	t.Helper()
	rec := e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: slug}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed app %q: code=%d body=%s", slug, rec.Code, rec.Body.String())
	}
	var out api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode seed app: %v", err)
	}
	return out
}

func seedDeploymentForAudit(t *testing.T, e testEnv, slug string) string {
	t.Helper()
	rec := e.do(t, http.MethodPost, "/v1/apps/"+slug+"/deployments",
		api.CreateDeploymentRequest{Image: "registry.example.com/x@" + digestFor}, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("seed deploy for %q: code=%d body=%s", slug, rec.Code, rec.Body.String())
	}
	var out api.DeploymentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode seed deploy: %v", err)
	}
	if err := e.store.SetDeploymentRootfs(context.Background(), out.ID, "/srv/fc/apps/"+slug+"/"+out.ID+".ext4", "apps/"+slug+"/"+out.ID+".ext4", 1); err != nil {
		t.Fatalf("seed deployment rootfs: %v", err)
	}
	return out.ID
}

// findEventByKind scans a ListEvents result for the first row matching
// kind. Returns nil when no row exists. Mirrors the inline loop used in
// TestAuditEvents_KeyMintEmitsEvent so the helper stays tight.
func findEventByKind(rows []state.Event, kind string) *state.Event {
	for i := range rows {
		if rows[i].Kind == kind {
			return &rows[i]
		}
	}
	return nil
}

// TestAuditEvents_AppCreatedEmitsEvent (issue #291) drives POST
// /v1/apps and asserts the auditor records app.created with the
// expected payload (app_id, slug, type, runtime, ram_mb,
// max_concurrency).
func TestAuditEvents_AppCreatedEmitsEvent(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: "audit-app-create", Type: "app"}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/apps: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var created api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	found := findEventByKind(rows, "app.created")
	found = mustAuditEvent(t, found, fmt.Sprintf("no app.created event row; rows=%+v", rows))
	if found.Subject == nil || found.Subject.String() != uuidStringOf(e.acct.ID) {
		t.Errorf("Subject = %v, want %s", found.Subject, uuidStringOf(e.acct.ID))
	}
	var data map[string]any
	if err := json.Unmarshal(found.Data, &data); err != nil {
		t.Fatalf("Data not valid JSON: %v", err)
	}
	if data["app_id"] != created.ID {
		t.Errorf("Data.app_id = %v, want %s", data["app_id"], created.ID)
	}
	if data["slug"] != "audit-app-create" {
		t.Errorf("Data.slug = %v, want audit-app-create", data["slug"])
	}
	if data["type"] != "app" {
		t.Errorf("Data.type = %v, want app", data["type"])
	}
	if v, _ := data["ram_mb"].(float64); v != float64(created.RAMMB) {
		t.Errorf("Data.ram_mb = %v, want %d", data["ram_mb"], created.RAMMB)
	}
	if v, _ := data["max_concurrency"].(float64); v != float64(created.MaxConcurrency) {
		t.Errorf("Data.max_concurrency = %v, want %d", data["max_concurrency"], created.MaxConcurrency)
	}
}

// TestAuditEvents_AppDeployedEmitsEvent (issue #291) drives a first
// POST /v1/apps/{slug}/deployments and asserts app.deployed with
// supersedes == "" (no prior deployment on this app).
func TestAuditEvents_AppDeployedEmitsEvent(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedAppForAudit(t, e, "audit-app-deploy")
	depID := seedDeploymentForAudit(t, e, app.Slug)

	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	// Two rows are expected: app.created (from seedAppForAudit) and
	// app.deployed. Pick the second kind specifically.
	found := findEventByKind(rows, "app.deployed")
	found = mustAuditEvent(t, found, fmt.Sprintf("no app.deployed event row; rows=%+v", rows))
	var data map[string]any
	if err := json.Unmarshal(found.Data, &data); err != nil {
		t.Fatalf("Data not valid JSON: %v", err)
	}
	if data["app_id"] != app.ID {
		t.Errorf("Data.app_id = %v, want %s", data["app_id"], app.ID)
	}
	if data["deployment_id"] != depID {
		t.Errorf("Data.deployment_id = %v, want %s", data["deployment_id"], depID)
	}
	if data["ref"] != "registry.example.com/x@"+digestFor {
		t.Errorf("Data.ref = %v", data["ref"])
	}
	if data["supersedes"] != "" {
		t.Errorf("Data.supersedes = %v, want \"\" (first deploy on this app)", data["supersedes"])
	}
}

// TestAuditEvents_AppDeployedEmitsSupersedesOnSecondDeploy covers the
// pre-PR #340 PR-B path: the second deployment carries
// data.supersedes == first_deployment.ID.
func TestAuditEvents_AppDeployedEmitsSupersedesOnSecondDeploy(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedAppForAudit(t, e, "audit-app-supersede")
	firstDep := seedDeploymentForAudit(t, e, app.Slug)
	secondDep := seedDeploymentForAudit(t, e, app.Slug)

	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	// Find the SECOND app.deployed row (newest first per listAuditEvents
	// ordering). Walk events newest-first to ensure we picked the second.
	var found *state.Event
	for i := range rows {
		if rows[i].Kind == "app.deployed" {
			var data map[string]any
			_ = json.Unmarshal(rows[i].Data, &data)
			if data["deployment_id"] == secondDep {
				found = &rows[i]
				break
			}
		}
	}
	found = mustAuditEvent(t, found, fmt.Sprintf("no app.deployed row for second deployment %s; rows=%+v", secondDep, rows))
	var data map[string]any
	_ = json.Unmarshal(found.Data, &data)
	if data["supersedes"] != firstDep {
		t.Errorf("Data.supersedes = %v, want %s (first deployment)", data["supersedes"], firstDep)
	}
}

// TestAuditEvents_AppUpdatedEmitsEvent (issue #291) drives PATCH
// /v1/apps/{slug} with a RAMMB change and asserts app.updated carries
// old + new payloads per user choice.
func TestAuditEvents_AppUpdatedEmitsEvent(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedAppForAudit(t, e, "audit-app-update")
	// Pro plan caps RAMMB at 512. The default for Pro is already
	// 512 so we drop to Hobby's 256 to exercise an in-plan change
	// without hitting the validate ceiling. Pins the seam that
	// the audit row fires for any successful PATCH, not just a
	// value increase.
	originalRAM := app.RAMMB
	newRAM := 256

	ram := newRAM
	rec := e.do(t, http.MethodPatch, "/v1/apps/"+app.Slug,
		api.UpdateAppRequest{RAMMB: &ram}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /v1/apps/%s: code=%d body=%s", app.Slug, rec.Code, rec.Body.String())
	}

	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	found := findEventByKind(rows, "app.updated")
	found = mustAuditEvent(t, found, fmt.Sprintf("no app.updated event row; rows=%+v", rows))
	var data map[string]any
	if err := json.Unmarshal(found.Data, &data); err != nil {
		t.Fatalf("Data not valid JSON: %v", err)
	}
	if data["app_id"] != app.ID {
		t.Errorf("Data.app_id = %v, want %s", data["app_id"], app.ID)
	}
	if data["slug"] != app.Slug {
		t.Errorf("Data.slug = %v, want %s", data["slug"], app.Slug)
	}
	oldMap, ok := data["old"].(map[string]any)
	if !ok {
		t.Fatalf("Data.old missing or wrong type: %+v", data)
	}
	newMap, ok := data["new"].(map[string]any)
	if !ok {
		t.Fatalf("Data.new missing or wrong type: %+v", data)
	}
	if v, _ := oldMap["ram_mb"].(float64); v != float64(originalRAM) {
		t.Errorf("Data.old.ram_mb = %v, want %d", oldMap["ram_mb"], originalRAM)
	}
	if v, _ := newMap["ram_mb"].(float64); v != float64(newRAM) {
		t.Errorf("Data.new.ram_mb = %v, want %d", newMap["ram_mb"], newRAM)
	}
	// Only ram_mb should be present — a schedule-only PATCH would
	// not include unrelated fields. Pins the per-field capture
	// invariant.
	if _, present := oldMap["max_concurrency"]; present {
		t.Errorf("Data.old.max_concurrency present on RAMMB-only patch: %+v", oldMap)
	}
}

// TestAuditEvents_AppDeletedEmitsEvent (issue #291) drives DELETE
// /v1/apps/{slug} and asserts app.deleted with app_id + slug.
func TestAuditEvents_AppDeletedEmitsEvent(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedAppForAudit(t, e, "audit-app-delete")

	rec := e.do(t, http.MethodDelete, "/v1/apps/"+app.Slug, nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE /v1/apps/%s: code=%d body=%s", app.Slug, rec.Code, rec.Body.String())
	}

	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	found := findEventByKind(rows, "app.deleted")
	found = mustAuditEvent(t, found, fmt.Sprintf("no app.deleted event row; rows=%+v", rows))
	var data map[string]any
	if err := json.Unmarshal(found.Data, &data); err != nil {
		t.Fatalf("Data not valid JSON: %v", err)
	}
	if data["app_id"] != app.ID {
		t.Errorf("Data.app_id = %v, want %s", data["app_id"], app.ID)
	}
	if data["slug"] != app.Slug {
		t.Errorf("Data.slug = %v, want %s", data["slug"], app.Slug)
	}
}

// TestAuditEvents_AppRollbackRequestedEmitsEvent (issue #291) drives
// /v1/apps/{slug}/rollback with two seeded deployments (the second
// live) and asserts the readiness-gated rollback request records its
// current and target deployment IDs.
func TestAuditEvents_AppRollbackRequestedEmitsEvent(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedAppForAudit(t, e, "audit-app-rollback")
	// LatestSupersededDeployment only returns rows that already have
	// status="superseded". MemStore's MarkDeploymentSuperseded
	// implements that, so seed → mark superseded → mark live to
	// give the rollback path a target.
	firstDep := seedDeploymentForAudit(t, e, app.Slug)
	if err := e.store.MarkDeploymentSuperseded(context.Background(), firstDep); err != nil {
		t.Fatalf("MarkDeploymentSuperseded: %v", err)
	}
	secondDep := seedDeploymentForAudit(t, e, app.Slug)
	if err := e.store.MarkDeploymentLive(context.Background(), secondDep); err != nil {
		t.Fatalf("MarkDeploymentLive: %v", err)
	}

	rec := e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/rollback", nil, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST /v1/apps/%s/rollback: code=%d body=%s", app.Slug, rec.Code, rec.Body.String())
	}

	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	found := findEventByKind(rows, "app.rollback_requested")
	found = mustAuditEvent(t, found, fmt.Sprintf("no app.rollback_requested event row; rows=%+v", rows))
	var data map[string]any
	if err := json.Unmarshal(found.Data, &data); err != nil {
		t.Fatalf("Data not valid JSON: %v", err)
	}
	if data["app_id"] != app.ID {
		t.Errorf("Data.app_id = %v, want %s", data["app_id"], app.ID)
	}
	if data["from"] != secondDep {
		t.Errorf("Data.from = %v, want %s (current live deployment)", data["from"], secondDep)
	}
	if data["to"] != firstDep {
		t.Errorf("Data.to = %v, want %s (rollback target)", data["to"], firstDep)
	}
}

// TestAuditEvents_DomainAddedEmitsEvent (issue #291) drives POST
// /v1/domains and asserts domain.added with app_id + lowercased
// domain (the canonical form stored on the row).
func TestAuditEvents_DomainAddedEmitsEvent(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedAppForAudit(t, e, "audit-domain-add")

	rec := e.do(t, http.MethodPost, "/v1/domains",
		api.CreateCustomDomainRequest{AppID: app.ID, Domain: "Audit-Domain.COM"}, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST /v1/domains: code=%d body=%s", rec.Code, rec.Body.String())
	}

	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	found := findEventByKind(rows, "domain.added")
	found = mustAuditEvent(t, found, fmt.Sprintf("no domain.added event row; rows=%+v", rows))
	var data map[string]any
	if err := json.Unmarshal(found.Data, &data); err != nil {
		t.Fatalf("Data not valid JSON: %v", err)
	}
	if data["app_id"] != app.ID {
		t.Errorf("Data.app_id = %v, want %s", data["app_id"], app.ID)
	}
	if data["domain"] != "audit-domain.com" {
		t.Errorf("Data.domain = %v, want audit-domain.com (lowercased canonical)", data["domain"])
	}
}

// TestAuditEvents_DomainRemovedEmitsEvent (issue #291) drives DELETE
// /v1/domains/{domain} and asserts domain.removed.
func TestAuditEvents_DomainRemovedEmitsEvent(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedAppForAudit(t, e, "audit-domain-remove")
	rec := e.do(t, http.MethodPost, "/v1/domains",
		api.CreateCustomDomainRequest{AppID: app.ID, Domain: "audit-domain-remove.com"}, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST /v1/domains seed: code=%d body=%s", rec.Code, rec.Body.String())
	}

	delRec := e.do(t, http.MethodDelete, "/v1/domains/audit-domain-remove.com", nil, nil)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("DELETE /v1/domains/audit-domain-remove.com: code=%d body=%s", delRec.Code, delRec.Body.String())
	}

	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	found := findEventByKind(rows, "domain.removed")
	found = mustAuditEvent(t, found, fmt.Sprintf("no domain.removed event row; rows=%+v", rows))
	var data map[string]any
	if err := json.Unmarshal(found.Data, &data); err != nil {
		t.Fatalf("Data not valid JSON: %v", err)
	}
	if data["app_id"] != app.ID {
		t.Errorf("Data.app_id = %v, want %s", data["app_id"], app.ID)
	}
	if data["domain"] != "audit-domain-remove.com" {
		t.Errorf("Data.domain = %v, want audit-domain-remove.com", data["domain"])
	}
}

// TestAuditEvents_CronCreatedEmitsEvent (issue #291) drives POST
// /v1/crons and asserts cron.created with cron_id, app_id, schedule,
// path, enabled. Sits AFTER PR #340's plan gate (handlers_ext.go)
// — see TestAuditEvents_CronCreatedFreeReturns402DoesNotEmit for
// the negative invariant.
func TestAuditEvents_CronCreatedEmitsEvent(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedAppForAudit(t, e, "audit-cron-create")

	rec := e.do(t, http.MethodPost, "/v1/crons",
		api.CreateCronRequest{AppID: app.ID, Schedule: "*/5 * * * *", Path: "/tick"}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/crons: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var created api.CronResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	found := findEventByKind(rows, "cron.created")
	found = mustAuditEvent(t, found, fmt.Sprintf("no cron.created event row; rows=%+v", rows))
	var data map[string]any
	if err := json.Unmarshal(found.Data, &data); err != nil {
		t.Fatalf("Data not valid JSON: %v", err)
	}
	if data["cron_id"] != created.ID {
		t.Errorf("Data.cron_id = %v, want %s", data["cron_id"], created.ID)
	}
	if data["app_id"] != app.ID {
		t.Errorf("Data.app_id = %v, want %s", data["app_id"], app.ID)
	}
	if data["schedule"] != "*/5 * * * *" {
		t.Errorf("Data.schedule = %v", data["schedule"])
	}
	if data["path"] != "/tick" {
		t.Errorf("Data.path = %v", data["path"])
	}
	if v, _ := data["enabled"].(bool); !v {
		t.Errorf("Data.enabled = %v, want true", data["enabled"])
	}
}

// TestAuditEvents_CronUpdatedEmitsEvent (issue #291) drives PATCH
// /v1/crons/{id} with a schedule change and asserts cron.updated
// carries old + new payloads per user choice.
func TestAuditEvents_CronUpdatedEmitsEvent(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedAppForAudit(t, e, "audit-cron-update")

	createRec := e.do(t, http.MethodPost, "/v1/crons",
		api.CreateCronRequest{AppID: app.ID, Schedule: "*/5 * * * *", Path: "/tick"}, nil)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("seed cron: code=%d body=%s", createRec.Code, createRec.Body.String())
	}
	var created api.CronResponse
	_ = json.Unmarshal(createRec.Body.Bytes(), &created)

	newSched := "*/15 * * * *"
	patchRec := e.do(t, http.MethodPatch, "/v1/crons/"+created.ID,
		api.UpdateCronRequest{Schedule: &newSched}, nil)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("PATCH /v1/crons/%s: code=%d body=%s", created.ID, patchRec.Code, patchRec.Body.String())
	}

	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	found := findEventByKind(rows, "cron.updated")
	found = mustAuditEvent(t, found, fmt.Sprintf("no cron.updated event row; rows=%+v", rows))
	var data map[string]any
	if err := json.Unmarshal(found.Data, &data); err != nil {
		t.Fatalf("Data not valid JSON: %v", err)
	}
	if data["cron_id"] != created.ID {
		t.Errorf("Data.cron_id = %v, want %s", data["cron_id"], created.ID)
	}
	if data["app_id"] != app.ID {
		t.Errorf("Data.app_id = %v, want %s", data["app_id"], app.ID)
	}
	oldMap, ok := data["old"].(map[string]any)
	if !ok {
		t.Fatalf("Data.old missing or wrong type: %+v", data)
	}
	newMap, ok := data["new"].(map[string]any)
	if !ok {
		t.Fatalf("Data.new missing or wrong type: %+v", data)
	}
	if oldMap["schedule"] != "*/5 * * * *" {
		t.Errorf("Data.old.schedule = %v", oldMap["schedule"])
	}
	if newMap["schedule"] != "*/15 * * * *" {
		t.Errorf("Data.new.schedule = %v", newMap["schedule"])
	}
	// Schedule-only patch — path / enabled must NOT appear on
	// either side. Pins the per-field capture invariant.
	if _, present := oldMap["path"]; present {
		t.Errorf("Data.old.path present on schedule-only patch: %+v", oldMap)
	}
}

// TestAuditEvents_CronDeletedEmitsEvent (issue #291) drives DELETE
// /v1/crons/{id} and asserts cron.deleted with cron_id + app_id.
func TestAuditEvents_CronDeletedEmitsEvent(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedAppForAudit(t, e, "audit-cron-delete")

	createRec := e.do(t, http.MethodPost, "/v1/crons",
		api.CreateCronRequest{AppID: app.ID, Schedule: "*/5 * * * *", Path: "/tick"}, nil)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("seed cron: code=%d body=%s", createRec.Code, createRec.Body.String())
	}
	var created api.CronResponse
	_ = json.Unmarshal(createRec.Body.Bytes(), &created)

	delRec := e.do(t, http.MethodDelete, "/v1/crons/"+created.ID, nil, nil)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("DELETE /v1/crons/%s: code=%d body=%s", created.ID, delRec.Code, delRec.Body.String())
	}

	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	found := findEventByKind(rows, "cron.deleted")
	found = mustAuditEvent(t, found, fmt.Sprintf("no cron.deleted event row; rows=%+v", rows))
	var data map[string]any
	if err := json.Unmarshal(found.Data, &data); err != nil {
		t.Fatalf("Data not valid JSON: %v", err)
	}
	if data["cron_id"] != created.ID {
		t.Errorf("Data.cron_id = %v, want %s", data["cron_id"], created.ID)
	}
	if data["app_id"] != app.ID {
		t.Errorf("Data.app_id = %v, want %s", data["app_id"], app.ID)
	}
}

// TestAuditEvents_ListEndpointRespectsKindPrefixFilterForAppFamily
// (issue #291) drives the GET /v1/audit-events?kind_prefix=app. read
// path and asserts both app.created + app.deployed come back (no
// auth.* / key.* / etc.). Confirms the existing kind_prefix filter
// works for the new family unchanged.
func TestAuditEvents_ListEndpointRespectsKindPrefixFilterForAppFamily(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedAppForAudit(t, e, "audit-list-prefix")
	_ = seedDeploymentForAudit(t, e, app.Slug)
	// Also seed a key.created row so we have a non-app event to
	// exclude — proves the filter is real, not a no-op.
	keyRec := e.do(t, http.MethodPost, "/v1/keys",
		api.CreateKeyRequest{Label: "audit-prefix-key", Scopes: []string{api.ScopeAppsRead}}, nil)
	if keyRec.Code != http.StatusCreated {
		t.Fatalf("seed key: code=%d body=%s", keyRec.Code, keyRec.Body.String())
	}

	rec := e.do(t, http.MethodGet, "/v1/audit-events?kind_prefix=app.", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/audit-events?kind_prefix=app.: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var list api.ListAuditEventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Exactly two rows expected: app.created + app.deployed.
	if len(list.Events) != 2 {
		t.Fatalf("got %d events, want 2 (app.created + app.deployed); events=%+v", len(list.Events), list.Events)
	}
	seen := map[string]bool{}
	for _, ev := range list.Events {
		if !strings.HasPrefix(ev.Kind, "app.") {
			t.Errorf("event %q leaked past kind_prefix=app. filter", ev.Kind)
		}
		seen[ev.Kind] = true
	}
	if !seen["app.created"] || !seen["app.deployed"] {
		t.Errorf("missing kind in filter result: got %+v, want app.created + app.deployed", seen)
	}
}

// TestAuditEvents_CronCreatedFreeReturns402DoesNotEmit (issue #291
// + PR #340) pins the seam-invariant that the 402 plan gate fires
// BEFORE the auditor.Emit call. A Free plan hitting POST /v1/crons
// must NOT produce a cron.created audit row, otherwise the audit log
// would track rejected attempts alongside accepted ones.
func TestAuditEvents_CronCreatedFreeReturns402DoesNotEmit(t *testing.T) {
	e := setup(t, api.PlanFree)
	app := seedAppForAudit(t, e, "audit-free-cron")

	rec := e.do(t, http.MethodPost, "/v1/crons",
		api.CreateCronRequest{AppID: app.ID, Schedule: "*/5 * * * *", Path: "/tick"}, nil)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("Free plan POST /v1/crons: code=%d body=%s, want 402",
			rec.Code, rec.Body.String())
	}

	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if found := findEventByKind(rows, "cron.created"); found != nil {
		t.Errorf("Free plan 402 must NOT emit cron.created, found: %+v", found)
	}
}

// doAsAccount fires a request against the same handler `e.h` but with
// foreignKey in the Authorization header. The store is shared with
// `e`, so a foreign account created on e.store authenticates
// through the same auth middleware — same as how a real foreign
// customer would arrive at the box. Used only by the cross-account
// 404 negative test below; happy-path tests keep using e.do.
func doAsAccount(t *testing.T, e testEnv, foreignKey, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, r)
	req.Header.Set("Authorization", "Bearer "+foreignKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

// TestAuditEvents_CronForeignOwnerReturns404DoesNotEmit (issue #291
// review) pins the cross-account seam on the cron mutation routes.
// updateCron (handlers_ext.go:791-799) and deleteCron (:837-845)
// resolve c, look up app, and 404 if app.AccountID != acct.ID. A
// foreign bearer that targets a cron owned by another account must
// get 404 (NOT 200) AND must NOT emit cron.updated / cron.deleted for
// the legitimate owner — the audit row would otherwise become a
// side-channel confirming another account's cron id even though the
// call itself was rejected.
func TestAuditEvents_CronForeignOwnerReturns404DoesNotEmit(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedAppForAudit(t, e, "audit-foreign-cron")

	createRec := e.do(t, http.MethodPost, "/v1/crons",
		api.CreateCronRequest{AppID: app.ID, Schedule: "*/5 * * * *", Path: "/tick"}, nil)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("seed cron: code=%d body=%s", createRec.Code, createRec.Body.String())
	}
	var created api.CronResponse
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode cron: %v", err)
	}

	// Foreign account + its own bearer on the SAME store / handler.
	// Mirrors the otherAcct pattern in handlers_quota_test.go:135.
	otherAcct, err := e.store.CreateAccount(context.Background(), "foreign-cron@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("seed foreign account: %v", err)
	}
	ptB, hashB, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(context.Background(), otherAcct.ID, hashB, "foreign", api.ScopesAdminOnly); err != nil {
		t.Fatalf("seed foreign key: %v", err)
	}

	// 1) Foreign PATCH — must 404.
	newSched := "*/15 * * * *"
	patchRec := doAsAccount(t, e, ptB, http.MethodPatch, "/v1/crons/"+created.ID,
		api.UpdateCronRequest{Schedule: &newSched})
	if patchRec.Code != http.StatusNotFound {
		t.Errorf("foreign PATCH /v1/crons/%s: code=%d, want 404; body=%s",
			created.ID, patchRec.Code, patchRec.Body.String())
	}

	// 2) Foreign DELETE — must 404.
	delRec := doAsAccount(t, e, ptB, http.MethodDelete, "/v1/crons/"+created.ID, nil)
	if delRec.Code != http.StatusNotFound {
		t.Errorf("foreign DELETE /v1/crons/%s: code=%d, want 404; body=%s",
			created.ID, delRec.Code, delRec.Body.String())
	}

	// 3) Read back events for the LEGITIMATE owner. The foreign probes
	// must not have produced cron.updated or cron.deleted rows. The
	// cron.created row from the seed step is allowed to remain.
	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if found := findEventByKind(rows, "cron.updated"); found != nil {
		t.Errorf("foreign PATCH must NOT emit cron.updated for the legitimate owner, found: %+v", found)
	}
	if found := findEventByKind(rows, "cron.deleted"); found != nil {
		t.Errorf("foreign DELETE must NOT emit cron.deleted for the legitimate owner, found: %+v", found)
	}
	// Cron should still exist (no foreign-side state mutation).
	if _, err := e.store.CronByID(context.Background(), created.ID); err != nil {
		t.Errorf("foreign DELETE must not have removed the cron, CronByID err: %v", err)
	}
}

// mustAuditEvent is the SA5011 escape hatch for the key.created /
// audit-row lookups: ListEvents can legitimately return 0 rows of a
// given Kind, but we want a real event for assertions below. A helper
// that t.Fatal()s and returns the value lets staticcheck see the value
// is non-nil at the call site.
func mustAuditEvent(t *testing.T, ev *state.Event, msg string) *state.Event {
	t.Helper()
	if ev == nil {
		t.Fatal(msg)
	}
	return ev
}

// mustAuditEventResponse is the same SA5011 escape hatch for the
// *api.AuditEventResponse type (the Wire-level DTO, not the store
// row). The list-shaped audit tests walk list.Events looking for a
// matching Kind and want a real response to assert against.
func mustAuditEventResponse(t *testing.T, ev *api.AuditEventResponse, msg string) *api.AuditEventResponse {
	t.Helper()
	if ev == nil {
		t.Fatal(msg)
	}
	return ev
}

// TestListAuditEvents_LivenessKinds (issue #554 / ADR-079 /
// AC #5) pins the customer-facing audit filter for the two
// liveness event kinds. The audit rows are emitted at
// pkg/sched/engine.go:3729 (transitionWithKind writes the kind
// `liveness_failed`) +
// pkg/sched/engine.go:3828 (instances.parked_liveness_exhausted).
//
// The two kinds do NOT share a single prefix — the destroy row
// starts with `liveness_` (no `instances.` prefix) and the park
// row starts with `instances.parked_`. The customer-facing
// dashboard therefore fires two queries (one per kind) and the
// audit handler's `?kind_prefix=` filter handles each on its
// own. This test pins both:
//
//   - `?kind_prefix=liveness_` — surfaces the 3 destroy rows.
//   - `?kind_prefix=instances.parked_liveness_` — surfaces the
//     1 park row.
//
// Without these pins, a future rename of either kind (e.g.
// dropping the `instances.` prefix on the park row, splitting
// the destroy row into `liveness.failed` + `liveness.parked`)
// would silently break the dashboard's "liveness history" view.
func TestListAuditEvents_LivenessKinds(t *testing.T) {
	e := setup(t, api.PlanPro)

	// The audit-events list endpoint is per-account-scoped —
	// the subject column is opaque to the handler but the
	// account-id filter is the query that gates results. Match
	// the cron-fired test shape: subject = e.acct.ID, with the
	// deployment id surfaced via the data JSON.
	subject := e.acct.ID

	// Three destroys + one park — mirrors the AC #3 envelope
	// (3 destroys in 5 min trips ParkDeployment which emits the
	// park audit row). The destroy row's kind is the bare
	// `liveness_failed` (transitionWithKind passes the kind
	// verbatim — no `instances.` prefix is added). The park
	// row's kind is the fully-qualified `instances.parked_*`.
	for i := 0; i < 3; i++ {
		payload, err := json.Marshal(map[string]any{
			"deployment": "dep-liveness-test",
			"reason":     "liveness_n_consecutive",
		})
		if err != nil {
			t.Fatalf("marshal liveness_failed: %v", err)
		}
		if err := e.store.AppendEvent(context.Background(), "schedd", "liveness_failed", &subject, payload); err != nil {
			t.Fatalf("AppendEvent liveness_failed[%d]: %v", i, err)
		}
	}
	parkPayload, err := json.Marshal(map[string]any{
		"deployment":    "dep-liveness-test",
		"reason":        "liveness_exhausted",
		"now":           time.Now().UTC().Format(time.RFC3339Nano),
		"window_recent": 3,
	})
	if err != nil {
		t.Fatalf("marshal parked: %v", err)
	}
	if err := e.store.AppendEvent(context.Background(), "schedd", "instances.parked_liveness_exhausted", &subject, parkPayload); err != nil {
		t.Fatalf("AppendEvent parked: %v", err)
	}

	// 1) kind_prefix=liveness_ — the per-destroy kind
	// (transitionWithKind passes "liveness_failed" verbatim).
	rec := e.do(t, http.MethodGet, "/v1/audit-events?kind_prefix=liveness_", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/audit-events?kind_prefix=liveness_: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var destroyList api.ListAuditEventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &destroyList); err != nil {
		t.Fatalf("decode destroy list: %v body=%s", err, rec.Body.String())
	}
	var livenessFails int
	for _, ev := range destroyList.Events {
		if ev.Kind == "liveness_failed" {
			livenessFails++
		}
	}
	if livenessFails != 3 {
		t.Errorf("liveness_failed count = %d, want 3 (AC #5: per-destroy audit row)", livenessFails)
	}

	// 2) kind_prefix=instances.parked_liveness_ — the per-park
	// kind (engine.go:3828 uses the fully-qualified name).
	rec = e.do(t, http.MethodGet, "/v1/audit-events?kind_prefix=instances.parked_liveness_", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/audit-events?kind_prefix=instances.parked_liveness_: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var parkList api.ListAuditEventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &parkList); err != nil {
		t.Fatalf("decode park list: %v body=%s", err, rec.Body.String())
	}
	var parked int
	for _, ev := range parkList.Events {
		if ev.Kind == "instances.parked_liveness_exhausted" {
			parked++
		}
	}
	if parked != 1 {
		t.Errorf("instances.parked_liveness_exhausted count = %d, want 1 (AC #5: per-park audit row)", parked)
	}
}

// --- production-leveling Stream A tests -----------------------------------
//
// TestListDeploymentAudit_HappyPath exercises GET
// /v1/deployments/{id}/audit end-to-end: seed an app + deployment,
// append three deployment_audit rows (deploy.created / traffic_changed
// / rolled_back), call the endpoint, assert the response carries all
// three kinds newest-first + the verbatim jsonb payload round-trips.
// Also asserts the per-deployment handler IDOR posture — a cross-account
// request 404s the same way an unknown id does.
func TestListDeploymentAudit_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	depID := seedAppWithRollout(t, e, "audit", "rolling_out", 1, 4, time.Now().Add(-1*time.Minute))

	acctID, err := uuid.Parse(e.acct.ID)
	if err != nil {
		t.Fatalf("parse acct id: %v", err)
	}
	rows := []struct {
		kind    state.DeploymentAuditKind
		actor   string
		payload map[string]any
	}{
		{state.DeployCreated, "meterd:canary_progression", map[string]any{"stage": 0}},
		{state.DeployTrafficChanged, "meterd:canary_progression", map[string]any{"from_percent": 0, "to_percent": 1}},
		{state.DeployRolledBack, "operator:cli:recover_rollout", map[string]any{"reason": "manual-test"}},
	}
	for _, r := range rows {
		data, err := json.Marshal(r.payload)
		if err != nil {
			t.Fatalf("marshal %s: %v", r.kind, err)
		}
		acctCopy := acctID
		if _, err := e.store.AppendDeploymentAudit(context.Background(), state.DeploymentAudit{
			DeploymentID: mustParseUUID(t, depID),
			AccountID:    &acctCopy,
			Kind:         r.kind,
			Actor:        r.actor,
			Data:         data,
		}); err != nil {
			t.Fatalf("append %s: %v", r.kind, err)
		}
	}

	rec := e.do(t, http.MethodGet, "/v1/deployments/"+depID+"/audit?limit=50", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/deployments/%s/audit: code=%d body=%s", depID, rec.Code, rec.Body.String())
	}
	var out api.ListDeploymentAuditResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode list: %v body=%s", err, rec.Body.String())
	}
	if out.Limit != 50 {
		t.Errorf("limit=%d, want 50", out.Limit)
	}
	if len(out.Items) != len(rows) {
		t.Fatalf("items=%d, want %d", len(out.Items), len(rows))
	}
	// Newest-first ordering: the rolled_back row must be index 0.
	if out.Items[0].Kind != "deploy.rolled_back" {
		t.Errorf("newest kind=%q, want deploy.rolled_back", out.Items[0].Kind)
	}
	if out.Items[0].Actor != "operator:cli:recover_rollout" {
		t.Errorf("newest actor=%q, want operator:cli:recover_rollout", out.Items[0].Actor)
	}
	// Data payload round-trips verbatim.
	var got map[string]any
	if err := json.Unmarshal(out.Items[0].Data, &got); err != nil {
		t.Fatalf("unmarshal newest data: %v body=%s", err, string(out.Items[0].Data))
	}
	if got["reason"] != "manual-test" {
		t.Errorf("newest data.reason=%v, want manual-test", got["reason"])
	}
}

// TestListDeploymentAudit_CrossAccount_IDOR: a request from a
// different account for the same deployment id must 404 — the
// handler enforces the IDOR posture via DeploymentByID + AppByID
// + AccountID check before reading the audit table.
func TestListDeploymentAudit_CrossAccount_IDOR(t *testing.T) {
	e1 := setup(t, api.PlanPro)
	depID := seedAppWithRollout(t, e1, "aud-cross", "rolling_out", 1, 4, time.Now().Add(-1*time.Minute))

	e2 := setup(t, api.PlanPro)
	rec := e2.do(t, http.MethodGet, "/v1/deployments/"+depID+"/audit", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account status %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// TestListDeploymentAudit_InvalidLimit: ?limit=0 / ?limit=-1 /
// ?limit=abc all return 400 invalid_limit, never 500. The closed
// 1..50 band mirrors listAuditEventsLimit (50 default, 50 max).
func TestListDeploymentAudit_InvalidLimit(t *testing.T) {
	e := setup(t, api.PlanPro)
	depID := seedAppWithRollout(t, e, "audit-bad-limit", "rolling_out", 1, 4, time.Now().Add(-1*time.Minute))

	for _, raw := range []string{"0", "-1", "abc"} {
		t.Run(raw, func(t *testing.T) {
			rec := e.do(t, http.MethodGet, "/v1/deployments/"+depID+"/audit?limit="+raw, nil, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("limit=%s: status %d, body %s", raw, rec.Code, rec.Body.String())
			}
		})
	}
}

// mustParseUUID is a test helper that fatals on a bad uuid parse.
// The seedAppWithRollout helper returns the deployment id as a
// canonical uuid string (e.mstore.CreateDeployment returns uuid.UUID
// shaped id), so the conversion is safe in tests.
func mustParseUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	u, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("parse uuid %q: %v", s, err)
	}
	return u
}
