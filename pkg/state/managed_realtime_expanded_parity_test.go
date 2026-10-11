package state_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Run the same observable contracts against both stores. In particular, a
// failed batch/CAS must leave both the channel head and derived state intact.
type expandedRealtimeStore interface {
	state.Store
	state.ManagedRealtimeEndpointStore
	state.ManagedRealtimeHistoryStore
	state.ManagedRealtimeInboxStore
	state.ManagedRealtimeReadProgressStore
	state.ManagedRealtimeMutationStore
	state.ManagedRealtimeDirectMessageReceiptStore
	state.ManagedRealtimeReducerStore
	state.ManagedRealtimeConditionalStore
	state.ManagedRealtimeSnapshotStore
	state.ManagedRealtimeEventSchemaStore
	state.ManagedRealtimeScheduleStore
	state.ManagedRealtimeScheduleGroupStore
	state.ManagedRealtimeScheduleHistoryStore
	state.ManagedRealtimePushStore
	state.ManagedRealtimePushPreferencesStore
	state.ManagedRealtimeNotificationStore
	state.ManagedRealtimeNotificationControlStore
	state.ManagedRealtimeNotificationTimelineStore
	state.ManagedRealtimePresenceStore
}

func expandedRealtimeCheck(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func expandedRealtimeEndpoint(t *testing.T, s expandedRealtimeStore, ctx context.Context) string {
	t.Helper()
	account, err := s.CreateAccount(ctx, "realtime-contract-"+uuid.NewString()+"@example.com", api.PlanPro)
	expandedRealtimeCheck(t, err)
	app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "realtime-" + uuid.NewString()})
	expandedRealtimeCheck(t, err)
	endpoint, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(account.ID, app.ID), 10, 50)
	expandedRealtimeCheck(t, err)
	return endpoint.ID
}

func TestManagedRealtimeExpandedStoreParity(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var s expandedRealtimeStore
			ctx := context.Background()
			if backend == "memory" {
				s = state.NewMemStore()
			} else {
				pg, pgctx := pgStore(t)
				s, ctx = pg, pgctx
			}
			contracts := []struct {
				name string
				run  func(*testing.T, expandedRealtimeStore, context.Context, string)
			}{
				{"inbox_and_read_progress", expandedRealtimeInbox},
				{"direct_receipts", expandedRealtimeDirectReceipts},
				{"reducer_atomicity", expandedRealtimeReducer},
				{"typed_batch_atomicity", expandedRealtimeSchema},
				{"schedule_group_fences", expandedRealtimeSchedules},
				{"push_rotation_and_leases", expandedRealtimePush},
				{"presence_aggregation", expandedRealtimePresence},
			}
			for _, contract := range contracts {
				t.Run(contract.name, func(t *testing.T) {
					contract.run(t, s, ctx, expandedRealtimeEndpoint(t, s, ctx))
				})
			}
		})
	}
}

func expandedRealtimeInbox(t *testing.T, s expandedRealtimeStore, ctx context.Context, ep string) {
	data := []byte(`{"text":"private"}`)
	first, err := s.AppendManagedRealtimeInboxMessage(ctx, ep, "alice", data, false, "first")
	expandedRealtimeCheck(t, err)
	data[0] = 'x'
	retry, err := s.AppendManagedRealtimeInboxMessage(ctx, ep, "alice", []byte(`{"text":"private"}`), false, "first")
	expandedRealtimeCheck(t, err)
	if first.Sequence != 1 || retry.Sequence != first.Sequence {
		t.Fatalf("idempotency: first=%+v retry=%+v", first, retry)
	}
	if _, err = s.AppendManagedRealtimeInboxMessage(ctx, ep, "alice", []byte("changed"), false, "first"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("conflicting content: %v", err)
	}
	second, err := s.AppendManagedRealtimeInboxMessage(ctx, ep, "alice", []byte("second"), false, "second")
	expandedRealtimeCheck(t, err)
	bob, err := s.ReadManagedRealtimeInbox(ctx, ep, "bob", 0, 100)
	expandedRealtimeCheck(t, err)
	if len(bob.Messages) != 0 || bob.LatestSequence != 0 {
		t.Fatalf("principal leak: %+v", bob)
	}
	page, err := s.ReadManagedRealtimeInbox(ctx, ep, "alice", 0, 100)
	expandedRealtimeCheck(t, err)
	if len(page.Messages) != 2 || string(page.Messages[0].Data) != `{"text":"private"}` || second.Sequence != 2 {
		t.Fatalf("inbox page: %+v", page)
	}
	for _, consumer := range []string{"phone", "laptop"} {
		cursor, err := s.LoadManagedRealtimeInboxCursor(ctx, ep, "alice", consumer, 0)
		expandedRealtimeCheck(t, err)
		if cursor != 0 {
			t.Fatalf("initial cursor: %d", cursor)
		}
	}
	cursor, err := s.AdvanceManagedRealtimeInboxCursor(ctx, ep, "alice", "phone", 2)
	expandedRealtimeCheck(t, err)
	if cursor != 2 {
		t.Fatalf("advance cursor: %d", cursor)
	}
	cursor, err = s.AdvanceManagedRealtimeInboxCursor(ctx, ep, "alice", "phone", 1)
	expandedRealtimeCheck(t, err)
	if cursor != 2 {
		t.Fatalf("cursor regressed: %d", cursor)
	}
	cursor, err = s.GetManagedRealtimeInboxCursor(ctx, ep, "alice", "laptop")
	expandedRealtimeCheck(t, err)
	if cursor != 0 {
		t.Fatalf("device cursor leak: %d", cursor)
	}
	cursor, err = s.ResetManagedRealtimeInboxCursor(ctx, ep, "alice", "phone", 1)
	expandedRealtimeCheck(t, err)
	if cursor != 1 {
		t.Fatalf("explicit cursor reset: %d", cursor)
	}
	progress, err := s.GetManagedRealtimeReadProgress(ctx, ep, "alice", "", true)
	expandedRealtimeCheck(t, err)
	if progress.Unread != 2 || progress.LatestSequence != 2 {
		t.Fatalf("unread: %+v", progress)
	}
	progress, err = s.AdvanceManagedRealtimeReadProgress(ctx, ep, "alice", "", true, 1)
	expandedRealtimeCheck(t, err)
	if progress.Sequence != 1 || progress.Unread != 1 {
		t.Fatalf("read marker: %+v", progress)
	}
	updated, err := s.MutateManagedRealtimeMessage(ctx, ep, "alice", true, "second", 1, []byte("edited"), false, false)
	expandedRealtimeCheck(t, err)
	if updated.Version != 2 || updated.Sequence != 3 || string(updated.Data) != "edited" {
		t.Fatalf("edit: %+v", updated)
	}
	if _, err = s.MutateManagedRealtimeMessage(ctx, ep, "alice", true, "second", 1, []byte("stale"), false, false); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale edit: %v", err)
	}
	removed, err := s.MutateManagedRealtimeMessage(ctx, ep, "alice", true, "second", 2, nil, false, true)
	expandedRealtimeCheck(t, err)
	if !removed.Deleted || removed.Version != 3 || removed.Sequence != 4 {
		t.Fatalf("delete: %+v", removed)
	}
	progress, err = s.GetManagedRealtimeReadProgress(ctx, ep, "alice", "", true)
	expandedRealtimeCheck(t, err)
	if progress.Unread != 0 {
		t.Fatalf("deleted notification counted unread: %+v", progress)
	}
	if _, err = s.ResetManagedRealtimeInboxCursor(ctx, ep, "alice", "phone", 100); !errors.Is(err, state.ErrManagedRealtimeHistoryInvalid) {
		t.Fatalf("reset beyond head: %v", err)
	}
	if _, err = s.AdvanceManagedRealtimeReadProgress(ctx, ep, "alice", "", true, 100); !errors.Is(err, state.ErrManagedRealtimeHistoryInvalid) {
		t.Fatalf("read marker beyond head: %v", err)
	}
}

func expandedRealtimeDirectReceipts(t *testing.T, s expandedRealtimeStore, ctx context.Context, ep string) {
	nodeA, nodeB := uuid.NewString(), uuid.NewString()
	fingerprint := sha256.Sum256([]byte("payload"))
	dispatch, inFlight, err := s.BeginManagedRealtimeDirectMessageReceipt(ctx, ep, "message", fingerprint[:])
	expandedRealtimeCheck(t, err)
	if !dispatch || inFlight {
		t.Fatalf("initial dispatch: %v/%v", dispatch, inFlight)
	}
	dispatch, inFlight, err = s.BeginManagedRealtimeDirectMessageReceipt(ctx, ep, "message", fingerprint[:])
	expandedRealtimeCheck(t, err)
	if dispatch || !inFlight {
		t.Fatalf("duplicate lease: %v/%v", dispatch, inFlight)
	}
	other := sha256.Sum256([]byte("other"))
	if _, _, err = s.BeginManagedRealtimeDirectMessageReceipt(ctx, ep, "message", other[:]); !errors.Is(err, state.ErrManagedRealtimeDirectMessageConflict) {
		t.Fatalf("fingerprint fence: %v", err)
	}
	targets := []state.ManagedRealtimeDirectMessageTarget{{ConnectionID: "ack", AckSupported: true}, {ConnectionID: "legacy"}}
	created, err := s.RegisterManagedRealtimeDirectMessageTargets(ctx, ep, "message", nodeA, targets)
	expandedRealtimeCheck(t, err)
	if !reflect.DeepEqual(created, []string{"ack", "legacy"}) {
		t.Fatalf("created targets: %v", created)
	}
	created, err = s.RegisterManagedRealtimeDirectMessageTargets(ctx, ep, "message", nodeA, targets)
	expandedRealtimeCheck(t, err)
	if len(created) != 0 {
		t.Fatalf("duplicate targets: %v", created)
	}
	if err = s.AcknowledgeManagedRealtimeDirectMessage(ctx, ep, "message", nodeB, "ack"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-node ack: %v", err)
	}
	expandedRealtimeCheck(t, s.UpdateManagedRealtimeDirectMessageDeliveries(ctx, ep, "message", nodeA, []state.ManagedRealtimeDirectMessageDeliveryResult{{ConnectionID: "ack", QueueStatus: state.ManagedRealtimeDirectQueueQueued}}))
	expandedRealtimeCheck(t, s.AcknowledgeManagedRealtimeDirectMessage(ctx, ep, "message", nodeA, "ack"))
	expandedRealtimeCheck(t, s.AcknowledgeManagedRealtimeDirectMessage(ctx, ep, "message", nodeA, "ack"))
	if err = s.AcknowledgeManagedRealtimeDirectMessage(ctx, ep, "message", nodeA, "legacy"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unsupported ack: %v", err)
	}
	summary := state.ManagedRealtimeDirectMessageSummary{Recipients: 2, Queued: 1, Unsupported: 1}
	expandedRealtimeCheck(t, s.CompleteManagedRealtimeDirectMessageReceipt(ctx, ep, "message", summary))
	receipt, err := s.GetManagedRealtimeDirectMessageReceipt(ctx, ep, "message")
	expandedRealtimeCheck(t, err)
	if !receipt.DispatchComplete || receipt.Summary != summary || len(receipt.Deliveries) != 2 || receipt.Deliveries[0].Status != "acknowledged" || receipt.Deliveries[1].Status != "unsupported" {
		t.Fatalf("receipt: %+v", receipt)
	}
	dispatch, inFlight, err = s.BeginManagedRealtimeDirectMessageReceipt(ctx, ep, "message", fingerprint[:])
	expandedRealtimeCheck(t, err)
	if dispatch || inFlight {
		t.Fatal("completed dispatch reacquired")
	}
}

func expandedRealtimeReducer(t *testing.T, s expandedRealtimeStore, ctx context.Context, ep string) {
	initial := state.ManagedRealtimeReducerState{EndpointID: ep, Channel: "cart", Entities: json.RawMessage(`{}`)}
	_, err := s.PutManagedRealtimeReducer(ctx, initial)
	expandedRealtimeCheck(t, err)
	_, err = s.PutManagedRealtimeReducer(ctx, initial)
	expandedRealtimeCheck(t, err)
	if _, err = s.PutManagedRealtimeChannelSnapshot(ctx, state.ManagedRealtimeChannelSnapshot{EndpointID: ep, Channel: "cart", Data: []byte("override")}); !errors.Is(err, state.ErrManagedRealtimeReducerActive) {
		t.Fatalf("reducer ownership: %v", err)
	}
	items := []state.ManagedRealtimeBatchItem{
		{Data: []byte(`{"op":"set","key":"item","value":{"count":1,"tags":["a"]},"expected_version":0}`)},
		{Data: []byte(`{"op":"increment","key":"item","field":"count","delta":2,"min":0,"max":10,"expected_version":1}`)},
		{Data: []byte(`{"op":"append","key":"item","field":"tags","items":["a","b"],"unique":true,"max_length":3}`)},
		{Data: []byte(`{"op":"remove","key":"item","field":"tags","items":["a"]}`)},
		{Data: []byte(`{"op":"merge","key":"item","value":{"name":"book"}}`)},
	}
	zero := int64(0)
	messages, err := s.AppendManagedRealtimeBatchConditional(ctx, ep, "cart", "initial", items, &zero)
	expandedRealtimeCheck(t, err)
	if len(messages) != 5 || messages[4].Sequence != 5 {
		t.Fatalf("batch: %+v", messages)
	}
	row, err := s.GetManagedRealtimeReducer(ctx, ep, "cart")
	expandedRealtimeCheck(t, err)
	var entities map[string]map[string]any
	expandedRealtimeCheck(t, json.Unmarshal(row.Entities, &entities))
	if row.Sequence != 5 || row.EntityVersions["item"] != 5 || entities["item"]["count"] != float64(3) || !reflect.DeepEqual(entities["item"]["tags"], []any{"b"}) || entities["item"]["name"] != "book" {
		t.Fatalf("reduced state: %+v %s", row, row.Entities)
	}
	before := append([]byte(nil), row.Entities...)
	bad := []state.ManagedRealtimeBatchItem{{Data: []byte(`{"op":"merge","key":"item","value":{"name":"changed"}}`)}, {Data: []byte(`{"op":"delete","key":"item","expected_version":0}`)}}
	_, err = s.AppendManagedRealtimeBatchConditional(ctx, ep, "cart", "invalid", bad, nil)
	var conflict *state.ManagedRealtimeEntityVersionConflict
	if !errors.As(err, &conflict) || conflict.Item != 1 {
		t.Fatalf("entity fence: %v", err)
	}
	row, err = s.GetManagedRealtimeReducer(ctx, ep, "cart")
	expandedRealtimeCheck(t, err)
	if row.Sequence != 5 || string(row.Entities) != string(before) || row.EntityVersions["item"] != 5 {
		t.Fatalf("failed batch changed reducer: %+v", row)
	}
	page, err := s.ReadManagedRealtimeChannelHistory(ctx, ep, "cart", 0, 100)
	expandedRealtimeCheck(t, err)
	if page.LatestSequence != 5 || len(page.Messages) != 5 {
		t.Fatalf("failed batch changed head: %+v", page)
	}
	_, err = s.AppendManagedRealtimeBatchConditional(ctx, ep, "cart", "stale", items[:1], &zero)
	var headConflict *state.ManagedRealtimeSequenceConflict
	if !errors.As(err, &headConflict) || headConflict.Expected != 0 || headConflict.Current != 5 {
		t.Fatalf("stale head conflict: %v", err)
	}
	_, err = s.AppendManagedRealtimeChannelMessage(ctx, ep, "cart", []byte(`{"op":"delete","key":"item","expected_version":5}`), false, "delete")
	expandedRealtimeCheck(t, err)
	row, err = s.GetManagedRealtimeReducer(ctx, ep, "cart")
	expandedRealtimeCheck(t, err)
	if string(row.Entities) != "{}" || row.EntityVersions["item"] != 6 {
		t.Fatalf("deletion lost tombstone version: %+v", row)
	}
	expandedRealtimeCheck(t, s.DeleteManagedRealtimeReducer(ctx, ep, "cart"))
	if _, err = s.GetManagedRealtimeReducer(ctx, ep, "cart"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted reducer: %v", err)
	}
	if _, err = s.GetManagedRealtimeChannelSnapshot(ctx, ep, "cart"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("orphan derived snapshot: %v", err)
	}
}

func expandedRealtimeSchema(t *testing.T, s expandedRealtimeStore, ctx context.Context, ep string) {
	schema := state.ManagedRealtimeEventSchema{EndpointID: ep, Channel: "orders", EventType: "created", Version: 1, Schema: json.RawMessage(`{"type":"object","required":["id"],"properties":{"id":{"type":"integer"}},"additionalProperties":false}`)}
	_, err := s.PutManagedRealtimeEventSchema(ctx, schema)
	expandedRealtimeCheck(t, err)
	_, err = s.PutManagedRealtimeEventSchema(ctx, schema)
	expandedRealtimeCheck(t, err)
	changed := schema
	changed.Schema = json.RawMessage(`{"type":"string"}`)
	if _, err = s.PutManagedRealtimeEventSchema(ctx, changed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("schema version overwritten: %v", err)
	}
	got, err := s.GetManagedRealtimeEventSchema(ctx, ep, "orders", "created", 1)
	expandedRealtimeCheck(t, err)
	if !json.Valid(got.Schema) || got.Version != 1 {
		t.Fatalf("schema: %+v", got)
	}
	metadata := map[string]string{"event_type": "created", "schema_version": "1"}
	items := []state.ManagedRealtimeBatchItem{{Data: []byte(`{"id":1}`), Metadata: metadata}, {Data: []byte(`{"id":"invalid"}`), Metadata: metadata}}
	_, err = s.AppendManagedRealtimeBatchConditional(ctx, ep, "orders", "typed", items, nil)
	var invalid *state.ManagedRealtimeEventSchemaError
	if !errors.As(err, &invalid) || invalid.Item != 1 || invalid.Path != "/id" {
		t.Fatalf("schema validation detail: %v", err)
	}
	page, err := s.ReadManagedRealtimeChannelHistory(ctx, ep, "orders", 0, 100)
	expandedRealtimeCheck(t, err)
	if page.LatestSequence != 0 || len(page.Messages) != 0 {
		t.Fatalf("invalid typed batch partially committed: %+v", page)
	}
	items[1].Data = []byte(`{"id":2}`)
	rows, err := s.AppendManagedRealtimeBatchConditional(ctx, ep, "orders", "typed", items, nil)
	expandedRealtimeCheck(t, err)
	if len(rows) != 2 || rows[0].Sequence != 1 || rows[1].Sequence != 2 {
		t.Fatalf("corrected typed batch: %+v", rows)
	}
	replayed, err := s.AppendManagedRealtimeBatchConditional(ctx, ep, "orders", "typed", items, nil)
	expandedRealtimeCheck(t, err)
	if len(replayed) != 2 || replayed[1].Sequence != 2 {
		t.Fatalf("batch retry appended: %+v", replayed)
	}
	for _, bad := range []state.ManagedRealtimeBatchItem{{Data: []byte(`{"id":3}`)}, {Data: []byte(`{"id":3}`), Binary: true, Metadata: metadata}} {
		if _, err = s.AppendManagedRealtimeBatchConditional(ctx, ep, "orders", uuid.NewString(), []state.ManagedRealtimeBatchItem{bad}, nil); err == nil {
			t.Fatal("schema-bound channel accepted untyped or binary data")
		}
	}
}

func expandedRealtimeSchedules(t *testing.T, s expandedRealtimeStore, ctx context.Context, ep string) {
	deliverAt := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	versions := map[string]int64{}
	for _, id := range []string{"a", "b"} {
		row := state.ManagedRealtimeSchedule{EndpointID: ep, Channel: "jobs", ID: id, Group: "daily", Data: []byte("payload"), DeliverAt: deliverAt, Metadata: map[string]string{"kind": "job"}}
		created, err := s.PutManagedRealtimeSchedule(ctx, row)
		expandedRealtimeCheck(t, err)
		versions[id] = created.Version
		retry, err := s.PutManagedRealtimeSchedule(ctx, row)
		expandedRealtimeCheck(t, err)
		if retry.Version != created.Version || retry.MaxAttempts != 1 || retry.BackoffSeconds != 5 {
			t.Fatalf("schedule creation retry: %+v", retry)
		}
	}
	stale := map[string]int64{"a": versions["a"], "b": versions["b"] + 1}
	if _, err := s.ApplyManagedRealtimeScheduleGroup(ctx, ep, "jobs", "daily", "pause", stale); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("group version fence: %v", err)
	}
	rows, err := s.ListManagedRealtimeSchedules(ctx, ep, "jobs")
	expandedRealtimeCheck(t, err)
	if len(rows) != 2 || rows[0].Status != "pending" || rows[1].Status != "pending" {
		t.Fatalf("partial group pause: %+v", rows)
	}
	for _, action := range []string{"pause", "resume", "cancel"} {
		rows, err = s.ApplyManagedRealtimeScheduleGroup(ctx, ep, "jobs", "daily", action, versions)
		expandedRealtimeCheck(t, err)
		if len(rows) != 2 {
			t.Fatalf("group %s: %+v", action, rows)
		}
		want := map[string]string{"pause": "paused", "resume": "pending", "cancel": "canceled"}[action]
		for _, row := range rows {
			if row.Status != want || row.Version != versions[row.ID]+1 {
				t.Fatalf("group %s result: %+v", action, row)
			}
			versions[row.ID] = row.Version
		}
	}
	page, err := s.ReadManagedRealtimeScheduleHistory(ctx, ep, "jobs", "a", 0, 2)
	expandedRealtimeCheck(t, err)
	if len(page.Events) != 2 || !page.HasMore || page.Events[0].Event != "created" || page.Events[1].Event != "paused" {
		t.Fatalf("history page: %+v", page)
	}
	next, err := s.ReadManagedRealtimeScheduleHistory(ctx, ep, "jobs", "a", page.Events[1].Version, 100)
	expandedRealtimeCheck(t, err)
	if len(next.Events) != 2 || next.HasMore || next.Events[0].Event != "resumed" || next.Events[1].Event != "canceled" {
		t.Fatalf("history continuation: %+v", next)
	}
	if _, err = s.UpdateManagedRealtimeSchedule(ctx, ep, "jobs", "a", versions["a"], &deliverAt); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("terminal schedule revived: %v", err)
	}
}

func expandedRealtimePush(t *testing.T, s expandedRealtimeStore, ctx context.Context, ep string) {
	provider := state.ManagedRealtimePushProvider{EndpointID: ep, Provider: "fcm", Enabled: true, Sealed: []byte("provider-secret")}
	expandedRealtimeCheck(t, s.PutManagedRealtimePushProvider(ctx, provider))
	device := state.ManagedRealtimePushDevice{EndpointID: ep, Principal: "alice", Device: "phone", Provider: "fcm", Fingerprint: strings.Repeat("a", 64), Sealed: []byte("device-secret")}
	expandedRealtimeCheck(t, s.PutManagedRealtimePushDevice(ctx, device))
	devices, err := s.ListManagedRealtimePushDevices(ctx, ep, "alice")
	expandedRealtimeCheck(t, err)
	if len(devices) != 1 || len(devices[0].Sealed) != 0 || devices[0].Version < 1 {
		t.Fatalf("device projection: %+v", devices)
	}
	version := devices[0].Version
	expandedRealtimeCheck(t, s.PutManagedRealtimePushDevice(ctx, device))
	devices, err = s.ListManagedRealtimePushDevices(ctx, ep, "alice")
	expandedRealtimeCheck(t, err)
	if devices[0].Version != version {
		t.Fatal("idempotent device registration rotated version")
	}
	preferences, err := s.GetManagedRealtimeNotificationPreferences(ctx, ep, "alice")
	expandedRealtimeCheck(t, err)
	if !preferences.Enabled || preferences.Devices != nil {
		t.Fatalf("default preferences: %+v", preferences)
	}
	preferences.Categories["marketing"] = false
	expandedRealtimeCheck(t, s.PutManagedRealtimeNotificationPreferences(ctx, ep, "alice", preferences))
	preferences.Categories["marketing"] = true
	stored, err := s.GetManagedRealtimeNotificationPreferences(ctx, ep, "alice")
	expandedRealtimeCheck(t, err)
	if stored.Categories["marketing"] {
		t.Fatal("preference document aliases input")
	}
	_, err = s.AppendManagedRealtimeNotification(ctx, ep, "alice", []byte("notify"), false, "notify", 1)
	expandedRealtimeCheck(t, err)
	// Wait for the retained fallback deadline, without a host-speed assertion.
	wait, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		count, err := s.DrainManagedRealtimeFallbacks(wait, 100)
		expandedRealtimeCheck(t, err)
		if count == 1 {
			break
		}
		select {
		case <-wait.Done():
			t.Fatal("fallback deadline did not produce a delivery")
		case <-ticker.C:
		}
	}
	claims, err := s.ClaimManagedRealtimePush(ctx, 100)
	expandedRealtimeCheck(t, err)
	if len(claims) != 1 || claims[0].MessageID != "notify" || claims[0].Lease == "" || string(claims[0].Config) != "provider-secret" || string(claims[0].Target) != "device-secret" {
		t.Fatalf("push claim: %+v", claims)
	}
	claim := claims[0]
	active, err := s.ManagedRealtimePushLeaseActive(ctx, claim.ID, claim.Lease)
	expandedRealtimeCheck(t, err)
	if !active {
		t.Fatal("new lease inactive")
	}
	ready, err := s.PrepareManagedRealtimePush(ctx, claim.ID, claim.Lease)
	expandedRealtimeCheck(t, err)
	if !ready {
		t.Fatal("default preferences blocked delivery")
	}
	device.Fingerprint = strings.Repeat("b", 64)
	device.Sealed = []byte("rotated-secret")
	expandedRealtimeCheck(t, s.PutManagedRealtimePushDevice(ctx, device))
	active, err = s.ManagedRealtimePushLeaseActive(ctx, claim.ID, claim.Lease)
	expandedRealtimeCheck(t, err)
	if active {
		t.Fatal("rotation left old delivery authority active")
	}
	expandedRealtimeCheck(t, s.CompleteManagedRealtimePush(ctx, claim.ID, claim.Lease, 200, "sent", false, false))
	deliveries, err := s.ListManagedRealtimePushDeliveries(ctx, ep, "alice")
	expandedRealtimeCheck(t, err)
	if len(deliveries) != 1 || deliveries[0].Status != "cancelled" || deliveries[0].Code != "device_rotated" {
		t.Fatalf("stale completion overwrote cancellation: %+v", deliveries)
	}
	bob, err := s.ListManagedRealtimePushDeliveries(ctx, ep, "bob")
	expandedRealtimeCheck(t, err)
	if len(bob) != 0 {
		t.Fatal("push delivery leaked to another principal")
	}
	timeline, err := s.ListManagedRealtimeNotificationTimeline(ctx, ep, "alice", "notify", 0)
	expandedRealtimeCheck(t, err)
	if len(timeline) == 0 {
		t.Fatal("notification audit timeline is empty")
	}
	expandedRealtimeCheck(t, s.DeleteManagedRealtimePushDevice(ctx, ep, "alice", "phone"))
	devices, err = s.ListManagedRealtimePushDevices(ctx, ep, "alice")
	expandedRealtimeCheck(t, err)
	if len(devices) != 0 {
		t.Fatal("device survived deletion")
	}
	provider.Enabled = false
	expandedRealtimeCheck(t, s.PutManagedRealtimePushProvider(ctx, provider))
	if err = s.PutManagedRealtimePushDevice(ctx, device); !errors.Is(err, state.ErrManagedRealtimeFallbackSubscription) {
		t.Fatalf("disabled provider accepted device: %v", err)
	}
}

func expandedRealtimePresence(t *testing.T, s expandedRealtimeStore, ctx context.Context, ep string) {
	node, err := s.CreateComputeNode(ctx, state.ComputeNode{
		Name: "realtime-" + uuid.NewString(), TargetURL: "unix:///run/vmmd.sock",
		VPCPUs: 1, MemMB: 1024, MaxConcurrency: 1, AdmissionCeilingMB: 512,
		VCPUBudget: 1, Active: true,
	})
	expandedRealtimeCheck(t, err)
	for _, connection := range []string{"phone", "laptop"} {
		_, err := s.UpsertManagedRealtimePresenceLease(ctx, state.ManagedRealtimePresenceLease{EndpointID: ep, Channel: "room", NodeID: node.ID, ConnectionID: connection, MemberID: "member", Principal: "alice", State: []byte(`{"online":true}`), ExpiresAt: time.Now().UTC().Add(state.ManagedRealtimePresenceLeaseTTL)})
		expandedRealtimeCheck(t, err)
	}
	rows, err := s.ReadManagedRealtimePresenceSnapshot(ctx, ep, "room")
	expandedRealtimeCheck(t, err)
	if len(rows) != 1 || rows[0].ConnectionCount != 2 || rows[0].MemberID != "member" {
		t.Fatalf("principal aggregation: %+v", rows)
	}
	var presence struct{ Online bool }
	expandedRealtimeCheck(t, json.Unmarshal(rows[0].State, &presence))
	if !presence.Online {
		t.Fatalf("presence state: %s", rows[0].State)
	}
	_, _, err = s.DeleteManagedRealtimePresenceLease(ctx, ep, "room", node.ID, "phone")
	expandedRealtimeCheck(t, err)
	rows, err = s.ReadManagedRealtimePresenceSnapshot(ctx, ep, "room")
	expandedRealtimeCheck(t, err)
	if len(rows) != 1 || rows[0].ConnectionCount != 1 {
		t.Fatalf("partial disconnect: %+v", rows)
	}
	_, _, err = s.DeleteManagedRealtimePresenceLease(ctx, ep, "room", node.ID, "laptop")
	expandedRealtimeCheck(t, err)
	rows, err = s.ReadManagedRealtimePresenceSnapshot(ctx, ep, "room")
	expandedRealtimeCheck(t, err)
	if len(rows) != 0 {
		t.Fatalf("last disconnect left presence: %+v", rows)
	}
}
