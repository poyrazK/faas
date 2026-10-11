package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	metricsqlc "github.com/onebox-faas/faas/pkg/state/realtimebulk/sqlc"
)

type RealtimeConsumerGroupMetricBucket struct {
	BucketAt               time.Time `json:"bucket_at"`
	FirstClaims            string    `json:"first_claims"`
	Completed              string    `json:"completed"`
	Retries                string    `json:"retries"`
	DeadLetters            string    `json:"dead_letters"`
	QueueWaitMilliseconds  float64   `json:"queue_wait_milliseconds"`
	ProcessingMilliseconds float64   `json:"processing_milliseconds"`
}
type RealtimeConsumerGroupMetrics struct {
	BucketSeconds int                                 `json:"bucket_seconds"`
	Buckets       []RealtimeConsumerGroupMetricBucket `json:"buckets"`
}
type RealtimeConsumerGroupMetricsStore interface {
	ReadRealtimeConsumerGroupMetrics(context.Context, string, string, string) (RealtimeConsumerGroupMetrics, error)
}

func (s *PgStore) ReadRealtimeConsumerGroupMetrics(ctx context.Context, ep, ch, name string) (RealtimeConsumerGroupMetrics, error) {
	result := RealtimeConsumerGroupMetrics{BucketSeconds: 60, Buckets: []RealtimeConsumerGroupMetricBucket{}}
	if _, ok := realtimeWorkerFromContext(ctx); ok {
		return result, ErrRealtimeWorkerCredentialDenied
	}
	tx, g, err := s.lockRealtimeGroup(ctx, ep, ch, name)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := metricsqlc.New().ReadGroupMetricBuckets(ctx, tx, metricsqlc.ReadGroupMetricBucketsParams{GroupID: g.id, BucketCount: api.RealtimeConsumerGroupMetricBuckets})
	if err != nil {
		return result, err
	}
	for _, row := range rows {
		result.Buckets = append(result.Buckets, RealtimeConsumerGroupMetricBucket{BucketAt: row.BucketAt.Time, FirstClaims: row.FirstClaims, Completed: row.Completed, Retries: row.Retries, DeadLetters: row.DeadLetters, QueueWaitMilliseconds: row.QueueWaitMilliseconds, ProcessingMilliseconds: row.ProcessingMilliseconds})
	}
	return result, tx.Commit(ctx)
}
