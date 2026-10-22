package state

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	ratesqlc "github.com/onebox-faas/faas/pkg/state/realtimebulk/sqlc"
)

type RealtimeConsumerGroupClaimRatePolicy struct {
	RatePerSecond int `json:"rate_per_second"`
	Burst         int `json:"burst"`
}
type RealtimeConsumerGroupClaimRateStatus struct {
	RealtimeConsumerGroupClaimRatePolicy
	ThrottledClaimRequests string `json:"throttled_claim_requests"`
	RetryAfterMilliseconds int64  `json:"retry_after_milliseconds"`
}
type RealtimeConsumerGroupClaimRateStore interface {
	ReadRealtimeConsumerGroupClaimRate(context.Context, string, string, string) (RealtimeConsumerGroupClaimRateStatus, error)
	SetRealtimeConsumerGroupClaimRate(context.Context, string, string, string, RealtimeConsumerGroupClaimRatePolicy) (RealtimeConsumerGroupClaimRateStatus, error)
}
type RealtimeConsumerGroupRateLimitError struct{ RetryAfterMilliseconds int64 }

func (e *RealtimeConsumerGroupRateLimitError) Error() string {
	return "state: realtime consumer group claim rate reached"
}
func readRealtimeClaimRate(ctx context.Context, tx pgx.Tx, id string) (RealtimeConsumerGroupClaimRateStatus, error) {
	row, err := ratesqlc.New().ReadClaimRate(ctx, tx, id)
	return RealtimeConsumerGroupClaimRateStatus{RealtimeConsumerGroupClaimRatePolicy: RealtimeConsumerGroupClaimRatePolicy{RatePerSecond: int(row.ClaimRatePerSecond), Burst: int(row.ClaimRateBurst)}, ThrottledClaimRequests: row.ThrottledClaimRequests, RetryAfterMilliseconds: row.RetryAfterMilliseconds}, err
}
func gateRealtimeClaimRate(ctx context.Context, tx pgx.Tx, id string) (int64, error) {
	status, err := readRealtimeClaimRate(ctx, tx, id)
	if err != nil {
		return 0, err
	}
	if status.RetryAfterMilliseconds > 0 {
		err = ratesqlc.New().CountClaimThrottle(ctx, tx, id)
	}
	return status.RetryAfterMilliseconds, err
}
func advanceRealtimeClaimRate(ctx context.Context, tx pgx.Tx, id string) error {
	return ratesqlc.New().AdvanceClaimRate(ctx, tx, id)
}
func (s *PgStore) ReadRealtimeConsumerGroupClaimRate(ctx context.Context, ep, ch, name string) (RealtimeConsumerGroupClaimRateStatus, error) {
	tx, g, err := s.lockRealtimeGroup(ctx, ep, ch, name)
	if err != nil {
		return RealtimeConsumerGroupClaimRateStatus{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := readRealtimeClaimRate(ctx, tx, g.id)
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}
func (s *PgStore) SetRealtimeConsumerGroupClaimRate(ctx context.Context, ep, ch, name string, p RealtimeConsumerGroupClaimRatePolicy) (RealtimeConsumerGroupClaimRateStatus, error) {
	if _, ok := realtimeWorkerFromContext(ctx); ok {
		return RealtimeConsumerGroupClaimRateStatus{}, ErrRealtimeWorkerCredentialDenied
	}
	if p.RatePerSecond < 0 || p.RatePerSecond > api.RealtimeConsumerGroupClaimRateMax || p.Burst < 1 || p.Burst > api.RealtimeConsumerGroupClaimBurstMax {
		return RealtimeConsumerGroupClaimRateStatus{}, ErrRealtimeConsumerGroupInvalid
	}
	tx, g, err := s.lockRealtimeGroup(ctx, ep, ch, name)
	if err != nil {
		return RealtimeConsumerGroupClaimRateStatus{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	err = ratesqlc.New().ConfigureClaimRate(ctx, tx, ratesqlc.ConfigureClaimRateParams{GroupID: g.id, Rate: int32(p.RatePerSecond), Burst: int32(p.Burst)})
	if err != nil {
		return RealtimeConsumerGroupClaimRateStatus{}, err
	}
	result, err := readRealtimeClaimRate(ctx, tx, g.id)
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}
