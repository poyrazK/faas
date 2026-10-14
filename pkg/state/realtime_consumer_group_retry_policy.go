package state

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"math/big"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type RealtimeConsumerGroupRetryPolicy struct {
	Enabled               bool     `json:"enabled"`
	BaseDelaySeconds      int      `json:"base_delay_seconds"`
	MaxDelaySeconds       int      `json:"max_delay_seconds"`
	Jitter                bool     `json:"jitter"`
	PermanentFailureCodes []string `json:"permanent_failure_codes"`
}
type RealtimeConsumerGroupRetryStatus struct {
	RealtimeConsumerGroupRetryPolicy
	NextRetryAt *time.Time `json:"next_retry_at"`
}
type RealtimeConsumerGroupRetryPolicyStore interface {
	ReadRealtimeConsumerGroupRetryPolicy(context.Context, string, string, string) (RealtimeConsumerGroupRetryStatus, error)
	SetRealtimeConsumerGroupRetryPolicy(context.Context, string, string, string, RealtimeConsumerGroupRetryPolicy) (RealtimeConsumerGroupRetryStatus, error)
}

func validateRealtimeRetryPolicy(p RealtimeConsumerGroupRetryPolicy) error {
	if p.BaseDelaySeconds < 1 || p.MaxDelaySeconds < p.BaseDelaySeconds || p.MaxDelaySeconds > api.RealtimeConsumerGroupRetryMaxSeconds || len(p.PermanentFailureCodes) > api.RealtimeConsumerGroupPermanentFailureCodesMax {
		return ErrRealtimeConsumerGroupInvalid
	}
	seen := map[string]bool{}
	for _, code := range p.PermanentFailureCodes {
		if len(code) == 0 || len(code) > api.RealtimeConsumerGroupFailureMaxBytes || seen[code] {
			return ErrRealtimeConsumerGroupInvalid
		}
		for _, c := range code {
			if c < 32 || c > 126 {
				return ErrRealtimeConsumerGroupInvalid
			}
		}
		seen[code] = true
	}
	return nil
}
func realtimeRetryDecision(p RealtimeConsumerGroupRetryPolicy, attempt int, code string, override *int) (int, bool, error) {
	if p.Enabled {
		for _, c := range p.PermanentFailureCodes {
			if c == code {
				return 0, true, nil
			}
		}
	}
	if override != nil {
		return *override, false, nil
	}
	if !p.Enabled {
		return 0, false, nil
	}
	delay := p.BaseDelaySeconds
	for i := 1; i < attempt && delay < p.MaxDelaySeconds; i++ {
		delay *= 2
		if delay > p.MaxDelaySeconds {
			delay = p.MaxDelaySeconds
		}
	}
	if p.Jitter {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(delay-delay/2+1)))
		if err != nil {
			return 0, false, err
		}
		delay = delay/2 + int(n.Int64())
	}
	return delay, false, nil
}
func (s *PgStore) ReadRealtimeConsumerGroupRetryPolicy(ctx context.Context, ep, ch, name string) (RealtimeConsumerGroupRetryStatus, error) {
	tx, g, err := s.lockRealtimeGroup(ctx, ep, ch, name)
	if err != nil {
		return RealtimeConsumerGroupRetryStatus{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result := RealtimeConsumerGroupRetryStatus{RealtimeConsumerGroupRetryPolicy: g.retryPolicy}
	err = tx.QueryRow(ctx, `select min(available_at) from managed_realtime_consumer_group_messages where group_id=$1 and status='ready' and available_at>clock_timestamp()`, g.id).Scan(&result.NextRetryAt)
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}
func (s *PgStore) SetRealtimeConsumerGroupRetryPolicy(ctx context.Context, ep, ch, name string, p RealtimeConsumerGroupRetryPolicy) (RealtimeConsumerGroupRetryStatus, error) {
	if _, ok := realtimeWorkerFromContext(ctx); ok {
		return RealtimeConsumerGroupRetryStatus{}, ErrRealtimeWorkerCredentialDenied
	}
	if err := validateRealtimeRetryPolicy(p); err != nil {
		return RealtimeConsumerGroupRetryStatus{}, err
	}
	if p.PermanentFailureCodes == nil {
		p.PermanentFailureCodes = []string{}
	}
	tx, g, err := s.lockRealtimeGroup(ctx, ep, ch, name)
	if err != nil {
		return RealtimeConsumerGroupRetryStatus{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	data, err := json.Marshal(p)
	if err != nil {
		return RealtimeConsumerGroupRetryStatus{}, err
	}
	_, err = tx.Exec(ctx, `update managed_realtime_consumer_groups set retry_policy=$2::jsonb where id=$1`, g.id, data)
	if err != nil {
		return RealtimeConsumerGroupRetryStatus{}, err
	}
	result := RealtimeConsumerGroupRetryStatus{RealtimeConsumerGroupRetryPolicy: p}
	err = tx.QueryRow(ctx, `select min(available_at) from managed_realtime_consumer_group_messages where group_id=$1 and status='ready' and available_at>clock_timestamp()`, g.id).Scan(&result.NextRetryAt)
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}
