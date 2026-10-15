package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type eventAcknowledgmentMetricKey struct {
	account, app, subscription, kind string
	bucket                           time.Time
}
type eventAcknowledgmentMetric struct {
	count       int64
	saturated   bool
	retainUntil time.Time
}

func acknowledgmentMetricKind(kind string) bool {
	return kind == "renewed" || kind == "renewal_rejected" || kind == "expired"
}
func acknowledgmentMetricWindow(now time.Time, window time.Duration) api.EventAcknowledgmentWindowHealth {
	start, end := api.EventAcknowledgmentWindowBounds(now, window, nil)
	return api.EventAcknowledgmentWindowHealth{WindowStart: start, WindowEnd: end, BucketSeconds: int(api.EventAcknowledgmentMetricBucket / time.Second), RetentionSeconds: int(api.EventAcknowledgmentMetricRetention / time.Second), Coverage: "recorded_full_minute_buckets"}
}
func finishAcknowledgmentMetricHealth(out *api.EventAcknowledgmentWindowHealth) {
	out.RenewalAttempts = out.RenewalSuccesses + out.RenewalRejections
	seconds := out.WindowEnd.Sub(out.WindowStart).Seconds()
	if out.Saturated || seconds <= 0 {
		return
	}
	if out.RenewalAttempts > 0 {
		fraction := float64(out.RenewalRejections) / float64(out.RenewalAttempts)
		out.RenewalRejectionFraction = &fraction
	}
	rate := float64(out.LeaseExpiries) / seconds
	out.LeaseExpiryRatePerSecond = &rate
}
func (s *PgStore) setAcknowledgmentMetricHealth(ctx context.Context, account, app, sub string, window time.Duration, health *api.EventConsumerProcessingHealth) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	coverage, err := s.acknowledgmentRecordingCoverage(ctx, tx)
	if err != nil {
		return err
	}
	out := acknowledgmentMetricWindow(health.ObservedAt, window)
	out.RecordingCoverage = &coverage
	out.WindowStart, out.WindowEnd = api.EventAcknowledgmentWindowBounds(health.ObservedAt, window, &coverage)
	row, err := sqlc.New().EventAckMetricHealth(ctx, tx, sqlc.EventAckMetricHealthParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub), WindowStart: pgtypeFromTime(out.WindowStart), WindowEnd: pgtypeFromTime(out.WindowEnd)})
	if err != nil {
		return err
	}
	out.RenewalSuccesses = row.RenewalSuccesses
	out.RenewalRejections = row.RenewalRejections
	out.LeaseExpiries = row.LeaseExpiries
	out.Saturated = row.Saturated
	finishAcknowledgmentMetricHealth(&out)
	health.RenewalWindow = &out
	return tx.Commit(ctx)
}
func (m *MemStore) recordAcknowledgmentMetricLocked(inv Invocation, kind string, now time.Time) {
	if !acknowledgmentMetricKind(kind) {
		return
	}
	var headers map[string]string
	if json.Unmarshal(inv.Headers, &headers) != nil {
		return
	}
	sub := headers["x-gregale-event-subscription-id"]
	if _, err := operationUUID(sub); err != nil {
		return
	}
	if m.eventAcknowledgmentMetrics == nil {
		m.eventAcknowledgmentMetrics = map[eventAcknowledgmentMetricKey]eventAcknowledgmentMetric{}
	}
	key := eventAcknowledgmentMetricKey{canonicalMemUUID(inv.AccountID), canonicalMemUUID(inv.AppID), canonicalMemUUID(sub), kind, now.UTC().Truncate(api.EventAcknowledgmentMetricBucket)}
	row := m.eventAcknowledgmentMetrics[key]
	if row.count >= api.EventAcknowledgmentMetricCountMax {
		row.saturated = true
	} else {
		row.count++
	}
	row.retainUntil = key.bucket.Add(api.EventAcknowledgmentMetricRetention)
	m.eventAcknowledgmentMetrics[key] = row
}
func (m *MemStore) pruneAcknowledgmentMetricsLocked(now time.Time, limit int) {
	count := 0
	for key, row := range m.eventAcknowledgmentMetrics {
		if !row.retainUntil.After(now) {
			delete(m.eventAcknowledgmentMetrics, key)
			count++
			if count >= limit {
				return
			}
		}
	}
}
func (m *MemStore) setAcknowledgmentMetricHealthLocked(account, app, sub string, window time.Duration, health *api.EventConsumerProcessingHealth) {
	coverage := bindAcknowledgmentCoverage(assessAcknowledgmentWriters(m.liveAcknowledgmentWritersLocked(health.ObservedAt), health.ObservedAt), m.eventAcknowledgmentCoverageHead, false)
	out := acknowledgmentMetricWindow(health.ObservedAt, window)
	out.RecordingCoverage = &coverage
	out.WindowStart, out.WindowEnd = api.EventAcknowledgmentWindowBounds(health.ObservedAt, window, &coverage)
	for key, row := range m.eventAcknowledgmentMetrics {
		if !sameMemUUID(key.account, account) || !sameMemUUID(key.app, app) || !sameMemUUID(key.subscription, sub) || key.bucket.Before(out.WindowStart) || !key.bucket.Before(out.WindowEnd) || !row.retainUntil.After(health.ObservedAt) {
			continue
		}
		out.Saturated = out.Saturated || row.saturated
		switch key.kind {
		case "renewed":
			out.RenewalSuccesses += row.count
		case "renewal_rejected":
			out.RenewalRejections += row.count
		case "expired":
			out.LeaseExpiries += row.count
		}
	}
	finishAcknowledgmentMetricHealth(&out)
	health.RenewalWindow = &out
}
