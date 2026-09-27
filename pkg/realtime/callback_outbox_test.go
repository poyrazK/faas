package realtime

// adr: 281
// adr: 293
// adr: 294
// adr: 295
// adr: 296

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testCallbackEvent() Event {
	return Event{
		ID:                "evt_outbox_1",
		Type:              EventMessage,
		EndpointID:        "endpoint-1",
		AppID:             "app-1",
		AccountID:         "account-1",
		ConnectionID:      "connection-1",
		Sequence:          7,
		Data:              []byte("hello"),
		At:                time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC),
		CallbackURL:       "https://example.com/callback",
		CallbackPath:      "/realtime/message",
		CallbackAuthToken: "callback-secret",
	}
}

func newTestCallbackOutbox(t *testing.T, cfg CallbackOutboxConfig) *CallbackOutbox {
	t.Helper()
	if cfg.Root == "" {
		cfg.Root = t.TempDir()
	}
	if cfg.MaxBytes == 0 {
		cfg.MaxBytes = 1 << 20
	}
	if cfg.RetryInterval == 0 {
		cfg.RetryInterval = time.Millisecond
	}
	queue, err := NewCallbackOutbox(cfg)
	if err != nil {
		t.Fatalf("NewCallbackOutbox: %v", err)
	}
	return queue
}

func TestCallbackOutboxPersistsPendingEventAndPrivateFields(t *testing.T) {
	root := t.TempDir()
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{Root: root})
	event := testCallbackEvent()
	claimed, err := queue.EnqueueAndClaim(event)
	if err != nil || !claimed {
		t.Fatalf("EnqueueAndClaim = (%v, %v), want claimed", claimed, err)
	}
	if err := queue.Fail(event.ID); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	restarted := newTestCallbackOutbox(t, CallbackOutboxConfig{Root: root})
	time.Sleep(3 * time.Millisecond)
	replayed, ok, err := restarted.ClaimNext()
	if err != nil || !ok {
		t.Fatalf("ClaimNext after restart = (%+v, %v, %v), want event", replayed, ok, err)
	}
	if replayed.ID != event.ID || replayed.Type != EventMessage || string(replayed.Data) != "hello" ||
		replayed.CallbackURL != event.CallbackURL || replayed.CallbackPath != event.CallbackPath ||
		replayed.CallbackAuthToken != event.CallbackAuthToken {
		t.Fatalf("replayed event = %+v, want private callback fields preserved", replayed)
	}
}

func TestCallbackOutboxKeepsQueuedTokenAndUsesUpdatedTokenForNewEvents(t *testing.T) {
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{})
	queued := testCallbackEvent()
	if claimed, err := queue.EnqueueAndClaim(queued); err != nil || !claimed {
		t.Fatalf("EnqueueAndClaim queued event = (%v, %v)", claimed, err)
	}
	queue.Release(queued.ID)
	if err := queue.updateCallbackAuthToken(queued.EndpointID, "callback-secret-next"); err != nil {
		t.Fatalf("update callback token: %v", err)
	}
	restarted := newTestCallbackOutbox(t, CallbackOutboxConfig{Root: queue.root})
	replayed, ok, err := restarted.ClaimNext()
	if err != nil || !ok || replayed.CallbackAuthToken != "callback-secret" {
		t.Fatalf("ClaimNext queued event = (%+v, %v, %v), want original token", replayed, ok, err)
	}
	if err := restarted.Ack(queued.ID); err != nil {
		t.Fatalf("Ack queued event: %v", err)
	}
	if err := restarted.updateCallbackAuthToken(queued.EndpointID, "callback-secret-next"); err != nil {
		t.Fatalf("restore callback token after restart: %v", err)
	}

	future := testCallbackEvent()
	future.ID = "evt_after_rotation"
	if claimed, err := restarted.EnqueueAndClaim(future); err != nil || !claimed {
		t.Fatalf("EnqueueAndClaim future event = (%v, %v)", claimed, err)
	}
	delivering, err := restarted.claimedEvent(future.ID)
	if err != nil || delivering.CallbackAuthToken != "callback-secret-next" {
		t.Fatalf("claimed future event = (%+v, %v), want updated token", delivering, err)
	}
}

func TestManagerRegistrationUpdatesOutboxCredentialForFutureEvents(t *testing.T) {
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{})
	manager := NewManager(Config{}, HTTPHooks{DurableQueue: queue})
	defer func() { _ = manager.Close() }()
	if err := manager.RegisterEndpoint(Endpoint{ID: "endpoint-1", CallbackAuthToken: "callback-secret-next"}); err != nil {
		t.Fatalf("RegisterEndpoint: %v", err)
	}
	event := testCallbackEvent()
	if claimed, err := queue.EnqueueAndClaim(event); err != nil || !claimed {
		t.Fatalf("EnqueueAndClaim = (%v, %v)", claimed, err)
	}
	delivering, err := queue.claimedEvent(event.ID)
	if err != nil || delivering.CallbackAuthToken != "callback-secret-next" {
		t.Fatalf("claimed event = (%+v, %v), want updated token", delivering, err)
	}
	manager.RemoveEndpoint(event.EndpointID)
	queue.mu.Lock()
	_, retained := queue.callbackAuthTokens[event.EndpointID]
	queue.mu.Unlock()
	if retained {
		t.Fatal("removed endpoint callback token remained in outbox memory")
	}
}

func TestCallbackOutboxPreservesConnectionSequence(t *testing.T) {
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{})
	first := testCallbackEvent()
	first.ID, first.Sequence = "evt_z", 1
	second := testCallbackEvent()
	second.ID, second.Sequence = "evt_a", 2
	disconnect := testCallbackEvent()
	disconnect.ID, disconnect.Type, disconnect.Sequence = "evt_0", EventDisconnect, 2
	if claimed, err := queue.EnqueueAndClaim(first); err != nil || !claimed {
		t.Fatalf("first event claim = (%v, %v)", claimed, err)
	}
	queue.Release(first.ID)
	for _, event := range []Event{second, disconnect} {
		if claimed, err := queue.EnqueueAndClaim(event); err != nil || claimed {
			t.Fatalf("later event %s claim = (%v, %v), want persisted but blocked", event.ID, claimed, err)
		}
	}
	for _, want := range []string{first.ID, second.ID, disconnect.ID} {
		event, ok, err := queue.ClaimNext()
		if err != nil || !ok || event.ID != want {
			t.Fatalf("ClaimNext = (%s, %v, %v), want %s", event.ID, ok, err, want)
		}
		if err := queue.Ack(event.ID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCallbackOutboxSchedulerPromotesNextConnectionHead(t *testing.T) {
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{
		RetryInterval: time.Millisecond, MaxRetryInterval: time.Second, MaxAttempts: 4,
	})
	first := testCallbackEvent()
	first.ID, first.Sequence = "evt_sched_first", 1
	first.At = time.Now().Add(-time.Second)
	second := first
	second.ID, second.Sequence = "evt_sched_second", 2
	second.At = first.At.Add(time.Nanosecond)
	independent := first
	independent.ID = "evt_sched_independent"
	independent.ConnectionID = "connection-2"
	independent.At = first.At.Add(time.Second)

	if claimed, err := queue.EnqueueAndClaim(first); err != nil || !claimed {
		t.Fatalf("enqueue first = (%v, %v), want claimed", claimed, err)
	}
	queue.Release(first.ID)
	if claimed, err := queue.EnqueueAndClaim(second); err != nil || claimed {
		t.Fatalf("enqueue second = (%v, %v), want queued behind first", claimed, err)
	}
	if claimed, err := queue.EnqueueAndClaim(independent); err != nil || !claimed {
		t.Fatalf("enqueue independent = (%v, %v), want claimed", claimed, err)
	}
	queue.Release(independent.ID)

	got, ok, err := queue.ClaimNext()
	if err != nil || !ok || got.ID != first.ID {
		t.Fatalf("ClaimNext before delay = (%s, %v, %v), want first event", got.ID, ok, err)
	}
	if err := queue.FailWithRetryAfter(first.ID, 35*time.Millisecond); err != nil {
		t.Fatalf("FailWithRetryAfter first: %v", err)
	}
	if stats := queue.Stats(); stats.ReplayReady != 1 || stats.ReplayDelayed != 1 {
		t.Fatalf("stats during backoff = %+v, want one ready and one delayed connection head", stats)
	}
	got, ok, err = queue.ClaimNext()
	if err != nil || !ok || got.ID != independent.ID {
		t.Fatalf("ClaimNext while first is delayed = (%s, %v, %v), want independent event", got.ID, ok, err)
	}
	if err := queue.Ack(got.ID); err != nil {
		t.Fatalf("Ack independent: %v", err)
	}
	if stats := queue.Stats(); stats.ReplayReady != 0 || stats.ReplayDelayed != 1 {
		t.Fatalf("stats with only delayed work = %+v, want no ready heads and one delayed head", stats)
	}
	if _, ok, err := queue.ClaimNext(); err != nil || ok {
		t.Fatalf("ClaimNext before retry time = (%v, %v), want no event", ok, err)
	}
	time.Sleep(40 * time.Millisecond)
	if stats := queue.Stats(); stats.ReplayReady != 1 || stats.ReplayDelayed != 0 {
		t.Fatalf("stats after retry time = %+v, want the due head promoted to ready", stats)
	}
	got, ok, err = queue.ClaimNext()
	if err != nil || !ok || got.ID != first.ID {
		t.Fatalf("ClaimNext after retry time = (%s, %v, %v), want first event", got.ID, ok, err)
	}
	if err := queue.Ack(got.ID); err != nil {
		t.Fatalf("Ack first: %v", err)
	}
	got, ok, err = queue.ClaimNext()
	if err != nil || !ok || got.ID != second.ID {
		t.Fatalf("ClaimNext after first ack = (%s, %v, %v), want second event", got.ID, ok, err)
	}
	if err := queue.Ack(got.ID); err != nil {
		t.Fatalf("Ack second: %v", err)
	}
}

func TestCallbackOutboxRestartRebuildsPerConnectionOrder(t *testing.T) {
	root := t.TempDir()
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{Root: root})
	first := testCallbackEvent()
	first.ID, first.Sequence = "evt_z_first", 1
	second := first
	second.ID, second.Sequence = "evt_a_second", 2
	for _, event := range []Event{first, second} {
		claimed, err := queue.EnqueueAndClaim(event)
		if err != nil {
			t.Fatalf("EnqueueAndClaim(%s): %v", event.ID, err)
		}
		if claimed {
			queue.Release(event.ID)
		}
	}

	restarted := newTestCallbackOutbox(t, CallbackOutboxConfig{Root: root})
	for _, want := range []string{first.ID, second.ID} {
		got, ok, err := restarted.ClaimNext()
		if err != nil || !ok || got.ID != want {
			t.Fatalf("ClaimNext after restart = (%s, %v, %v), want %s", got.ID, ok, err, want)
		}
		if err := restarted.Ack(got.ID); err != nil {
			t.Fatalf("Ack(%s): %v", got.ID, err)
		}
	}
}

func TestCallbackOutboxAckReleasesEarlierArrivingConnectionHead(t *testing.T) {
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{})
	later := testCallbackEvent()
	later.ID, later.Sequence = "evt_late_inflight", 2
	earlier := later
	earlier.ID, earlier.Sequence = "evt_earlier_queued", 1
	if claimed, err := queue.EnqueueAndClaim(later); err != nil || !claimed {
		t.Fatalf("enqueue later event = (%v, %v), want claimed", claimed, err)
	}
	if claimed, err := queue.EnqueueAndClaim(earlier); err != nil || claimed {
		t.Fatalf("enqueue earlier event = (%v, %v), want blocked by active delivery", claimed, err)
	}
	if err := queue.Ack(later.ID); err != nil {
		t.Fatalf("Ack later event: %v", err)
	}
	got, ok, err := queue.ClaimNext()
	if err != nil || !ok || got.ID != earlier.ID {
		t.Fatalf("ClaimNext after active delivery = (%s, %v, %v), want earlier event", got.ID, ok, err)
	}
}

func TestCallbackOutboxDeadLetterPromotesNextConnectionHead(t *testing.T) {
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{MaxAttempts: 1})
	first := testCallbackEvent()
	first.ID, first.Sequence = "evt_deadletter_head", 1
	second := first
	second.ID, second.Sequence = "evt_after_deadletter", 2
	if claimed, err := queue.EnqueueAndClaim(first); err != nil || !claimed {
		t.Fatalf("enqueue first event = (%v, %v), want claimed", claimed, err)
	}
	queue.Release(first.ID)
	if claimed, err := queue.EnqueueAndClaim(second); err != nil || claimed {
		t.Fatalf("enqueue second event = (%v, %v), want queued behind first", claimed, err)
	}
	if _, ok, err := queue.ClaimNext(); err != nil || !ok {
		t.Fatalf("ClaimNext first event = (%v, %v), want event", ok, err)
	}
	if err := queue.Fail(first.ID); err != nil {
		t.Fatalf("Fail first event: %v", err)
	}
	got, ok, err := queue.ClaimNext()
	if err != nil || !ok || got.ID != second.ID {
		t.Fatalf("ClaimNext after dead-letter = (%s, %v, %v), want second event", got.ID, ok, err)
	}
}

func TestCallbackOutboxAckRemovesPendingEvent(t *testing.T) {
	root := t.TempDir()
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{Root: root})
	event := testCallbackEvent()
	claimed, err := queue.EnqueueAndClaim(event)
	if err != nil || !claimed {
		t.Fatalf("EnqueueAndClaim = (%v, %v), want claimed", claimed, err)
	}
	if err := queue.Ack(event.ID); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if stats := queue.Stats(); stats.Pending != 0 || stats.PendingBytes != 0 {
		t.Fatalf("Stats after ack = %+v, want empty", stats)
	}
	if _, err := os.Stat(filepath.Join(root, event.ID+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending file stat = %v, want not found", err)
	}
}

func TestCallbackOutboxDeadLettersAfterBoundedFailures(t *testing.T) {
	root := t.TempDir()
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{Root: root, MaxAttempts: 2})
	event := testCallbackEvent()
	for attempt := 1; attempt <= 2; attempt++ {
		if attempt > 1 {
			time.Sleep(3 * time.Millisecond)
		}
		claimed, err := queue.EnqueueAndClaim(event)
		if err != nil || !claimed {
			t.Fatalf("attempt %d EnqueueAndClaim = (%v, %v), want claimed", attempt, claimed, err)
		}
		if err := queue.Fail(event.ID); err != nil {
			t.Fatalf("attempt %d Fail: %v", attempt, err)
		}
	}
	stats := queue.Stats()
	if stats.Pending != 0 || stats.DeadLetterTotal != 1 {
		t.Fatalf("Stats after dead letter = %+v, want no pending and one dead letter", stats)
	}
	if _, err := os.Stat(filepath.Join(root, "dead", event.ID+".json")); err != nil {
		t.Fatalf("dead letter stat: %v", err)
	}
}

func TestCallbackOutboxRetryDelayUsesCappedExponentialBackoff(t *testing.T) {
	base := time.Second
	maximum := 5 * time.Second
	cases := []struct {
		attempt int
		minimum time.Duration
		maximum time.Duration
	}{
		{attempt: 1, minimum: 500 * time.Millisecond, maximum: time.Second},
		{attempt: 2, minimum: time.Second, maximum: 2 * time.Second},
		{attempt: 3, minimum: 2 * time.Second, maximum: 4 * time.Second},
		{attempt: 4, minimum: 2500 * time.Millisecond, maximum: 5 * time.Second},
		{attempt: 10, minimum: 2500 * time.Millisecond, maximum: 5 * time.Second},
	}
	for _, test := range cases {
		delay := callbackOutboxRetryDelay(base, maximum, test.attempt, 0)
		if delay < test.minimum || delay > test.maximum {
			t.Errorf("retry delay for attempt %d = %s, want [%s, %s]", test.attempt, delay, test.minimum, test.maximum)
		}
	}
	if delay := callbackOutboxRetryDelay(base, maximum, 1, 3*time.Second); delay != 3*time.Second {
		t.Errorf("Retry-After delay = %s, want 3s", delay)
	}
	if delay := callbackOutboxRetryDelay(base, maximum, 1, 8*time.Second); delay != maximum {
		t.Errorf("Retry-After above maximum = %s, want cap %s", delay, maximum)
	}
}

func TestCallbackOutboxPersistsRetryAfterScheduleAcrossRestart(t *testing.T) {
	root := t.TempDir()
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{
		Root: root, RetryInterval: 5 * time.Millisecond, MaxRetryInterval: 50 * time.Millisecond,
	})
	event := testCallbackEvent()
	if claimed, err := queue.EnqueueAndClaim(event); err != nil || !claimed {
		t.Fatalf("EnqueueAndClaim = (%v, %v), want claimed", claimed, err)
	}
	started := time.Now()
	if err := queue.FailWithRetryAfter(event.ID, time.Second); err != nil {
		t.Fatalf("FailWithRetryAfter: %v", err)
	}
	payload, err := os.ReadFile(filepath.Join(root, event.ID+".json"))
	if err != nil {
		t.Fatalf("read retry record: %v", err)
	}
	var record callbackOutboxRecord
	if err := json.Unmarshal(payload, &record); err != nil {
		t.Fatalf("decode retry record: %v", err)
	}
	remaining := record.NextAttemptAt.Sub(started)
	if record.Attempts != 1 || remaining < 50*time.Millisecond || remaining > 60*time.Millisecond {
		t.Fatalf("retry record = attempts %d, remaining %s, want one attempt and configured 50ms cap", record.Attempts, remaining)
	}

	restarted := newTestCallbackOutbox(t, CallbackOutboxConfig{
		Root: root, RetryInterval: 5 * time.Millisecond, MaxRetryInterval: 50 * time.Millisecond,
	})
	restarted.mu.Lock()
	restored := restarted.items[event.ID].record.NextAttemptAt
	restarted.mu.Unlock()
	if !restored.Equal(record.NextAttemptAt) {
		t.Fatalf("restart retry timestamp = %s, want persisted %s", restored, record.NextAttemptAt)
	}
}

func TestCallbackOutboxListsDeadLetterMetadataWithoutPrivatePayload(t *testing.T) {
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{MaxAttempts: 1})
	for _, id := range []string{"evt_dead_a", "evt_dead_b"} {
		event := testCallbackEvent()
		event.ID = id
		if claimed, err := queue.EnqueueAndClaim(event); err != nil || !claimed {
			t.Fatalf("EnqueueAndClaim %s = (%v, %v), want claimed", id, claimed, err)
		}
		if err := queue.Fail(id); err != nil {
			t.Fatalf("Fail %s: %v", id, err)
		}
	}

	first, err := queue.ListDeadLetters("", 1)
	if err != nil {
		t.Fatalf("ListDeadLetters first page: %v", err)
	}
	if len(first.Items) != 1 || first.Items[0].ID != "evt_dead_a" || first.NextCursor != "evt_dead_a" {
		t.Fatalf("first page = %+v, want evt_dead_a and its cursor", first)
	}
	entry := first.Items[0]
	if entry.Type != EventMessage || entry.EndpointID != "endpoint-1" || entry.ConnectionID != "connection-1" ||
		entry.Sequence != 7 || entry.Attempts != 1 || entry.SizeBytes <= 0 || entry.OccurredAt.IsZero() ||
		entry.EnqueuedAt.IsZero() || entry.DeadLetteredAt.IsZero() {
		t.Fatalf("dead-letter metadata = %+v, missing expected values", entry)
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"hello", "callback-secret", "https://example.com/callback"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("metadata JSON contains private callback value %q: %s", secret, encoded)
		}
	}

	second, err := queue.ListDeadLetters(first.NextCursor, 1)
	if err != nil {
		t.Fatalf("ListDeadLetters second page: %v", err)
	}
	if len(second.Items) != 1 || second.Items[0].ID != "evt_dead_b" || second.NextCursor != "" {
		t.Fatalf("second page = %+v, want evt_dead_b and no next cursor", second)
	}
}

func TestCallbackOutboxReplaysDeadLetterWithOriginalIDAndResetAttempts(t *testing.T) {
	root := t.TempDir()
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{Root: root, MaxAttempts: 1})
	event := testCallbackEvent()
	if claimed, err := queue.EnqueueAndClaim(event); err != nil || !claimed {
		t.Fatalf("EnqueueAndClaim = (%v, %v), want claimed", claimed, err)
	}
	if err := queue.Fail(event.ID); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	if err := queue.ReplayDeadLetter(event.ID); err != nil {
		t.Fatalf("ReplayDeadLetter: %v", err)
	}
	if err := queue.ReplayDeadLetter(event.ID); err != nil {
		t.Fatalf("repeat ReplayDeadLetter: %v, want idempotent success", err)
	}
	stats := queue.Stats()
	if stats.Pending != 1 || stats.DeadLetterTotal != 0 {
		t.Fatalf("Stats after replay = %+v, want one pending and no dead letters", stats)
	}
	var record callbackOutboxRecord
	payload, err := os.ReadFile(filepath.Join(root, event.ID+".json"))
	if err != nil {
		t.Fatalf("read replayed pending record: %v", err)
	}
	if err := json.Unmarshal(payload, &record); err != nil {
		t.Fatalf("decode replayed pending record: %v", err)
	}
	if record.Event.ID != event.ID || record.Attempts != 0 || !record.NextAttemptAt.IsZero() || record.Event.Sequence != event.Sequence {
		t.Fatalf("replayed record = %+v, want original event and reset attempts", record)
	}
	if record.CallbackURL != event.CallbackURL || record.CallbackAuthToken != event.CallbackAuthToken {
		t.Fatalf("replayed record lost its private delivery configuration: %+v", record)
	}
	claimed, ok, err := queue.ClaimNext()
	if err != nil || !ok || claimed.ID != event.ID {
		t.Fatalf("ClaimNext after replay = (%+v, %v, %v), want original event", claimed, ok, err)
	}
}

func TestCallbackOutboxReplayHonorsPendingCapacityAndInFlightOrder(t *testing.T) {
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{MaxAttempts: 1})
	event := testCallbackEvent()
	if claimed, err := queue.EnqueueAndClaim(event); err != nil || !claimed {
		t.Fatalf("EnqueueAndClaim = (%v, %v), want claimed", claimed, err)
	}
	if err := queue.Fail(event.ID); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	queue.maxBytes = 1
	if err := queue.ReplayDeadLetter(event.ID); !errors.Is(err, ErrCallbackOutboxFull) {
		t.Fatalf("ReplayDeadLetter at capacity = %v, want ErrCallbackOutboxFull", err)
	}
	if stats := queue.Stats(); stats.Pending != 0 || stats.DeadLetterTotal != 1 {
		t.Fatalf("Stats after capacity rejection = %+v, want dead letter retained", stats)
	}

	queue.maxBytes = 1 << 20
	later := testCallbackEvent()
	later.ID = "evt_later"
	later.Sequence = event.Sequence + 1
	if claimed, err := queue.EnqueueAndClaim(later); err != nil || !claimed {
		t.Fatalf("EnqueueAndClaim later event = (%v, %v), want claimed", claimed, err)
	}
	if err := queue.ReplayDeadLetter(event.ID); !errors.Is(err, ErrCallbackDeadLetterConflict) {
		t.Fatalf("ReplayDeadLetter with later in-flight event = %v, want order conflict", err)
	}
}

func TestCallbackOutboxEvictsOldestDeadLetterWithoutTouchingPending(t *testing.T) {
	root := t.TempDir()
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{Root: root, MaxAttempts: 1})
	first := testCallbackEvent()
	first.ID = "evt_dead_older1"
	if claimed, err := queue.EnqueueAndClaim(first); err != nil || !claimed {
		t.Fatalf("first EnqueueAndClaim = (%v, %v)", claimed, err)
	}
	if err := queue.Fail(first.ID); err != nil {
		t.Fatal(err)
	}
	queue.deadMaxBytes = queue.Stats().DeadLetterBytes + 128
	second := testCallbackEvent()
	second.ID = "evt_dead_newer2"
	if claimed, err := queue.EnqueueAndClaim(second); err != nil || !claimed {
		t.Fatalf("second EnqueueAndClaim = (%v, %v)", claimed, err)
	}
	if err := queue.Fail(second.ID); err != nil {
		t.Fatal(err)
	}
	stats := queue.Stats()
	if stats.DeadLetterTotal != 1 || stats.DeadLetterEvictions != 1 || stats.DeadLetterBytes > stats.DeadLetterCapacityBytes {
		t.Fatalf("Stats after eviction = %+v", stats)
	}
	if _, err := os.Stat(filepath.Join(root, "dead", first.ID+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oldest dead letter stat = %v, want not found", err)
	}
	if _, err := os.Stat(filepath.Join(root, "dead", second.ID+".json")); err != nil {
		t.Fatalf("newest dead letter stat: %v", err)
	}
	pending := testCallbackEvent()
	pending.ID = "evt_pending"
	if claimed, err := queue.EnqueueAndClaim(pending); err != nil || !claimed {
		t.Fatalf("pending EnqueueAndClaim = (%v, %v)", claimed, err)
	}
	if queue.Stats().Pending != 1 {
		t.Fatalf("pending callback removed by dead-letter retention: %+v", queue.Stats())
	}
}

func TestCallbackOutboxPrunesExistingDeadLettersOnRestart(t *testing.T) {
	root := t.TempDir()
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{Root: root, MaxAttempts: 1})
	for _, id := range []string{"evt_restart_old", "evt_restart_new"} {
		event := testCallbackEvent()
		event.ID = id
		if claimed, err := queue.EnqueueAndClaim(event); err != nil || !claimed {
			t.Fatalf("EnqueueAndClaim %s = (%v, %v)", id, claimed, err)
		}
		if err := queue.Fail(id); err != nil {
			t.Fatal(err)
		}
	}
	oldPath := filepath.Join(root, "dead", "evt_restart_old.json")
	newPath := filepath.Join(root, "dead", "evt_restart_new.json")
	oldTime := time.Now().Add(-time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	pending := testCallbackEvent()
	pending.ID = "evt_restart_pending"
	if claimed, err := queue.EnqueueAndClaim(pending); err != nil || !claimed {
		t.Fatalf("pending EnqueueAndClaim = (%v, %v)", claimed, err)
	}
	queue.Release(pending.ID)
	newInfo, err := os.Stat(newPath)
	if err != nil {
		t.Fatal(err)
	}
	restarted := newTestCallbackOutbox(t, CallbackOutboxConfig{Root: root, DeadLetterMaxBytes: newInfo.Size()})
	stats := restarted.Stats()
	if stats.Pending != 1 || stats.DeadLetterTotal != 1 || stats.DeadLetterBytes != newInfo.Size() || stats.DeadLetterEvictions != 1 {
		t.Fatalf("restarted Stats = %+v", stats)
	}
	if _, err := os.Stat(oldPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old dead letter stat = %v, want not found", err)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("new dead letter stat: %v", err)
	}
}

func TestCallbackOutboxRetentionToleratesOperatorRemovedFile(t *testing.T) {
	root := t.TempDir()
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{Root: root, MaxAttempts: 1})
	first := testCallbackEvent()
	first.ID = "evt_removed_older"
	if claimed, err := queue.EnqueueAndClaim(first); err != nil || !claimed {
		t.Fatalf("first EnqueueAndClaim = (%v, %v)", claimed, err)
	}
	if err := queue.Fail(first.ID); err != nil {
		t.Fatal(err)
	}
	queue.deadMaxBytes = queue.Stats().DeadLetterBytes + 128
	if err := os.Remove(filepath.Join(root, "dead", first.ID+".json")); err != nil {
		t.Fatal(err)
	}
	second := testCallbackEvent()
	second.ID = "evt_removed_newer"
	if claimed, err := queue.EnqueueAndClaim(second); err != nil || !claimed {
		t.Fatalf("second EnqueueAndClaim = (%v, %v)", claimed, err)
	}
	if err := queue.Fail(second.ID); err != nil {
		t.Fatalf("Fail with removed prior dead letter: %v", err)
	}
	stats := queue.Stats()
	if stats.DeadLetterTotal != 1 || stats.DeadLetterEvictions != 0 || stats.DeadLetterBytes > stats.DeadLetterCapacityBytes {
		t.Fatalf("Stats after reconciling missing file = %+v", stats)
	}
}

func TestCallbackOutboxRunReplaysPendingEvents(t *testing.T) {
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{MaxAttempts: 2})
	event := testCallbackEvent()
	if claimed, err := queue.EnqueueAndClaim(event); err != nil || !claimed {
		t.Fatalf("EnqueueAndClaim = (%v, %v), want claimed", claimed, err)
	} else {
		queue.Release(event.ID)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	delivered := make(chan Event, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- queue.Run(ctx, func(_ context.Context, event Event) error {
			delivered <- event
			cancel()
			return nil
		})
	}()
	select {
	case got := <-delivered:
		if got.ID != event.ID {
			t.Fatalf("delivered event ID = %q, want %q", got.ID, event.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for outbox replay")
	}
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
}

func TestCallbackOutboxStatsTracksOldestPendingAgeAndReplayDeliveries(t *testing.T) {
	root := t.TempDir()
	newTestCallbackOutbox(t, CallbackOutboxConfig{Root: root})

	now := time.Now().UTC()
	events := []struct {
		event      Event
		enqueuedAt time.Time
	}{
		{event: testCallbackEvent(), enqueuedAt: now.Add(-3 * time.Minute)},
		{event: testCallbackEvent(), enqueuedAt: now.Add(-time.Minute)},
	}
	events[0].event.ID = "evt_age_a"
	events[0].event.ConnectionID = "connection-a"
	events[0].event.At = now
	events[1].event.ID = "evt_age_b"
	events[1].event.ConnectionID = "connection-b"
	events[1].event.At = now
	for _, item := range events {
		record := callbackOutboxRecord{
			Event:             item.event,
			CallbackURL:       item.event.CallbackURL,
			CallbackPath:      item.event.CallbackPath,
			CallbackAuthToken: item.event.CallbackAuthToken,
			EnqueuedAt:        item.enqueuedAt,
		}
		payload, err := json.Marshal(record)
		if err != nil {
			t.Fatalf("marshal record %q: %v", item.event.ID, err)
		}
		if err := writeCallbackOutboxFile(filepath.Join(root, item.event.ID+".json"), payload); err != nil {
			t.Fatalf("write record %q: %v", item.event.ID, err)
		}
	}
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{Root: root})
	stats := queue.Stats()
	if stats.Pending != 2 || stats.OldestPendingAgeSeconds < 170 || stats.OldestPendingAgeSeconds > 200 {
		t.Fatalf("initial stats = %+v, want two pending and oldest age near three minutes", stats)
	}

	event, ok, err := queue.ClaimNext()
	if err != nil || !ok || event.ID != "evt_age_a" {
		t.Fatalf("ClaimNext = (%+v, %v, %v), want oldest event evt_age_a", event, ok, err)
	}
	if err := queue.ackReplay(event.ID); err != nil {
		t.Fatalf("ackReplay: %v", err)
	}
	stats = queue.Stats()
	if stats.Pending != 1 || stats.ReplayDeliveries != 1 ||
		stats.OldestPendingAgeSeconds < 50 || stats.OldestPendingAgeSeconds > 80 {
		t.Fatalf("stats after replay = %+v, want one pending near one minute and one replay delivery", stats)
	}
}

func TestCallbackOutboxStatsTracksFailedReplayAttemptsAndBackoff(t *testing.T) {
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{
		RetryInterval: time.Hour, MaxRetryInterval: time.Hour,
	})
	event := testCallbackEvent()
	if claimed, err := queue.EnqueueAndClaim(event); err != nil || !claimed {
		t.Fatalf("EnqueueAndClaim = (%v, %v), want claimed", claimed, err)
	}
	queue.Release(event.ID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	attempted := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		errCh <- queue.Run(ctx, func(context.Context, Event) error {
			close(attempted)
			return errors.New("receiver unavailable")
		})
	}()
	select {
	case <-attempted:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("timed out waiting for failed callback attempt")
	}
	deadline := time.After(time.Second)
	for {
		stats := queue.Stats()
		if stats.ReplayAttempts == 1 && stats.ReplayDeliveries == 0 &&
			stats.ReplayReady == 0 && stats.ReplayDelayed == 1 {
			break
		}
		select {
		case <-deadline:
			cancel()
			t.Fatalf("stats after failed replay = %+v, want one failed attempt and one delayed head", stats)
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
}

func TestCallbackOutboxReplayRunsIndependentConnectionsInParallelAndKeepsOrder(t *testing.T) {
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{ReplayWorkers: 2})
	first := testCallbackEvent()
	first.ID = "evt_parallel_first"
	first.Sequence = 1
	second := first
	second.ID = "evt_parallel_second"
	second.Sequence = 2
	independent := first
	independent.ID = "evt_parallel_independent"
	independent.ConnectionID = "connection-2"
	for _, event := range []Event{first, second, independent} {
		claimed, err := queue.EnqueueAndClaim(event)
		if err != nil {
			t.Fatalf("EnqueueAndClaim(%q): %v", event.ID, err)
		}
		if claimed {
			queue.Release(event.ID)
		}
	}

	release := map[string]chan struct{}{
		first.ID:       make(chan struct{}),
		second.ID:      make(chan struct{}),
		independent.ID: make(chan struct{}),
	}
	started := make(chan string, len(release))
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- queue.Run(ctx, func(ctx context.Context, event Event) error {
			started <- event.ID
			select {
			case <-release[event.ID]:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()

	initial := make(map[string]bool, 2)
	for range 2 {
		select {
		case id := <-started:
			initial[id] = true
		case <-time.After(time.Second):
			cancel()
			t.Fatal("timed out waiting for independent callbacks to start in parallel")
		}
	}
	if !initial[first.ID] || !initial[independent.ID] || initial[second.ID] {
		cancel()
		t.Fatalf("callbacks started before releasing the first event = %v, want first and independent only", initial)
	}

	close(release[independent.ID])
	close(release[first.ID])
	select {
	case id := <-started:
		if id != second.ID {
			cancel()
			t.Fatalf("next callback = %q, want same-connection successor %q", id, second.ID)
		}
	case <-time.After(time.Second):
		cancel()
		t.Fatal("timed out waiting for same-connection successor")
	}
	close(release[second.ID])
	deadline := time.After(time.Second)
	for queue.Stats().Pending != 0 {
		select {
		case <-deadline:
			cancel()
			t.Fatal("timed out waiting for the final callback acknowledgement")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	if stats := queue.Stats(); stats.Pending != 0 {
		t.Fatalf("pending after successful replay = %d, want 0", stats.Pending)
	}
}

func TestCallbackOutboxReplayCancellationReleasesClaim(t *testing.T) {
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{ReplayWorkers: 1})
	event := testCallbackEvent()
	claimed, err := queue.EnqueueAndClaim(event)
	if err != nil || !claimed {
		t.Fatalf("EnqueueAndClaim = (%v, %v), want claimed", claimed, err)
	}
	queue.Release(event.ID)

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		errCh <- queue.Run(ctx, func(ctx context.Context, _ Event) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("timed out waiting for callback replay")
	}
	cancel()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}

	got, ok, err := queue.ClaimNext()
	if err != nil || !ok || got.ID != event.ID {
		t.Fatalf("ClaimNext after cancellation = (%+v, %v, %v), want released event", got, ok, err)
	}
	if err := queue.Ack(got.ID); err != nil {
		t.Fatalf("Ack after cancellation: %v", err)
	}
}

func TestCallbackOutboxRejectsFullPayload(t *testing.T) {
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{MaxBytes: 1})
	if _, err := queue.EnqueueAndClaim(testCallbackEvent()); !errors.Is(err, ErrCallbackOutboxFull) {
		t.Fatalf("EnqueueAndClaim full = %v, want ErrCallbackOutboxFull", err)
	}
}

func TestCallbackOutboxSupportsDisconnectEvents(t *testing.T) {
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{})
	event := testCallbackEvent()
	event.Type = EventDisconnect
	if claimed, err := queue.EnqueueAndClaim(event); err != nil || !claimed {
		t.Fatalf("EnqueueAndClaim disconnect = (%v, %v), want claimed", claimed, err)
	}
	if err := queue.Ack(event.ID); err != nil {
		t.Fatalf("Ack disconnect: %v", err)
	}
}

func TestManagerStatsIncludesCallbackOutboxCounters(t *testing.T) {
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{})
	event := testCallbackEvent()
	if claimed, err := queue.EnqueueAndClaim(event); err != nil || !claimed {
		t.Fatalf("EnqueueAndClaim = (%v, %v), want claimed", claimed, err)
	}
	queue.Release(event.ID)
	m := NewManager(Config{}, HTTPHooks{DurableQueue: queue})
	defer m.Close()
	stats := m.Stats()
	if stats.CallbackPending != 1 || stats.CallbackPendingBytes == 0 || stats.CallbackDeadLetters != 0 {
		t.Fatalf("Manager Stats = %+v, want pending callback counters", stats)
	}
}
