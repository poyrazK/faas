package internalmail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus"
)

func schedulerFixture(t *testing.T, limit int) *pgFixture {
	t.Helper()
	f := producerFixture(t)
	f.service.ProducerSchedulingEnabled = func() bool { return true }
	for id, p := range f.service.Producers {
		p.DispatchPerMinute = limit
		f.service.Producers[id] = p
	}
	return f
}

func dispatchRows(t *testing.T, f *pgFixture) int {
	t.Helper()
	var count int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM internal_mail_dispatch_reservations WHERE namespace='platform'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestPGProducerSchedulingFairTurnsSurviveRestartAndOffRestoresFIFO(t *testing.T) {
	f := schedulerFixture(t, 10)
	for i := range 6 {
		produce(t, f, "billing", fmt.Sprint("billing-", i))
	}
	for i := range 3 {
		produce(t, f, "auth", fmt.Sprint("auth-", i))
		singleMail(t, f, fmt.Sprint("legacy-", i))
	}
	for i, want := range []string{"", "auth", "billing", "", "auth", "billing", "", "auth", "billing"} {
		restarted, err := NewPGStore(f.pool, "platform")
		if err != nil {
			t.Fatal(err)
		}
		f.service.Store = restarted
		r, err := f.service.claimMail(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		m, err := f.store.Message(t.Context(), r.MessageID)
		if err != nil || m.ProducerID != want {
			t.Fatal("producer backlog displaced another producer", i, want, m.ProducerID, err)
		}
	}
	if dispatchRows(t, f) != 0 {
		t.Fatal("claim consumed a dispatch reservation")
	}
	// Off bypasses fair turns and limits and keeps the original queue order.
	f.service.ProducerSchedulingEnabled = func() bool { return false }
	produce(t, f, "auth", "new-auth")
	r, err := f.service.claimMail(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	m, _ := f.store.Message(t.Context(), r.MessageID)
	if m.ProducerID != "billing" {
		t.Fatal("disabled scheduler changed FIFO")
	}
}

func TestPGProducerSchedulingConcurrentClaimsShareFairness(t *testing.T) {
	f := schedulerFixture(t, 10)
	for _, id := range []string{"auth", "billing"} {
		for i := range 6 {
			produce(t, f, id, fmt.Sprint(id, i))
		}
	}
	var wg sync.WaitGroup
	claimed := make(chan Recipient, 8)
	for range 8 {
		wg.Go(func() {
			store, err := NewPGStore(f.pool, "platform")
			if err != nil {
				t.Error(err)
				return
			}
			policy, _ := f.service.dispatchPolicy()
			r, err := store.claimScheduled(t.Context(), domainAuthorization{}, f.service.allowedProducerSenders(), false, PacingPolicy{}, false, false, policy)
			if err != nil {
				t.Error(err)
				return
			}
			claimed <- r
		})
	}
	wg.Wait()
	close(claimed)
	counts := map[string]int{}
	seen := map[string]bool{}
	for r := range claimed {
		if seen[r.ID] {
			t.Fatal("workers shared a lease")
		}
		seen[r.ID] = true
		m, _ := f.store.Message(t.Context(), r.MessageID)
		counts[m.ProducerID]++
	}
	if counts["auth"] != 4 || counts["billing"] != 4 {
		t.Fatal("independent workers lost producer turns", counts)
	}
}

func TestPGProducerSchedulingConcurrentMinuteBudgetAndCustody(t *testing.T) {
	f := schedulerFixture(t, 2)
	domain, err := f.service.RegisterDomain(t.Context(), "second.test")
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range domain.Records {
		f.resolver[record.Name] = []string{record.Value}
	}
	if _, err := f.service.VerifyDomain(t.Context(), domain.ID); err != nil {
		t.Fatal(err)
	}
	p := f.service.Producers["auth"]
	p.From = append(p.From, "notify@second.test")
	f.service.Producers["auth"] = p
	var recipients []Recipient
	for i := range 8 {
		input := producerRequest()
		if i%2 != 0 {
			input.From = "notify@second.test"
		}
		input.To = []string{fmt.Sprintf("one@destination%d.test", i)}
		if _, err := f.service.SubmitProduced(t.Context(), "auth", fmt.Sprint(i), input); err != nil {
			t.Fatal(err)
		}
		r, err := f.service.claimMail(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		recipients = append(recipients, r)
	}
	var started atomic.Int32
	var wg sync.WaitGroup
	barrier := make(chan struct{})
	for _, r := range recipients {
		wg.Go(func() {
			<-barrier
			// Empty selector still checks the current verified sender domain.
			err := f.service.beginMailSubmission(t.Context(), r, "")
			status, code := "queued", producerThrottleCode
			if err == nil {
				started.Add(1)
				status, code = "uncertain", "fixture_ambiguous"
			} else if !errors.Is(err, ErrProducerThrottled) {
				t.Error(err)
				return
			}
			if err := f.store.Settle(t.Context(), r, status, code, time.Now()); err != nil {
				t.Error(err)
			}
		})
	}
	close(barrier)
	wg.Wait()
	if started.Load() != 2 || dispatchRows(t, f) != 2 {
		t.Fatal("concurrent workers exceeded a producer minute budget", started.Load())
	}
	for _, r := range recipients {
		m, _ := f.store.Message(t.Context(), r.MessageID)
		after := m.Recipients[0]
		if after.Status == "queued" && (after.Attempts != 0 || after.Code != producerThrottleCode) {
			t.Fatal("rate wait charged a delivery attempt", after)
		}
		if after.Status == "uncertain" {
			if err := f.store.Report(t.Context(), Report{EventID: "retry-" + r.ID, RecipientID: r.ID, Kind: "retry", Code: "confirmed_not_accepted"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	f.delivery.Store(true)
	f.service.Codec = nil
	if err := f.service.DeliverOne(t.Context()); err != nil || len(f.transport.envelopes) != 0 {
		t.Fatal("throttled producer decrypted or submitted mail", err)
	}
	// Reconciliation and restart do not refund committed capacity.
	restarted, _ := NewPGStore(f.pool, "platform")
	f.service.Store = restarted
	if _, err := f.service.claimMail(t.Context()); !errors.Is(err, ErrNotFound) {
		t.Fatal("restart lost reservations", err)
	}
	// The same producer name in another namespace has an independent budget.
	other, _ := NewPGStore(f.pool, "other")
	policy, _ := f.service.dispatchPolicy()
	stats, err := other.producerDispatchStatus(t.Context(), policy)
	if err != nil || len(stats) != 3 || stats[1].InWindow != 0 {
		t.Fatal("dispatch budget crossed namespaces", stats, err)
	}
}

func TestPGProducerSchedulingRollingWindowPrunesAndDisablingBypassesLimit(t *testing.T) {
	f := schedulerFixture(t, 1)
	for i := range 4 {
		produce(t, f, "auth", fmt.Sprint(i))
	}
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DeliverOne(t.Context()); err != nil || len(f.transport.envelopes) != 1 {
		t.Fatal("minute limit allowed an idle burst", err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_dispatch_reservations SET started_at=clock_timestamp()-interval '61 seconds'"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := f.service.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.transport.envelopes) != 2 || dispatchRows(t, f) != 1 {
		t.Fatal("rolling window reset accumulated credit or retained expired reservations")
	}
	f.service.ProducerSchedulingEnabled = func() bool { return false }
	if err := f.service.DeliverOne(t.Context()); err != nil || len(f.transport.envelopes) != 3 || dispatchRows(t, f) != 1 {
		t.Fatal("off gate did not bypass limits while preserving reservations", err)
	}
	f.service.ProducerSchedulingEnabled = func() bool { return true }
	if err := f.service.DeliverOne(t.Context()); err != nil || len(f.transport.envelopes) != 3 {
		t.Fatal("re-enabling forgot recent durable reservations", err)
	}
	produce(t, f, "billing", "independent-budget")
	if err := f.service.DeliverOne(t.Context()); err != nil || len(f.transport.envelopes) != 4 {
		t.Fatal("one producer's limit blocked another producer", err)
	}
}

func TestPGProducerSchedulingStatusMetricsAndMigrationGuard(t *testing.T) {
	f := schedulerFixture(t, 1)
	first := produce(t, f, "auth", "first")
	produce(t, f, "auth", "waiting")
	f.delivery.Store(true)
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_messages SET created_at=clock_timestamp()-interval '1 minute' WHERE id<>$1", first.ID); err != nil {
		t.Fatal(err)
	}
	h, err := Handler(f.service, testToken)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, ProducerSchedulerPath, nil)
	req.Header.Set("Authorization", "Bearer "+operatorToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var status []ProducerDispatchStatus
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &status) != nil || len(status) != 3 {
		t.Fatal("private status failed", w.Code, w.Body.String())
	}
	auth := status[1]
	if auth.ProducerID != "auth" || auth.InWindow != 1 || auth.Remaining != 0 || !auth.RateLimited || auth.RetryAt == nil || auth.DueRecipients != 1 || auth.OldestDueSeconds < 59 {
		t.Fatal("status did not explain a producer wait", auth)
	}
	registry := prometheus.NewRegistry()
	registry.MustRegister(NewProducerSchedulerMonitor(f.service))
	metrics, err := registry.Gather()
	if err != nil || len(metrics) != 7 {
		t.Fatal("scheduler metrics unavailable", err)
	}
	values := map[string]float64{}
	for _, family := range metrics {
		if len(family.Metric) != 1 || len(family.Metric[0].Label) != 0 {
			t.Fatal("producer identity leaked in metric labels")
		}
		values[strings.TrimPrefix(family.GetName(), "maild_mail_producer_scheduler_")] = family.Metric[0].GetGauge().GetValue()
	}
	if values["stats_up"] != 1 || values["limited_producers"] != 1 || values["rate_limited_recipients"] != 1 || values["oldest_due_seconds"] < 59 {
		t.Fatal("metrics lost queue wait or limit", values)
	}
	for _, secret := range []string{producerToken, operatorToken, "notify@", "one@", "Secret verification", "sealed_payload"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("status exposed private message or credential")
		}
	}
	raw, _ := migrations.FS.ReadFile("20261016130417046_internal_mail_producer_scheduler.sql")
	parts := strings.Split(string(raw), "-- +goose StatementBegin\n")
	up := strings.Split(parts[1], "-- +goose StatementEnd")[0]
	down := strings.Split(parts[2], "-- +goose StatementEnd")[0]
	for range 2 {
		if _, err := f.pool.Exec(t.Context(), up); err != nil {
			t.Fatal("migration replay failed", err)
		}
	}
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback discarded active scheduling and dispatch reservations")
	}
	if dispatchRows(t, f) != 1 {
		t.Fatal("rejected rollback changed capacity")
	}
}

func TestPGProducerSchedulingLockWaitRollsBackCapacityAndPacing(t *testing.T) {
	for _, expired := range []string{"message", "lease", "authorization"} {
		t.Run(expired, func(t *testing.T) {
			f := schedulerFixture(t, 1)
			f.service.PacingEnabled = func() bool { return true }
			f.service.PacingPolicy.Interval = api.InternalMailDomainPacingInterval
			f.service.SchedulingEnabled = func() bool { return true }
			deadline := time.Now().Add(650 * time.Millisecond)
			input := producerRequest()
			if expired == "message" {
				input.ExpiresAt = &deadline
			}
			if _, err := f.service.SubmitProduced(t.Context(), "auth", "lock-wait", input); err != nil {
				t.Fatal(err)
			}
			if expired == "authorization" {
				f.service.ReverificationEnabled = func() bool { return true }
				if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_domains SET last_verified_at=$1,verified_sending_ip=$2 WHERE id=$3", deadline.Add(-api.InternalMailDomainCheckMaxAge), f.service.SendingIP.String(), f.domain.ID); err != nil {
					t.Fatal(err)
				}
			}
			r, err := f.service.claimMail(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if expired == "lease" {
				if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_recipients SET lease_until=$1 WHERE id=$2", deadline, r.ID); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			lock, err := f.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = lock.Rollback(ctx) }()
			if _, err := lock.Exec(ctx, "SELECT last_turn FROM internal_mail_producer_dispatch WHERE namespace='platform' AND producer_id='auth' FOR UPDATE"); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- f.service.beginMailSubmission(ctx, r, f.domain.Selector) }()
			select {
			case err := <-done:
				t.Fatal("submission did not wait for producer bucket", err)
			case <-time.After(30 * time.Millisecond):
			}
			waitMailTime(t, deadline)
			if err := lock.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			want := ErrLeaseLost
			if expired == "message" {
				want = ErrMessageExpired
			} else if expired == "authorization" {
				want = ErrUnverified
			}
			if err := <-done; !errors.Is(err, want) {
				t.Fatal("producer lock wait bypassed current deadlines", err)
			}
			var pacing int
			if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM internal_mail_domain_pacing").Scan(&pacing); err != nil {
				t.Fatal(err)
			}
			if pacing != 0 || dispatchRows(t, f) != 0 {
				t.Fatal("rejected handoff spent producer capacity or recipient-domain pacing")
			}
		})
	}
}

func TestPGProducerSchedulingThrottledHandoffRefundsPacing(t *testing.T) {
	f := schedulerFixture(t, 1)
	f.service.PacingEnabled = func() bool { return true }
	f.service.PacingPolicy.Interval = api.InternalMailDomainPacingInterval
	produce(t, f, "auth", "first")
	input := producerRequest()
	input.To = []string{"one@another.test"}
	if _, err := f.service.SubmitProduced(t.Context(), "auth", "second", input); err != nil {
		t.Fatal(err)
	}
	first, err := f.service.claimMail(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.service.claimMail(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.beginMailSubmission(t.Context(), first, f.domain.Selector); err != nil {
		t.Fatal(err)
	}
	if err := f.service.beginMailSubmission(t.Context(), second, f.domain.Selector); !errors.Is(err, ErrProducerThrottled) {
		t.Fatal("racing claim escaped producer limit", err)
	}
	if err := f.store.Settle(t.Context(), second, "queued", producerThrottleCode, time.Now()); err != nil {
		t.Fatal(err)
	}
	var pacing int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM internal_mail_domain_pacing").Scan(&pacing); err != nil {
		t.Fatal(err)
	}
	if pacing != 1 || dispatchRows(t, f) != 1 {
		t.Fatal("throttled handoff consumed another pacing or dispatch slot")
	}
	var failures int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM internal_mail_events WHERE kind='failed'").Scan(&failures); err != nil || failures != 0 {
		t.Fatal("throttling created a failure event", err)
	}
}

func TestPGProducerSchedulingCurrentClockAfterBucketWait(t *testing.T) {
	f := schedulerFixture(t, 1)
	produce(t, f, "auth", "after-wait")
	r, err := f.service.claimMail(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(650 * time.Millisecond)
	if _, err := f.pool.Exec(t.Context(), "INSERT INTO internal_mail_dispatch_reservations(namespace,producer_id,lease_id,started_at) VALUES('platform','auth',$1,$2)", uuid.NewString(), deadline.Add(-api.InternalMailProducerDispatchWindow)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	lock, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback(ctx) }()
	if _, err := lock.Exec(ctx, "SELECT last_turn FROM internal_mail_producer_dispatch WHERE namespace='platform' AND producer_id='auth' FOR UPDATE"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- f.service.beginMailSubmission(ctx, r, f.domain.Selector) }()
	select {
	case err := <-done:
		t.Fatal("handoff did not wait on the producer bucket", err)
	case <-time.After(30 * time.Millisecond):
	}
	waitMailTime(t, deadline)
	if err := lock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil || dispatchRows(t, f) != 1 {
		t.Fatal("submission used a clock sampled before the lock wait", err)
	}
}

func TestPGProducerSchedulingPacingStartsAfterProducerLockWait(t *testing.T) {
	f := schedulerFixture(t, 1)
	f.service.PacingEnabled = func() bool { return true }
	f.service.PacingPolicy.Interval = api.InternalMailMinDomainPacingInterval
	produce(t, f, "auth", "pacing-after-wait")
	r, err := f.service.claimMail(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	lock, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback(ctx) }()
	if _, err := lock.Exec(ctx, "SELECT last_turn FROM internal_mail_producer_dispatch WHERE namespace='platform' AND producer_id='auth' FOR UPDATE"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- f.service.beginMailSubmission(ctx, r, f.domain.Selector) }()
	deadline := time.Now().Add(api.InternalMailMinDomainPacingInterval + 150*time.Millisecond)
	select {
	case err := <-done:
		t.Fatal("submission did not wait on the producer bucket", err)
	case <-time.After(30 * time.Millisecond):
	}
	waitMailTime(t, deadline)
	released := time.Now()
	if err := lock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	slot := pacingSlot(t, f, "platform", "example.net")
	if slot.Before(released.Add(api.InternalMailMinDomainPacingInterval)) {
		t.Fatal("producer lock wait aged the recipient-domain cooldown before custody", slot, released)
	}
}

func TestPGProducerSchedulingPreservesHoldsAndExpiryMaintenance(t *testing.T) {
	t.Run("manual hold", func(t *testing.T) {
		f := schedulerFixture(t, 1)
		produce(t, f, "auth", "hold")
		r, err := f.service.claimMail(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		controlChange(t, f, DeliveryScope{Kind: "namespace"}, "paused", 0)
		f.service.DeliveryControlsEnabled = func() bool { return false }
		if err := f.service.beginMailSubmission(t.Context(), r, f.domain.Selector); !errors.Is(err, ErrDeliveryPaused) {
			t.Fatal("fair scheduler bypassed a durable manual hold", err)
		}
		if err := f.store.Settle(t.Context(), r, "queued", "delivery_paused", time.Now()); err != nil {
			t.Fatal(err)
		}
		if _, err := f.service.claimMail(t.Context()); !errors.Is(err, ErrNotFound) || dispatchRows(t, f) != 0 {
			t.Fatal("held mail became eligible or spent capacity", err)
		}
	})
	t.Run("throttled expiry", func(t *testing.T) {
		f := schedulerFixture(t, 1)
		f.service.SchedulingEnabled = func() bool { return true }
		produce(t, f, "auth", "delivered")
		input := producerRequest()
		deadline := time.Now().Add(time.Hour)
		input.ExpiresAt = &deadline
		m, err := f.service.SubmitProduced(t.Context(), "auth", "expired", input)
		if err != nil {
			t.Fatal(err)
		}
		f.delivery.Store(true)
		if err := f.service.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_messages SET created_at=clock_timestamp()-interval '2 minutes',expires_at=clock_timestamp()-interval '1 minute' WHERE id=$1", m.ID); err != nil {
			t.Fatal(err)
		}
		if err := f.service.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
		after, _ := f.store.Message(t.Context(), m.ID)
		if after.Recipients[0].Status != "expired" || after.Recipients[0].Attempts != 0 || len(f.transport.envelopes) != 1 || dispatchRows(t, f) != 1 {
			t.Fatal("producer limit blocked expiry maintenance or changed custody")
		}
	})
}
