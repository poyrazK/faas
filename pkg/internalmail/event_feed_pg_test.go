package internalmail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
)

func eventFeedFixture(t *testing.T) *pgFixture {
	t.Helper()
	f := producerFixture(t)
	f.service.EventFeedEnabled = func() bool { return true }
	return f
}
func publishFeed(t *testing.T, f *pgFixture) int {
	t.Helper()
	n, err := f.service.PublishEventFeed(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return n
}
func feedTarget(t *testing.T, f *pgFixture, id string) MessageResponse {
	t.Helper()
	m := produce(t, f, id, uuid.NewString())
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.delivery.Store(false)
	return m
}
func pullFeed(t *testing.T, f *pgFixture, id, name string, limit int) EventFeedBatch {
	t.Helper()
	b, err := f.service.PullEventFeed(t.Context(), id, name, limit)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func ackFeed(t *testing.T, f *pgFixture, b EventFeedBatch) EventFeedAck {
	t.Helper()
	a, err := f.service.AckEventFeed(t.Context(), b.ProducerID, b.Consumer, EventFeedAckInput{Receipt: b.Receipt, Generation: b.Generation})
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func feedStatus(t *testing.T, f *pgFixture, id, name string) EventConsumerStatus {
	t.Helper()
	s, err := f.service.ProducerEventConsumer(t.Context(), id, name)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func feedReplay(producer, consumer, cursor string, generation int64) EventFeedReplayRequest {
	return EventFeedReplayRequest{OperatorAuditInput: auditInput(), ProducerID: producer, Consumer: consumer, Cursor: cursor, ExpectedGeneration: &generation}
}

func TestPGEventFeedDurableBatchRestartAndAckReplay(t *testing.T) {
	f := eventFeedFixture(t)
	m := feedTarget(t, f, "auth")
	if err := f.store.Report(t.Context(), Report{EventID: "private-provider@example.net", RecipientID: m.Recipients[0].ID, Kind: "accepted", Code: "private bearer diagnostic"}); err != nil {
		t.Fatal(err)
	}
	legacy := singleMail(t, f, "legacy-excluded")
	if publishFeed(t, f) != 3 || publishFeed(t, f) != 0 {
		t.Fatal("feed duplicated events or included unscoped mail", legacy.ID)
	}
	b := pullFeed(t, f, "auth", "effects", 2)
	if len(b.Events) != 2 || b.Generation != 1 || b.Cursor != "" || b.NextCursor != b.Events[1].ID {
		t.Fatal("bad initial batch", b)
	}
	raw, _ := json.Marshal(b)
	for _, secret := range []string{"Secret verification", "example.net", "private-provider", "bearer", "address", "signing", "payload"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("feed leaked private message data", secret)
		}
	}
	restarted, _ := NewPGStore(f.pool, "platform")
	f.service.Store = restarted
	f.store = restarted
	if replay := pullFeed(t, f, "auth", "effects", 1); !reflect.DeepEqual(replay, b) {
		t.Fatal("restart or changed limit changed pending batch")
	}
	ack := ackFeed(t, f, b)
	if ack.Cursor != b.NextCursor {
		t.Fatal("ack advanced incorrectly")
	}
	next := pullFeed(t, f, "auth", "effects", 100)
	if len(next.Events) != 1 || next.Events[0].Kind != "accepted" || next.Cursor != b.NextCursor {
		t.Fatal("next batch lost outcome", next)
	}
	if old := ackFeed(t, f, b); old != ack {
		t.Fatal("ack replay changed result")
	}
	if status := feedStatus(t, f, "auth", "effects"); status.PendingReceipt != next.Receipt {
		t.Fatal("old ack cleared a newer batch")
	}
	ackFeed(t, f, next)
	ackFeed(t, f, b)
	if status := feedStatus(t, f, "auth", "effects"); status.Cursor != next.NextCursor || status.Backlog != 0 {
		t.Fatal("late ack replay moved checkpoint", status)
	}
	if empty := pullFeed(t, f, "auth", "effects", 2); len(empty.Events) != 0 || empty.Receipt != "" || empty.Cursor != next.NextCursor {
		t.Fatal("empty poll moved cursor")
	}
	independent := pullFeed(t, f, "auth", "analytics", 100)
	if len(independent.Events) != 3 {
		t.Fatal("independent consumer inherited checkpoint")
	}
	stats, err := f.store.eventFeedStatistics(t.Context())
	if err != nil || stats.Unpublished != 0 || stats.Published != 3 || stats.Consumers != 2 || stats.Unacked != 3 || stats.MaxBacklog != 3 || stats.PendingBatches != 1 {
		t.Fatal("bad durable lag statistics", stats, err)
	}
}

func TestPGEventFeedLateSourceCommitAndAtomicPublication(t *testing.T) {
	f := eventFeedFixture(t)
	first := feedTarget(t, f, "auth")
	second := feedTarget(t, f, "auth")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackOperator(ctx, tx)
	if err := f.store.event(ctx, tx, "000-late-source", first.Recipients[0].ID, "accepted", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "UPDATE internal_mail_events SET created_at=clock_timestamp()-interval '1 hour' WHERE id='000-late-source'"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Report(ctx, Report{EventID: "zzz-committed-first", RecipientID: second.Recipients[0].ID, Kind: "accepted"}); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	done := make(chan error, 1)
	go func() {
		calls := 0
		_, err := f.store.publishEventFeed(ctx, func() bool {
			calls++
			if calls == 2 {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return false
				}
			}
			return true
		})
		done <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("publisher never reached commit fence")
	}
	if b := pullFeed(t, f, "auth", "effects", 100); len(b.Events) != 0 || b.Cursor != "" || b.Receipt != "" {
		t.Fatal("consumer advanced past uncommitted publication")
	}
	unblock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	before := pullFeed(t, f, "auth", "effects", 100)
	if len(before.Events) != 5 {
		t.Fatal("uncommitted source became visible", len(before.Events))
	}
	ackFeed(t, f, before)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if publishFeed(t, f) != 1 {
		t.Fatal("late transaction was skipped")
	}
	after := pullFeed(t, f, "auth", "effects", 100)
	if len(after.Events) != 1 || after.Events[0].Sequence <= before.Events[len(before.Events)-1].Sequence || !after.Events[0].OccurredAt.Before(before.Events[0].OccurredAt) {
		t.Fatal("source time or ID was used as a commit cursor", after)
	}
}

func TestPGEventFeedConcurrentPublishersBoundedPassesAndConsumerBudget(t *testing.T) {
	f := eventFeedFixture(t)
	m := feedTarget(t, f, "auth")
	for i := range api.InternalMailEventFeedBatchSize + 23 {
		if err := f.store.Report(t.Context(), Report{EventID: fmt.Sprintf("deferred:%03d", i), RecipientID: m.Recipients[0].ID, Kind: "deferred"}); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	counts := make(chan int, 4)
	for range 4 {
		wg.Go(func() {
			n, err := f.service.PublishEventFeed(t.Context())
			if err != nil {
				t.Error(err)
				return
			}
			counts <- n
		})
	}
	wg.Wait()
	close(counts)
	total := 0
	for n := range counts {
		if n > api.InternalMailEventFeedBatchSize {
			t.Fatal("unbounded publisher pass")
		}
		total += n
	}
	if total != api.InternalMailEventFeedBatchSize+25 {
		t.Fatal("concurrent publication duplicated or lost events", total)
	}
	seen := map[string]bool{}
	last := int64(0)
	for {
		b := pullFeed(t, f, "auth", "ordered", 100)
		if len(b.Events) == 0 {
			break
		}
		for _, e := range b.Events {
			if e.Sequence != last+1 || seen[e.ID] {
				t.Fatal("sequence gap or duplicate event")
			}
			last = e.Sequence
			seen[e.ID] = true
		}
		ackFeed(t, f, b)
	}
	if len(seen) != total {
		t.Fatal("batch pagination skipped events")
	}
	results := make(chan string, 16)
	for i := range 16 {
		wg.Go(func() {
			name := fmt.Sprintf("consumer-%d", i)
			_, err := f.service.PullEventFeed(t.Context(), "billing", name, 1)
			if err == nil {
				results <- name
			} else if !errors.Is(err, ErrQuota) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	close(results)
	n := 0
	for name := range results {
		n++
		pullFeed(t, f, "billing", name, 1)
	}
	if n != api.InternalMailEventFeedMaxConsumers {
		t.Fatal("concurrent consumer budget was not atomic", n)
	}
}

func TestPGEventFeedOperatorRewindFencesStaleAcknowledgments(t *testing.T) {
	f := eventFeedFixture(t)
	m := feedTarget(t, f, "auth")
	feedTarget(t, f, "billing")
	publishFeed(t, f)
	b := pullFeed(t, f, "auth", "effects", 100)
	ackFeed(t, f, b)
	if err := f.store.Report(t.Context(), Report{EventID: "later-outcome", RecipientID: m.Recipients[0].ID, Kind: "accepted"}); err != nil {
		t.Fatal(err)
	}
	publishFeed(t, f)
	pending := pullFeed(t, f, "auth", "effects", 100)
	request := feedReplay("auth", "effects", b.Events[0].ID, 1)
	record, err := f.service.ReplayEventConsumer(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	var status EventConsumerStatus
	if json.Unmarshal(record.Result, &status) != nil || status.Generation != 2 || status.Cursor != b.Events[0].ID || status.PendingReceipt != "" {
		t.Fatal("rewind result", status)
	}
	if _, err := f.service.AckEventFeed(t.Context(), "auth", "effects", EventFeedAckInput{Receipt: pending.Receipt, Generation: 1}); !errors.Is(err, ErrEventFeedGeneration) {
		t.Fatal("old batch advanced rewound consumer", err)
	}
	replayed := pullFeed(t, f, "auth", "effects", 100)
	if replayed.Generation != 2 || len(replayed.Events) != 2 || replayed.Events[0].ID != b.Events[1].ID || replayed.Events[1].ID != pending.Events[0].ID {
		t.Fatal("rewind changed stable event identities", replayed)
	}
	ackFeed(t, f, replayed)
	if _, err := f.service.ReplayEventConsumer(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if got := feedStatus(t, f, "auth", "effects"); got.Cursor != replayed.NextCursor || got.Generation != 2 {
		t.Fatal("request replay reapplied old rewind")
	}
	foreign := pullFeed(t, f, "billing", "effects", 100)
	if _, err := f.service.ReplayEventConsumer(t.Context(), feedReplay("auth", "effects", foreign.NextCursor, 2)); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-producer cursor accepted", err)
	}
	if _, err := f.service.ReplayEventConsumer(t.Context(), feedReplay("auth", "effects", "", 1)); !errors.Is(err, ErrEventFeedGeneration) {
		t.Fatal("stale generation accepted", err)
	}
	if _, err := f.service.ReplayEventConsumer(t.Context(), feedReplay("auth", "effects", "", 2)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ReplayEventConsumer(t.Context(), feedReplay("auth", "effects", replayed.NextCursor, 3)); !errors.Is(err, ErrInvalid) {
		t.Fatal("rewind skipped unacknowledged events", err)
	}
	if _, err := f.service.ReplayEventConsumer(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if got := feedStatus(t, f, "auth", "effects"); got.Generation != 3 || got.Cursor != "" {
		t.Fatal("old replay undid newer rewind")
	}
	changed := request
	changed.Evidence = "ticket:changed"
	if _, err := f.service.ReplayEventConsumer(t.Context(), changed); !errors.Is(err, ErrConflict) {
		t.Fatal("changed replay accepted", err)
	}
}

func TestPGEventFeedGateRevocationRollsBackPublicationPullAckAndReplay(t *testing.T) {
	f := eventFeedFixture(t)
	feedTarget(t, f, "auth")
	gate := func() func() bool { calls := 0; return func() bool { calls++; return calls == 1 } }
	if _, err := f.store.publishEventFeed(t.Context(), gate()); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	stats, err := f.store.eventFeedStatistics(t.Context())
	if err != nil || stats.Published != 0 || stats.Unpublished != 2 {
		t.Fatal("partial publication", stats, err)
	}
	publishFeed(t, f)
	if _, err := f.store.pullEventFeed(t.Context(), "auth", "effects", 100, gate()); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	if _, err := f.store.eventConsumerStatus(t.Context(), "auth", "effects"); !errors.Is(err, ErrNotFound) {
		t.Fatal("partial consumer creation")
	}
	b := pullFeed(t, f, "auth", "effects", 100)
	if b.Events[0].Sequence != 1 {
		t.Fatal("rollback spent event position")
	}
	if _, err := f.store.ackEventFeed(t.Context(), "auth", "effects", EventFeedAckInput{Receipt: b.Receipt, Generation: 1}, gate()); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	if status := feedStatus(t, f, "auth", "effects"); status.Cursor != "" || status.PendingReceipt != b.Receipt {
		t.Fatal("partial acknowledgment")
	}
	ackFeed(t, f, b)
	if _, err := f.store.replayEventConsumer(t.Context(), feedReplay("auth", "effects", "", 1), gate()); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	if status := feedStatus(t, f, "auth", "effects"); status.Generation != 1 || status.Cursor != b.NextCursor {
		t.Fatal("partial rewind")
	}
}

func TestPGEventFeedNamespaceOwnershipAndDisableResume(t *testing.T) {
	f := eventFeedFixture(t)
	feedTarget(t, f, "auth")
	feedTarget(t, f, "billing")
	publishFeed(t, f)
	a := pullFeed(t, f, "auth", "effects", 100)
	b := pullFeed(t, f, "billing", "effects", 100)
	if _, err := f.service.AckEventFeed(t.Context(), "billing", "effects", EventFeedAckInput{Receipt: a.Receipt, Generation: a.Generation}); !errors.Is(err, ErrNotFound) {
		t.Fatal("ack crossed producer scope", err)
	}
	other, _ := NewPGStore(f.pool, "other")
	if n, err := other.publishEventFeed(t.Context(), func() bool { return true }); err != nil || n != 0 {
		t.Fatal("publication crossed namespace", err)
	}
	if _, err := other.eventConsumerStatus(t.Context(), "auth", "effects"); !errors.Is(err, ErrNotFound) {
		t.Fatal("consumer crossed namespace")
	}
	if _, err := other.ackEventFeed(t.Context(), "auth", "effects", EventFeedAckInput{Receipt: a.Receipt, Generation: 1}, func() bool { return true }); !errors.Is(err, ErrNotFound) {
		t.Fatal("receipt crossed namespace")
	}
	p := f.service.Producers["auth"]
	p.Token = "new-auth-token-at-least-thirty-two-bytes"
	f.service.Producers["auth"] = p
	if got := pullFeed(t, f, "auth", "effects", 1); !reflect.DeepEqual(got, a) {
		t.Fatal("credential rotation changed consumer identity")
	}
	f.service.EventFeedEnabled = func() bool { return false }
	f.service.Codec = nil
	f.service.Transport = nil
	if _, err := f.service.PublishEventFeed(t.Context()); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	if _, err := f.service.AckEventFeed(t.Context(), "billing", "effects", EventFeedAckInput{Receipt: b.Receipt, Generation: 1}); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	f.service.EventFeedEnabled = func() bool { return true }
	if got := pullFeed(t, f, "billing", "effects", 100); !reflect.DeepEqual(got, b) {
		t.Fatal("disable discarded pending batch")
	}
	delete(f.service.Producers, "auth")
	if _, err := f.service.PullEventFeed(t.Context(), "auth", "effects", 100); !errors.Is(err, ErrNotFound) {
		t.Fatal("withdrawn producer retained access")
	}
	if status, err := f.store.eventConsumerStatus(t.Context(), "auth", "effects"); err != nil || status.PendingReceipt != a.Receipt {
		t.Fatal("withdrawal erased metadata")
	}
}

func TestPGEventFeedMigrationReplayAndRollbackGuards(t *testing.T) {
	f := eventFeedFixture(t)
	raw, _ := migrations.FS.ReadFile("20261016123727973_internal_mail_event_feed.sql")
	parts := strings.Split(string(raw), "-- +goose StatementBegin\n")
	up := strings.Split(parts[1], "-- +goose StatementEnd")[0]
	down := strings.Split(parts[2], "-- +goose StatementEnd")[0]
	for range 2 {
		if _, err := f.pool.Exec(t.Context(), down); err != nil {
			t.Fatal(err)
		}
	}
	if f.store.Ready(t.Context()) == nil {
		t.Fatal("missing feed schema passed readiness")
	}
	for range 2 {
		if _, err := f.pool.Exec(t.Context(), up); err != nil {
			t.Fatal(err)
		}
	}
	pullFeed(t, f, "auth", "empty", 1)
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback discarded empty consumer checkpoint")
	}
	if err := f.store.Ready(t.Context()); err != nil {
		t.Fatal("rollback damaged schema", err)
	}
}

func TestPGEventFeedIncludesSQLLifecycleAndAuthenticatedFeedback(t *testing.T) {
	f := eventFeedFixture(t)
	signer := enableFeedback(t, f)
	m := feedTarget(t, f, "auth")
	cancelled := produce(t, f, "auth", "feed-cancelled")
	if !cancellation(t, f, cancelled.ID, true).Complete {
		t.Fatal("queued cancellation failed")
	}
	f.service.SchedulingEnabled = func() bool { return true }
	expires := time.Now().Add(time.Hour)
	input := producerRequest()
	input.ExpiresAt = &expires
	timed, err := f.service.SubmitProduced(t.Context(), "auth", "feed-expired", input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_messages SET created_at=clock_timestamp()-interval '1 minute',expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", timed.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	unknown := produce(t, f, "auth", "feed-unknown")
	r, err := f.service.claimMail(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if r.MessageID != unknown.ID {
		t.Fatal("unexpected queued work")
	}
	if err := f.service.beginMailSubmission(t.Context(), r, f.domain.Selector); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_recipients SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1", r.ID); err != nil {
		t.Fatal(err)
	}
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.delivery.Store(false)
	if err := f.store.Report(t.Context(), Report{EventID: "accepted-before-bounce", RecipientID: m.Recipients[0].ID, Kind: "accepted"}); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"dsn", "arf"} {
		raw := signer.sign(t, sampleFeedback(m.Recipients[0].ID, "example.test", m.Recipients[0].Address, format, "5.1.1"), "feedback.provider.test", nil)
		if err := f.service.IngestFeedback(t.Context(), "provider", raw); err != nil {
			t.Fatal(err)
		}
	}
	f.service.Codec = nil
	f.service.Transport = nil
	if publishFeed(t, f) != 11 {
		t.Fatal("lifecycle feed lost source events")
	}
	b := pullFeed(t, f, "auth", "effects", 100)
	kinds := map[string]int{}
	for _, e := range b.Events {
		kinds[e.Kind]++
	}
	for _, kind := range []string{"submitted", "cancelled", "expired", "uncertain", "accepted", "bounced", "complained"} {
		if kinds[kind] != 1 {
			t.Fatal("missing lifecycle event", kind, kinds)
		}
	}
	if kinds["queued"] != 4 {
		t.Fatal("queued observations missing")
	}
}
