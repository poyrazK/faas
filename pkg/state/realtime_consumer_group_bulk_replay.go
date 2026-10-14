package state

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	bulksqlc "github.com/onebox-faas/faas/pkg/state/realtimebulk/sqlc"
)

type RealtimeDeadLetterPreviewRequest struct {
	FailureCodes    []string `json:"failure_codes,omitempty"`
	AfterSequence   string   `json:"after_sequence,omitempty"`
	ThroughSequence string   `json:"through_sequence,omitempty"`
	Limit           int      `json:"limit,omitempty"`
}
type RealtimeDeadLetterReplaySelection struct {
	Sequence  string    `json:"sequence"`
	DeadSince time.Time `json:"dead_since"`
}
type RealtimeDeadLetterPreviewItem struct {
	RealtimeDeadLetterReplaySelection
	Attempts        int        `json:"attempts"`
	FailureCode     string     `json:"failure_code"`
	OrderingBlocked bool       `json:"ordering_blocked"`
	ExpiresAt       *time.Time `json:"expires_at"`
}
type RealtimeDeadLetterPreview struct {
	GroupID string                          `json:"group_id"`
	Items   []RealtimeDeadLetterPreviewItem `json:"items"`
	HasMore bool                            `json:"has_more"`
}
type RealtimeDeadLetterBulkReplayRequest struct {
	GroupID string                              `json:"group_id"`
	Items   []RealtimeDeadLetterReplaySelection `json:"items"`
}
type RealtimeDeadLetterReplayOutcome struct {
	Sequence string `json:"sequence"`
	Status   string `json:"status"`
}
type RealtimeDeadLetterBulkReplayResult struct {
	GroupID  string                            `json:"group_id"`
	Replayed int                               `json:"replayed"`
	Outcomes []RealtimeDeadLetterReplayOutcome `json:"outcomes"`
}
type RealtimeConsumerGroupBulkReplayStore interface {
	PreviewRealtimeConsumerGroupDeadLetters(context.Context, string, string, string, RealtimeDeadLetterPreviewRequest) (RealtimeDeadLetterPreview, error)
	BulkReplayRealtimeConsumerGroupDeadLetters(context.Context, string, string, string, RealtimeDeadLetterBulkReplayRequest) (RealtimeDeadLetterBulkReplayResult, error)
}

func validateRealtimeDeadPreview(req *RealtimeDeadLetterPreviewRequest) (int64, int64, error) {
	if req.Limit == 0 {
		req.Limit = api.RealtimeConsumerGroupBulkReplayMax
	}
	if req.Limit < 1 || req.Limit > api.RealtimeConsumerGroupBulkReplayMax || len(req.FailureCodes) > api.RealtimeConsumerGroupPermanentFailureCodesMax {
		return 0, 0, ErrRealtimeConsumerGroupInvalid
	}
	if req.AfterSequence == "" {
		req.AfterSequence = "0"
	}
	after, err := realtimeGroupSequence(req.AfterSequence)
	if err != nil {
		return 0, 0, err
	}
	through := int64(0)
	if req.ThroughSequence != "" {
		through, err = realtimeGroupSequence(req.ThroughSequence)
		if err != nil || through <= after {
			return 0, 0, ErrRealtimeConsumerGroupInvalid
		}
	}
	// Reuse exact diagnostic-code bounds and uniqueness rules.
	err = validateRealtimeRetryPolicy(RealtimeConsumerGroupRetryPolicy{BaseDelaySeconds: 1, MaxDelaySeconds: 1, PermanentFailureCodes: req.FailureCodes})
	return after, through, err
}
func (s *PgStore) PreviewRealtimeConsumerGroupDeadLetters(ctx context.Context, ep, ch, name string, req RealtimeDeadLetterPreviewRequest) (RealtimeDeadLetterPreview, error) {
	result := RealtimeDeadLetterPreview{Items: []RealtimeDeadLetterPreviewItem{}}
	if err := requireRealtimeWorkerOperation(ctx, "dead_letters"); err != nil {
		return result, err
	}
	after, through, err := validateRealtimeDeadPreview(&req)
	if err != nil {
		return result, err
	}
	tx, g, err := s.lockRealtimeGroup(ctx, ep, ch, name)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	codes := req.FailureCodes
	if codes == nil {
		codes = []string{}
	}
	queries := bulksqlc.New()
	rows, err := queries.PreviewDeadLetters(ctx, tx, bulksqlc.PreviewDeadLettersParams{GroupID: g.id, AfterSequence: after, ThroughSequence: through, FailureCodes: codes, RowLimit: int32(req.Limit + 1), RetentionSeconds: int32(g.retentionSeconds), Ordered: g.orderingField != ""})
	if err != nil {
		return result, err
	}
	for _, row := range rows {
		result.Items = append(result.Items, RealtimeDeadLetterPreviewItem{RealtimeDeadLetterReplaySelection: RealtimeDeadLetterReplaySelection{Sequence: row.Sequence, DeadSince: row.DeadSince.Time.UTC()}, Attempts: int(row.Attempts), FailureCode: row.FailureCode, OrderingBlocked: row.OrderingBlocked, ExpiresAt: timestamptzToTimePtr(row.ExpiresAt)})
	}
	result.GroupID = g.id
	result.HasMore = len(result.Items) > req.Limit
	if result.HasMore {
		result.Items = result.Items[:req.Limit]
	}
	return result, tx.Commit(ctx)
}
func validateRealtimeDeadReplay(req RealtimeDeadLetterBulkReplayRequest) ([]int64, error) {
	if req.GroupID == "" || len(req.Items) == 0 || len(req.Items) > api.RealtimeConsumerGroupBulkReplayMax {
		return nil, ErrRealtimeConsumerGroupInvalid
	}
	sequences := make([]int64, 0, len(req.Items))
	seen := map[int64]bool{}
	for _, item := range req.Items {
		seq, err := realtimeGroupSequence(item.Sequence)
		if err != nil || seq == 0 || item.DeadSince.IsZero() || seen[seq] {
			return nil, ErrRealtimeConsumerGroupInvalid
		}
		seen[seq] = true
		sequences = append(sequences, seq)
	}
	return sequences, nil
}
func (s *PgStore) BulkReplayRealtimeConsumerGroupDeadLetters(ctx context.Context, ep, ch, name string, req RealtimeDeadLetterBulkReplayRequest) (RealtimeDeadLetterBulkReplayResult, error) {
	result := RealtimeDeadLetterBulkReplayResult{Outcomes: []RealtimeDeadLetterReplayOutcome{}}
	if err := requireRealtimeWorkerOperation(ctx, "replay"); err != nil {
		return result, err
	}
	sequences, err := validateRealtimeDeadReplay(req)
	if err != nil {
		return result, err
	}
	tx, g, err := s.lockRealtimeGroup(ctx, ep, ch, name)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if req.GroupID != g.id {
		return result, ErrConflict
	}
	if err = expireRealtimeGroupLeases(ctx, tx, g); err != nil {
		return result, err
	}
	result.GroupID = g.id
	for i, seq := range sequences {
		outcome := RealtimeDeadLetterReplayOutcome{Sequence: strconv.FormatInt(seq, 10)}
		status, err := replaySelectedRealtimeDeadLetter(ctx, tx, g, seq, req.Items[i].DeadSince)
		if err != nil {
			return RealtimeDeadLetterBulkReplayResult{}, err
		}
		outcome.Status = status
		if status == "replayed" {
			result.Replayed++
		}
		result.Outcomes = append(result.Outcomes, outcome)
	}
	return result, tx.Commit(ctx)
}
func inspectSelectedRealtimeDeadLetter(ctx context.Context, tx pgx.Tx, g realtimeGroupRow, seq int64, expected time.Time) (string, error) {
	queries := bulksqlc.New()
	row, err := queries.ReadReplaySelection(ctx, tx, bulksqlc.ReadReplaySelectionParams{GroupID: g.id, Sequence: seq})
	if errors.Is(err, pgx.ErrNoRows) {
		return "missing", nil
	}
	if err != nil {
		return "", err
	}
	if row.Status != "dead" {
		return "not_dead", nil
	}
	if !row.DeadSince.Valid || !row.DeadSince.Time.Equal(expected) {
		return "stale", nil
	}
	if row.DeadlineInvalid {
		return "invalid_deadline", nil
	}
	if row.DeadlineExpired {
		return "deadline_expired", nil
	}
	if g.orderingField != "" {
		active, err := queries.HasActiveOrderingLease(ctx, tx, bulksqlc.HasActiveOrderingLeaseParams{GroupID: g.id, OrderingKey: row.OrderingKey})
		if err != nil {
			return "", err
		}
		if active {
			return "ordering_blocked", nil
		}
	}
	return "eligible", nil
}

func replaySelectedRealtimeDeadLetter(ctx context.Context, tx pgx.Tx, g realtimeGroupRow, seq int64, expected time.Time) (string, error) {
	status, err := inspectSelectedRealtimeDeadLetter(ctx, tx, g, seq, expected)
	if err != nil || status != "eligible" {
		return status, err
	}
	err = bulksqlc.New().ReplayDeadLetter(ctx, tx, bulksqlc.ReplayDeadLetterParams{GroupID: g.id, Sequence: seq})
	return "replayed", err
}
