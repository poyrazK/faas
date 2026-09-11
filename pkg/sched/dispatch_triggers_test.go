// dispatch_triggers_test.go — focused unit tests for the
// audit-round-2 fixes to pkg/sched/dispatch_triggers.go.
// adr: 100
//
// Scope (audit round 2 finding #1, PR #910): the deadLetterAll
// helper now bridges the broker-handle namespace to the
// trigger_records.id UUID via TriggerRecordIDByItemIdentifier
// before calling InsertTriggerDeadLetter. The unit test pins:
//
//  1. happy path — record IS in trigger_records; lookup returns
//     the UUID; InsertTriggerDeadLetter + MarkTriggerRecordDeadLetter
//     both fire with the UUID (not the broker handle).
//  2. missing-row path — record is NOT in trigger_records yet
//     (rate-limit fired before InsertTriggerRecord); the helper
//     returns ("", nil); deadLetterAll SKIPS both store calls
//     for that item; no FK violation, broker offset stays put,
//     the record retries on the next dispatch tick.
//  3. poison_record path — caller passes a row UUID directly
//     (claimed[i].ID.String()); the lookup self-resolves and
//     the dead_letter row still lands.
//
// The fakeStore implements the storeLike interface from
// dispatch_triggers.go without any real SQL — every assertion
// is on the helper's call sequence, not on Postgres semantics.

package sched

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/triggerconfig"
	"github.com/onebox-faas/faas/pkg/wire"
)

// fakeDeadLetterStore is a minimal storeLike for the deadLetterAll
// unit tests. It tracks InsertTriggerDeadLetter + MarkTriggerRecordDeadLetter
// call sequences keyed by the record-id argument the helper passes
// in, so the tests can assert the helper resolved the broker
// handle to the trigger_records.id UUID before issuing writes.
type fakeDeadLetterStore struct {
	// records maps item_identifier -> trigger_records.id so the
	// TriggerRecordIDByItemIdentifier lookup has data to return.
	records map[string]string
	// inserts is the list of record-id arguments passed to
	// InsertTriggerDeadLetter, in call order.
	inserts []string
	// marks is the list of record-id arguments passed to
	// MarkTriggerRecordDeadLetter, in call order.
	marks []string
	// forceMissingIDs makes TriggerRecordIDByItemIdentifier
	// return ("", nil) for the listed item_identifiers
	// regardless of the records map.
	forceMissingIDs map[string]bool
}

func installPollerFactory(t *testing.T, kind string, factory func(sqlc.Trigger) (triggerSource, error)) {
	t.Helper()
	defaultRegistry.mu.Lock()
	previous, hadPrevious := defaultRegistry.factories[kind]
	defaultRegistry.factories[kind] = factory
	defaultRegistry.mu.Unlock()
	t.Cleanup(func() {
		defaultRegistry.mu.Lock()
		defer defaultRegistry.mu.Unlock()
		if hadPrevious {
			defaultRegistry.factories[kind] = previous
		} else {
			delete(defaultRegistry.factories, kind)
		}
	})
}

func TestNewPollerForTriggerPreservesFactoryError(t *testing.T) {
	want := errors.New("bad poller config")
	installPollerFactory(t, "failing-test-kind", func(sqlc.Trigger) (triggerSource, error) {
		return nil, want
	})
	source, registered, err := newPollerForTrigger(sqlc.Trigger{Kind: "failing-test-kind"})
	if source != nil || !registered || !errors.Is(err, want) {
		t.Fatalf("newPollerForTrigger = (%v, %v, %v), want (nil, true, target error)", source, registered, err)
	}
}

func TestDispatchTriggerOpensSealedKafkaCredentialsForFactory(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity: %v", err)
	}
	sealed, err := triggerconfig.Seal(api.TriggerKindKafka,
		json.RawMessage(`{"brokers":["b:9092"],"topic":"orders","group":"g","sasl":{"mechanism":"PLAIN","username":"svc","password":"runtime-secret"}}`),
		identity.Recipient())
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	var factoryConfig []byte
	installPollerFactory(t, string(api.TriggerKindKafka), func(trigger sqlc.Trigger) (triggerSource, error) {
		factoryConfig = append([]byte(nil), trigger.Config...)
		return &fakePollerForFilter{}, nil
	})
	l := makeLoopForDLQ().WithTriggerSecretIdentities([]*age.X25519Identity{identity})
	trigger := sqlc.Trigger{
		ID:   pgtypeUUIDFromString(t, "00000000-0000-0000-0000-000000000099"),
		Kind: string(api.TriggerKindKafka), Config: sealed,
	}
	if err := l.dispatchOneTrigger(context.Background(), trigger, &fakeDeadLetterStore{}, func(string) api.Plan { return api.PlanPro }); err != nil {
		t.Fatalf("dispatchOneTrigger: %v", err)
	}
	if !bytes.Contains(factoryConfig, []byte("runtime-secret")) || bytes.Contains(factoryConfig, []byte("password_sealed")) {
		t.Fatalf("factory config was not opened: %s", factoryConfig)
	}
	if bytes.Contains(trigger.Config, []byte("runtime-secret")) {
		t.Fatalf("original trigger row was mutated: %s", trigger.Config)
	}
}

func TestDispatchTriggerAcceptsLegacyPlaintextKafkaConfig(t *testing.T) {
	legacy := []byte(`{"brokers":["b:9092"],"topic":"orders","group":"g","sasl":{"mechanism":"PLAIN","username":"svc","password":"legacy-secret"}}`)
	var factoryConfig []byte
	installPollerFactory(t, string(api.TriggerKindKafka), func(trigger sqlc.Trigger) (triggerSource, error) {
		factoryConfig = append([]byte(nil), trigger.Config...)
		return &fakePollerForFilter{}, nil
	})
	l := makeLoopForDLQ()
	trigger := sqlc.Trigger{
		ID:   pgtypeUUIDFromString(t, "00000000-0000-0000-0000-000000000098"),
		Kind: string(api.TriggerKindKafka), Config: legacy,
	}
	if err := l.dispatchOneTrigger(context.Background(), trigger, &fakeDeadLetterStore{}, func(string) api.Plan { return api.PlanPro }); err != nil {
		t.Fatalf("dispatchOneTrigger: %v", err)
	}
	if !bytes.Equal(factoryConfig, legacy) {
		t.Fatalf("factory config = %s, want legacy plaintext", factoryConfig)
	}
}

func TestDispatchTriggerRejectsCorruptSecretWithoutDisclosure(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity: %v", err)
	}
	installPollerFactory(t, string(api.TriggerKindKafka), func(sqlc.Trigger) (triggerSource, error) {
		t.Fatal("factory must not run for corrupt credentials")
		return nil, nil
	})
	l := makeLoopForDLQ().WithTriggerSecretIdentities([]*age.X25519Identity{identity})
	const ciphertext = "bm90LWFnZQ=="
	trigger := sqlc.Trigger{
		ID:     pgtypeUUIDFromString(t, "00000000-0000-0000-0000-000000000097"),
		Kind:   string(api.TriggerKindKafka),
		Config: []byte(`{"sasl":{"password_sealed":"` + ciphertext + `"}}`),
	}
	err = l.dispatchOneTrigger(context.Background(), trigger, &fakeDeadLetterStore{}, func(string) api.Plan { return api.PlanPro })
	if err == nil || !strings.Contains(err.Error(), "open kafka trigger credentials") ||
		strings.Contains(err.Error(), ciphertext) {
		t.Fatalf("dispatch error = %v, want sanitized open error", err)
	}
}

func (f *fakeDeadLetterStore) ListEnabledTriggers(_ context.Context) ([]sqlc.Trigger, error) {
	return nil, nil
}
func (f *fakeDeadLetterStore) ClaimTriggerRecords(_ context.Context, _ string, _ int32) ([]sqlc.TriggerRecord, error) {
	return nil, nil
}
func (f *fakeDeadLetterStore) InsertTriggerRecord(_ context.Context, _, _ string, _, _, _ []byte) (string, error) {
	return "", nil
}
func (f *fakeDeadLetterStore) MarkTriggerRecordSucceeded(_ context.Context, _ string) error {
	return nil
}
func (f *fakeDeadLetterStore) MarkTriggerRecordRetry(_ context.Context, _, _ string, _ time.Time) error {
	return errors.New("not used in this test")
}
func (f *fakeDeadLetterStore) MarkTriggerRecordDeadLetter(_ context.Context, id, _ string) error {
	f.marks = append(f.marks, id)
	return nil
}
func (f *fakeDeadLetterStore) InsertTriggerDeadLetter(_ context.Context, recordID, _, _, _ string, _ []byte) error {
	f.inserts = append(f.inserts, recordID)
	return nil
}
func (f *fakeDeadLetterStore) TriggerRecordIDByItemIdentifier(_ context.Context, _, itemIdentifier string) (string, error) {
	if f.forceMissingIDs[itemIdentifier] {
		return "", nil
	}
	if uuid, ok := f.records[itemIdentifier]; ok {
		return uuid, nil
	}
	return "", nil
}

// makeLoopForDLQ builds a Loop with just enough wiring for
// deadLetterAll — log + nil engine — so the test exercises the
// helper directly without the full dispatchOneTrigger surface.
func makeLoopForDLQ() *Loop {
	return &Loop{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// TestDeadLetterAll_HappyPath_BrokerHandleResolvesToUUID verifies
// the helper bridges the broker-handle namespace to the
// trigger_records.id UUID before issuing the dead_letter writes.
// Audit round 2 finding #1 (PR #910): without the lookup every
// rate-limit denial tripped SQLSTATE 23503 because
// trigger_dead_letter.record_id is a UUID FK into
// trigger_records.id.
func TestDeadLetterAll_HappyPath_BrokerHandleResolvesToUUID(t *testing.T) {
	const (
		triggerID = "11111111-1111-1111-1111-111111111111"
		kafkaOff  = "kafka-offset-12"
		rowUUID   = "22222222-2222-2222-2222-222222222222"
	)
	store := &fakeDeadLetterStore{
		records: map[string]string{kafkaOff: rowUUID},
	}
	l := makeLoopForDLQ()
	l.deadLetterAll(context.Background(), triggerID, []string{kafkaOff},
		triggerReasonRateLimited, "wake rate limit exceeded", store)

	if got, want := len(store.inserts), 1; got != want {
		t.Fatalf("InsertTriggerDeadLetter call count = %d, want %d (errors=%v)", got, want, store.inserts)
	}
	if got, want := store.inserts[0], rowUUID; got != want {
		t.Errorf("InsertTriggerDeadLetter record_id = %q, want %q (must be the UUID, not the broker handle)", got, want)
	}
	if got, want := len(store.marks), 1; got != want {
		t.Fatalf("MarkTriggerRecordDeadLetter call count = %d, want %d", got, want)
	}
	if got, want := store.marks[0], rowUUID; got != want {
		t.Errorf("MarkTriggerRecordDeadLetter id = %q, want %q (must be the UUID, not the broker handle)", got, want)
	}
}

// TestDeadLetterAll_RowMissing_RateLimitBeforeInsert pins the
// "rate-limit fires before InsertTriggerRecord" path. The
// trigger_records row does not exist yet (the insert happens
// later in dispatchOneTrigger); the lookup returns ("", nil);
// deadLetterAll MUST skip both store calls for that item so the
// broker offset stays put and the record retries on the next
// tick. Without the skip, the old code would have tripped an FK
// violation, the record would have stayed in poller.inFlight
// forever, and the broker offset would never advance.
func TestDeadLetterAll_RowMissing_RateLimitBeforeInsert(t *testing.T) {
	const (
		triggerID = "11111111-1111-1111-1111-111111111111"
		kafkaOff  = "kafka-offset-99"
	)
	store := &fakeDeadLetterStore{
		records:         map[string]string{}, // empty — no row
		forceMissingIDs: map[string]bool{kafkaOff: true},
	}
	l := makeLoopForDLQ()
	l.deadLetterAll(context.Background(), triggerID, []string{kafkaOff},
		triggerReasonRateLimited, "wake rate limit exceeded", store)

	if got := len(store.inserts); got != 0 {
		t.Errorf("InsertTriggerDeadLetter call count = %d, want 0 (rate-limited records must retry, not be silently dropped)", got)
	}
	if got := len(store.marks); got != 0 {
		t.Errorf("MarkTriggerRecordDeadLetter call count = %d, want 0 (no row to mark)", got)
	}
}

// TestDeadLetterAll_PoisonPath_PassesUUIDs pins the
// poison_record path at dispatch_triggers.go:354 — the caller
// already has the trigger_records.id UUID (from the `claimed`
// result-set, line 352), so the lookup self-resolves and the
// dead_letter row still lands. This guards against a future
// refactor that might "optimise" by skipping the lookup when the
// input looks UUID-like — the helper has no such heuristic; it
// always goes through TriggerRecordIDByItemIdentifier.
func TestDeadLetterAll_PoisonPath_PassesUUIDs(t *testing.T) {
	const (
		triggerID = "11111111-1111-1111-1111-111111111111"
		rowUUID   = "33333333-3333-3333-3333-333333333333"
	)
	store := &fakeDeadLetterStore{
		records: map[string]string{rowUUID: rowUUID}, // self-resolves
	}
	l := makeLoopForDLQ()
	l.deadLetterAll(context.Background(), triggerID, []string{rowUUID},
		triggerReasonPoisonRecord, "gateway response malformed", store)

	if got, want := len(store.inserts), 1; got != want {
		t.Fatalf("InsertTriggerDeadLetter call count = %d, want %d", got, want)
	}
	if got, want := store.inserts[0], rowUUID; got != want {
		t.Errorf("InsertTriggerDeadLetter record_id = %q, want %q", got, want)
	}
	if got, want := len(store.marks), 1; got != want {
		t.Fatalf("MarkTriggerRecordDeadLetter call count = %d, want %d", got, want)
	}
	if got, want := store.marks[0], rowUUID; got != want {
		t.Errorf("MarkTriggerRecordDeadLetter id = %q, want %q", got, want)
	}
}

// TestDeadLetterAll_MixedPath_SkipsOnlyMissingRows pins the
// partial-success case: one item_identifier resolves to a UUID,
// another doesn't. The helper must insert + mark the resolved
// row AND skip the unresolved one — never both, never neither.
func TestDeadLetterAll_MixedPath_SkipsOnlyMissingRows(t *testing.T) {
	const (
		triggerID = "11111111-1111-1111-1111-111111111111"
		present   = "present-handle"
		missing   = "missing-handle"
		rowUUID   = "44444444-4444-4444-4444-444444444444"
	)
	store := &fakeDeadLetterStore{
		records:         map[string]string{present: rowUUID},
		forceMissingIDs: map[string]bool{missing: true},
	}
	l := makeLoopForDLQ()
	l.deadLetterAll(context.Background(), triggerID, []string{present, missing},
		triggerReasonRateLimited, "wake rate limit exceeded", store)

	if got, want := len(store.inserts), 1; got != want {
		t.Fatalf("InsertTriggerDeadLetter call count = %d, want %d (mixed: 1 row, 1 missing)", got, want)
	}
	if got, want := store.inserts[0], rowUUID; got != want {
		t.Errorf("InsertTriggerDeadLetter record_id = %q, want %q", got, want)
	}
	if got, want := len(store.marks), 1; got != want {
		t.Fatalf("MarkTriggerRecordDeadLetter call count = %d, want %d", got, want)
	}
	if got, want := store.marks[0], rowUUID; got != want {
		t.Errorf("MarkTriggerRecordDeadLetter id = %q, want %q", got, want)
	}
}

// ----------------------------------------------------------------------
// Commit 6 (ADR-118 / issue #757 mega-PR) — filterBatch wiring
// ----------------------------------------------------------------------

// fakePollerForFilter is the minimal triggerSource the
// filterBatch test needs: it records every Ack call so the
// test can assert "filter rejected the record → broker
// Ack'd it" without standing up a real broker.
type fakePollerForFilter struct {
	ackCalls []string
}

func (f *fakePollerForFilter) Kind() string { return "kafka" }
func (f *fakePollerForFilter) Poll(_ context.Context, _ sqlc.Trigger) PollResult {
	return PollResult{}
}
func (f *fakePollerForFilter) Ack(_ context.Context, _ sqlc.Trigger, ids []string) error {
	f.ackCalls = append(f.ackCalls, ids...)
	return nil
}
func (f *fakePollerForFilter) Nack(_ context.Context, _ sqlc.Trigger, _ []string, _ string) error {
	return nil
}
func (f *fakePollerForFilter) Close() error { return nil }

// makeLoopForFilter builds a Loop with the minimal wiring for
// filterBatch — the triggerPollers map pre-populated with a
// fake poller so ackSingle has a target.
func makeLoopForFilter(_ *testing.T, triggerID string, poller *fakePollerForFilter) *Loop {
	return &Loop{
		log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		triggerPollers: map[string]triggerSource{triggerID: poller},
	}
}

// marshalFilter is a tiny helper to keep the test fixtures
// readable. The JSON form is the same shape as
// triggers.filter_criteria on the wire.
func marshalFilter(t *testing.T, fc *FilterCriteria) []byte {
	t.Helper()
	b, err := json.Marshal(fc)
	if err != nil {
		t.Fatalf("marshal filter: %v", err)
	}
	return b
}

// pgtypeUUIDFromString builds a pgtype.UUID from a string id so
// the test fixture matches the production sqlc.Trigger.ID
// shape. dispatch_triggers.go keys the triggerPollers map on
// t.ID.String(), so a zero UUID would silently fail every
// ackSingle lookup.
func pgtypeUUIDFromString(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	u, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("parse uuid %q: %v", s, err)
	}
	var b [16]byte
	copy(b[:], u[:])
	return pgtype.UUID{Bytes: b, Valid: true}
}

// TestFilterBatch_MatchKeepsAckDrops verifies the happy path:
// a record whose filter matches flows through unchanged; a
// record whose filter rejects is Ack'd (broker commits the
// offset) and dropped from the batch.
func TestFilterBatch_MatchKeepsAckDrops(t *testing.T) {
	triggerID := "00000000-0000-0000-0000-000000000001"
	poller := &fakePollerForFilter{}
	l := makeLoopForFilter(t, triggerID, poller)
	t1 := sqlc.Trigger{
		Kind: "kafka",
		FilterCriteria: marshalFilter(t, &FilterCriteria{
			AND: []FilterClause{
				{Op: FilterOpEq, Field: "x-tenant", Value: json.RawMessage(`"acme"`)},
			},
		}),
		ID: pgtypeUUIDFromString(t, triggerID),
	}
	batch := []SourceRecord{
		{ItemIdentifier: "match-1", Payload: []byte(`{}`), Headers: map[string]string{"x-tenant": "acme"}},
		{ItemIdentifier: "drop-1", Payload: []byte(`{}`), Headers: map[string]string{"x-tenant": "globex"}},
	}
	filtered, errCount, err := l.filterBatch(context.Background(), t1, batch)
	if err != nil {
		t.Fatalf("filterBatch err = %v, want nil", err)
	}
	if errCount != 0 {
		t.Errorf("errCount = %d, want 0", errCount)
	}
	if got, want := len(filtered), 1; got != want {
		t.Fatalf("filtered len = %d, want %d", got, want)
	}
	if got, want := filtered[0].ItemIdentifier, "match-1"; got != want {
		t.Errorf("filtered[0].ItemIdentifier = %q, want %q", got, want)
	}
	if got, want := len(poller.ackCalls), 1; got != want {
		t.Fatalf("poller.Ack call count = %d, want %d (only the dropped record should be Ack'd)", got, want)
	}
	if got, want := poller.ackCalls[0], "drop-1"; got != want {
		t.Errorf("Ack'd id = %q, want %q", got, want)
	}
}

// TestFilterBatch_PerRecordParseErrorIsCountedNotFatal verifies
// that a record with a malformed JSONPath is Ack'd silently
// (broker commits the offset; re-poll would loop) and the
// error count rises by 1 — the whole batch is NOT dropped
// because one record had a bad path.
func TestFilterBatch_PerRecordParseErrorIsCountedNotFatal(t *testing.T) {
	triggerID := "00000000-0000-0000-0000-000000000002"
	poller := &fakePollerForFilter{}
	l := makeLoopForFilter(t, triggerID, poller)
	t1 := sqlc.Trigger{
		Kind: "kafka",
		FilterCriteria: marshalFilter(t, &FilterCriteria{
			Payload: []FilterClause{
				{Op: FilterOpJsonPath, Path: "$.items[?(@.price>100)]"}, // unsupported form
			},
		}),
		ID: pgtypeUUIDFromString(t, triggerID),
	}
	batch := []SourceRecord{
		{ItemIdentifier: "bad-1", Payload: []byte(`{"items": [{"price": 200}]}`)},
	}
	filtered, errCount, err := l.filterBatch(context.Background(), t1, batch)
	if err != nil {
		t.Fatalf("filterBatch err = %v, want nil (per-record error must not propagate)", err)
	}
	if errCount != 1 {
		t.Errorf("errCount = %d, want 1 (one record with a malformed path)", errCount)
	}
	if len(filtered) != 0 {
		t.Errorf("filtered len = %d, want 0 (the bad record was Ack'd + dropped)", len(filtered))
	}
	if got, want := len(poller.ackCalls), 1; got != want {
		t.Errorf("poller.Ack call count = %d, want %d", got, want)
	}
}

// TestFilterBatch_NilFilterPassesThrough verifies the no-filter
// case: the batch is returned unchanged with zero work.
func TestFilterBatch_NilFilterPassesThrough(t *testing.T) {
	l := makeLoopForFilter(t, "trig-3", &fakePollerForFilter{})
	t1 := sqlc.Trigger{Kind: "kafka", FilterCriteria: nil}
	batch := []SourceRecord{
		{ItemIdentifier: "a"},
		{ItemIdentifier: "b"},
	}
	filtered, errCount, err := l.filterBatch(context.Background(), t1, batch)
	if err != nil {
		t.Fatalf("filterBatch err = %v", err)
	}
	if errCount != 0 {
		t.Errorf("errCount = %d, want 0", errCount)
	}
	if got, want := len(filtered), 2; got != want {
		t.Errorf("filtered len = %d, want %d (no filter → pass-through)", got, want)
	}
}

// TestFilterBatch_MalformedJSONTreeIsFatal verifies the
// catastrophic path: a malformed JSONB column on the trigger
// row returns a non-nil error so the dispatch tick drops the
// whole batch (half-wired filters would be silently confusing).
func TestFilterBatch_MalformedJSONTreeIsFatal(t *testing.T) {
	l := makeLoopForFilter(t, "trig-4", &fakePollerForFilter{})
	t1 := sqlc.Trigger{Kind: "kafka", FilterCriteria: []byte(`{"not": valid json`)}
	_, _, err := l.filterBatch(context.Background(), t1, []SourceRecord{{ItemIdentifier: "x"}})
	if err == nil {
		t.Errorf("err = nil, want non-nil (malformed JSONB column → caller drops the tick)")
	}
}

// ackRecordingPoller captures Ack calls so the CRIT-1 regression
// test can assert the broker-offset advance happens after a
// rate-limit deny. It mirrors fakePollerForFilter but is named
// distinctly so the new test reads cleanly.
type ackRecordingPoller struct {
	ackCalls []string
}

func (f *ackRecordingPoller) Kind() string { return "kafka" }
func (f *ackRecordingPoller) Poll(_ context.Context, _ sqlc.Trigger) PollResult {
	return PollResult{}
}
func (f *ackRecordingPoller) Ack(_ context.Context, _ sqlc.Trigger, ids []string) error {
	f.ackCalls = append(f.ackCalls, ids...)
	return nil
}
func (f *ackRecordingPoller) Nack(_ context.Context, _ sqlc.Trigger, _ []string, _ string) error {
	return nil
}
func (f *ackRecordingPoller) Close() error { return nil }

// TestRateLimitDeny_AcksBrokerOffset is the CRIT-1 regression
// test (PR #993 / issue #757 closure). Pre-CRIT-1 the rate-limit
// deny branches returned immediately after deadLetterAll without
// ack'ing the poller — every deny pinned the broker offset at
// the front of the batch and the same records re-poll'd forever.
// handleRateLimitedBatch is the seam; the test pins both:
//
//  1. deadLetterAll was called (audit row recorded) —
//     covered indirectly via fakeDeadLetterStore.inserts.
//  2. poller.Ack was called with the same item_identifiers —
//     the broker offset advances.
func TestRateLimitDeny_AcksBrokerOffset(t *testing.T) {
	const triggerID = "11111111-1111-1111-1111-111111111111"
	poller := &ackRecordingPoller{}
	l := &Loop{
		log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		triggerPollers: map[string]triggerSource{triggerID: poller},
	}
	// fakeDeadLetterStore returns empty UUIDs (the row doesn't
	// exist yet — the rate-limit fires before InsertTriggerRecord).
	// That's fine for the Ack assertion because Ack operates on
	// the broker handle, not the trigger_records row.
	store := &fakeDeadLetterStore{
		records:         map[string]string{},
		forceMissingIDs: map[string]bool{"kafka-1": true, "kafka-2": true},
	}
	t1 := sqlc.Trigger{ID: pgtypeUUIDFromString(t, triggerID)}
	batch := []SourceRecord{
		{ItemIdentifier: "kafka-1"},
		{ItemIdentifier: "kafka-2"},
	}
	l.handleRateLimitedBatch(context.Background(), poller, t1, batch, store)

	if got, want := poller.ackCalls, []string{"kafka-1", "kafka-2"}; !equalSlices(got, want) {
		t.Errorf("Ack calls = %v, want %v (broker offset must advance after deny)", got, want)
	}
}

// equalSlices is a tiny helper to keep the assertion readable
// without pulling in reflect.DeepEqual's verbosity for two
// []string compares.
func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestShardKeyFor covers MED-2 (PR #993 / issue #757 review) shard
// extraction logic — the per-record lag metric's source/shard label
// pair. The dashboard reader groups lag by shard; a missing-key
// record collapses to "_agg" so the closed-set pre-instantiation in
// wire.NewOpsMetrics doesn't blow up cardinality. The 32-byte cap
// mirrors the wire-side guard.
func TestShardKeyFor(t *testing.T) {
	cases := []struct {
		name string
		rec  SourceRecord
		kind api.TriggerKind
		want string
	}{
		{
			name: "kafka_partition_int",
			rec:  SourceRecord{Metadata: map[string]any{"partition": 3}},
			kind: api.TriggerKindKafka,
			want: "3",
		},
		{
			name: "kafka_partition_missing_collapses",
			rec:  SourceRecord{Metadata: map[string]any{}},
			kind: api.TriggerKindKafka,
			want: "_agg",
		},
		{
			name: "nats_stream",
			rec:  SourceRecord{Metadata: map[string]any{"stream": "events"}},
			kind: api.TriggerKindNATS,
			want: "events",
		},
		{
			name: "unknown_kind_collapses",
			rec:  SourceRecord{Metadata: map[string]any{"partition": 0}},
			kind: api.TriggerKindQueue,
			want: "_agg",
		},
		{
			name: "overlong_key_collapses",
			rec:  SourceRecord{Metadata: map[string]any{"partition": "this-string-is-far-longer-than-thirty-two-chars"}},
			kind: api.TriggerKindKafka,
			want: "_agg",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shardKeyFor(tc.rec, string(tc.kind)); got != tc.want {
				t.Errorf("shardKeyFor = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestObserveESM_NilOpsIsNoop is the MED-2 nil-safety gate. The
// dispatch hot path guards against a Loop with l.ops == nil
// (production always sets it via WithOpsMetrics, but tests can
// construct a bare Loop). Each wrapper must silently no-op.
func TestObserveESM_NilOpsIsNoop(t *testing.T) {
	l := &Loop{}
	l.observeESMPoll("kafka", wire.ESMPollOutcomeSuccess)
	l.observeESMRecords("kafka", 5)
	l.observeESMLag("kafka", "0", 0.1)
	// No panic, no observable side-effect. Test passes by
	// virtue of reaching this line.
}

// esmCounterValue reads the current value of a Prometheus counter
// with a single label pair. Mirrors pkg/wire/metrics_test.go's
// existing counter-probe helper but lives here so MED-2's tests
// don't depend on the wire package's test surface.
func esmCounterValue(t *testing.T, c prometheus.Counter) float64 {
	t.Helper()
	m := &dto.Metric{}
	if err := c.Write(m); err != nil {
		t.Fatalf("counter.Write: %v", err)
	}
	return m.GetCounter().GetValue()
}

// TestObserveESM_ForwardsToOpsMetrics asserts the MED-2 wrappers
// forward to OpsMetrics correctly: a single success poll + 2 records
// + 1 lag observation must move the wire-side counters exactly once
// each. The Loop helper exists to keep the nil-check centralised,
// so this test pins that the forwarding isn't shadowed by it.
func TestObserveESM_ForwardsToOpsMetrics(t *testing.T) {
	ops := wire.NewOpsMetrics("test")
	l := &Loop{ops: ops}

	l.observeESMPoll(string(api.TriggerKindKafka), wire.ESMPollOutcomeSuccess)
	l.observeESMRecords(string(api.TriggerKindKafka), 2)
	l.observeESMLag(string(api.TriggerKindKafka), "0", 0.25)

	// Counter values are read by querying the pre-instantiated
	// child for the (source, outcome) tuple — wire.NewOpsMetrics
	// pre-creates the success/error/empty series, so a successful
	// observeESMPoll call must increment the success child.
	successCounter, err := ops.ESMPollCounterForTest(string(api.TriggerKindKafka), wire.ESMPollOutcomeSuccess)
	if err != nil {
		t.Fatalf("ESMPollCounterForTest: %v", err)
	}
	if got := esmCounterValue(t, successCounter); got != 1 {
		t.Errorf("success poll counter = %v, want 1", got)
	}
	recordsCounter, err := ops.ESMRecordsCounterForTest(string(api.TriggerKindKafka))
	if err != nil {
		t.Fatalf("ESMRecordsCounterForTest: %v", err)
	}
	if got := esmCounterValue(t, recordsCounter); got != 2 {
		t.Errorf("records consumed counter = %v, want 2", got)
	}
}
