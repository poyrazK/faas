package state

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	circuitsqlc "github.com/onebox-faas/faas/pkg/state/realtimebulk/sqlc"
	"strconv"
	"time"
)

type RealtimeConsumerGroupCircuitPolicy struct {
	Enabled          bool `json:"enabled"`
	FailureThreshold int  `json:"failure_threshold"`
	CooldownSeconds  int  `json:"cooldown_seconds"`
}
type RealtimeConsumerGroupCircuitStatus struct {
	RealtimeConsumerGroupCircuitPolicy
	State                  string     `json:"state"`
	Reason                 string     `json:"reason"`
	ConsecutiveFailures    int        `json:"consecutive_failures"`
	NextProbeAt            *time.Time `json:"next_probe_at"`
	ProbeSequence          *string    `json:"probe_sequence"`
	Blocked                bool       `json:"blocked"`
	RetryAfterMilliseconds int64      `json:"retry_after_milliseconds"`
}
type RealtimeConsumerGroupCircuitStore interface {
	ReadRealtimeConsumerGroupCircuit(context.Context, string, string, string) (RealtimeConsumerGroupCircuitStatus, error)
	SetRealtimeConsumerGroupCircuit(context.Context, string, string, string, RealtimeConsumerGroupCircuitPolicy) (RealtimeConsumerGroupCircuitStatus, error)
}
type RealtimeConsumerGroupCircuitOpenError struct{ RetryAfterMilliseconds int64 }

func (e *RealtimeConsumerGroupCircuitOpenError) Error() string {
	return "state: realtime consumer group circuit open"
}
func readRealtimeCircuit(ctx context.Context, tx pgx.Tx, id string) (RealtimeConsumerGroupCircuitStatus, error) {
	row, err := circuitsqlc.New().ReadCircuitStatus(ctx, tx, circuitsqlc.ReadCircuitStatusParams{GroupID: id, ProbePollMilliseconds: api.RealtimeConsumerGroupCircuitProbePollMilliseconds})
	result := RealtimeConsumerGroupCircuitStatus{RealtimeConsumerGroupCircuitPolicy: RealtimeConsumerGroupCircuitPolicy{Enabled: row.CircuitEnabled, FailureThreshold: int(row.CircuitThreshold), CooldownSeconds: int(row.CircuitCooldownSeconds)}, State: row.CircuitState, Reason: row.CircuitReason, ConsecutiveFailures: int(row.CircuitFailures), Blocked: row.Blocked, RetryAfterMilliseconds: row.RetryAfterMilliseconds}
	if row.CircuitOpenUntil.Valid {
		t := row.CircuitOpenUntil.Time.UTC()
		result.NextProbeAt = &t
	}
	if row.CircuitProbeSequence.Valid {
		seq := strconv.FormatInt(row.CircuitProbeSequence.Int64, 10)
		result.ProbeSequence = &seq
	}
	return result, err
}
func gateRealtimeCircuit(ctx context.Context, tx pgx.Tx, g realtimeGroupRow) (RealtimeConsumerGroupCircuitStatus, error) {
	if !g.circuitEnabled {
		return RealtimeConsumerGroupCircuitStatus{}, nil
	}
	if err := circuitsqlc.New().RefreshCircuitGate(ctx, tx, g.id); err != nil {
		return RealtimeConsumerGroupCircuitStatus{}, err
	}
	return readRealtimeCircuit(ctx, tx, g.id)
}
func (s *PgStore) ReadRealtimeConsumerGroupCircuit(ctx context.Context, ep, ch, name string) (RealtimeConsumerGroupCircuitStatus, error) {
	if _, ok := realtimeWorkerFromContext(ctx); ok {
		return RealtimeConsumerGroupCircuitStatus{}, ErrRealtimeWorkerCredentialDenied
	}
	tx, g, err := s.lockRealtimeGroup(ctx, ep, ch, name)
	if err != nil {
		return RealtimeConsumerGroupCircuitStatus{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := readRealtimeCircuit(ctx, tx, g.id)
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}
func (s *PgStore) SetRealtimeConsumerGroupCircuit(ctx context.Context, ep, ch, name string, p RealtimeConsumerGroupCircuitPolicy) (RealtimeConsumerGroupCircuitStatus, error) {
	if _, ok := realtimeWorkerFromContext(ctx); ok {
		return RealtimeConsumerGroupCircuitStatus{}, ErrRealtimeWorkerCredentialDenied
	}
	if p.FailureThreshold < 1 || p.FailureThreshold > api.RealtimeConsumerGroupCircuitThresholdMax || p.CooldownSeconds < 1 || p.CooldownSeconds > api.RealtimeConsumerGroupCircuitCooldownMaxSeconds {
		return RealtimeConsumerGroupCircuitStatus{}, ErrRealtimeConsumerGroupInvalid
	}
	tx, g, err := s.lockRealtimeGroup(ctx, ep, ch, name)
	if err != nil {
		return RealtimeConsumerGroupCircuitStatus{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	err = circuitsqlc.New().ConfigureCircuitPolicy(ctx, tx, circuitsqlc.ConfigureCircuitPolicyParams{GroupID: g.id, Enabled: p.Enabled, Threshold: int32(p.FailureThreshold), CooldownSeconds: int32(p.CooldownSeconds)})
	if err != nil {
		return RealtimeConsumerGroupCircuitStatus{}, err
	}
	result, err := readRealtimeCircuit(ctx, tx, g.id)
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}
