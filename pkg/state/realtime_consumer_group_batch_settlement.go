package state

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	settlementsqlc "github.com/onebox-faas/faas/pkg/state/realtimebulk/sqlc"
)

type RealtimeConsumerGroupBatchSettlementRequest struct {
	IdempotencyKey string                            `json:"idempotency_key,omitempty"`
	Settlements    []RealtimeConsumerGroupSettlement `json:"settlements"`
}
type RealtimeConsumerGroupBatchSettlementItem struct {
	Sequence string `json:"sequence"`
	Action   string `json:"action"`
	RealtimeConsumerGroupSettlementResult
}
type RealtimeConsumerGroupBatchSettlementResult struct {
	Results []RealtimeConsumerGroupBatchSettlementItem `json:"results"`
}
type RealtimeConsumerGroupBatchSettlementStore interface {
	SettleRealtimeConsumerGroupBatch(context.Context, string, string, string, RealtimeConsumerGroupBatchSettlementRequest) (RealtimeConsumerGroupBatchSettlementResult, error)
}
type realtimeBatchSettlementInput struct {
	request  RealtimeConsumerGroupSettlement
	sequence int64
	hash     []byte
}

func validateRealtimeBatchSettlements(req RealtimeConsumerGroupBatchSettlementRequest) ([]realtimeBatchSettlementInput, error) {
	if validateRealtimeSettlementKey(req.IdempotencyKey) != nil || len(req.Settlements) < 1 || len(req.Settlements) > api.RealtimeConsumerGroupSettlementBatchMax {
		return nil, ErrRealtimeConsumerGroupInvalid
	}
	seen := map[int64]bool{}
	inputs := make([]realtimeBatchSettlementInput, 0, len(req.Settlements))
	for _, item := range req.Settlements {
		seq, hash, err := validateRealtimeGroupSettlement(item)
		if err != nil || seen[seq] || item.IdempotencyKey != "" {
			return nil, ErrRealtimeConsumerGroupInvalid
		}
		seen[seq] = true
		inputs = append(inputs, realtimeBatchSettlementInput{item, seq, hash})
	}
	return inputs, nil
}
func authorizeRealtimeBatchSettlements(ctx context.Context, tx pgx.Tx, g realtimeGroupRow, inputs []realtimeBatchSettlementInput) error {
	worker, ok := realtimeWorkerFromContext(ctx)
	if !ok {
		return nil
	}
	if err := requireRealtimeWorkerOperation(ctx, inputs[0].request.Action); err != nil {
		return err
	}
	actions := []string{}
	seen := map[string]bool{}
	for _, input := range inputs {
		if !seen[input.request.Action] {
			actions = append(actions, input.request.Action)
			seen[input.request.Action] = true
		}
	}
	allowed, err := settlementsqlc.New().AuthorizeBatchSettlementActions(ctx, tx, settlementsqlc.AuthorizeBatchSettlementActionsParams{GroupID: g.id, CredentialID: worker.grant.Info.ID, TokenHash: worker.grant.hash, Actions: actions})
	if err != nil {
		return err
	}
	if !allowed {
		return ErrRealtimeWorkerCredentialDenied
	}
	return nil
}
func readRealtimeSettlementAttempts(ctx context.Context, tx pgx.Tx, id string, seq int64, hash []byte) (int, error) {
	credential := pgtype.Text{}
	if worker, ok := realtimeWorkerFromContext(ctx); ok {
		credential = pgtype.Text{String: worker.grant.Info.ID, Valid: true}
	}
	attempts, err := settlementsqlc.New().ReadSettlementLeaseAttempts(ctx, tx, settlementsqlc.ReadSettlementLeaseAttemptsParams{GroupID: id, Sequence: seq, TokenHash: hash, CredentialID: credential})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrRealtimeConsumerGroupLease
	}
	return int(attempts), err
}
func (s *PgStore) SettleRealtimeConsumerGroupBatch(ctx context.Context, ep, ch, name string, req RealtimeConsumerGroupBatchSettlementRequest) (RealtimeConsumerGroupBatchSettlementResult, error) {
	result := RealtimeConsumerGroupBatchSettlementResult{Results: []RealtimeConsumerGroupBatchSettlementItem{}}
	inputs, err := validateRealtimeBatchSettlements(req)
	if err != nil {
		return result, err
	}
	tx, g, err := s.lockRealtimeGroup(ctx, ep, ch, name)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = requireRealtimeGroupConsumer(ctx, g); err != nil {
		return result, err
	}
	if err = authorizeRealtimeBatchSettlements(ctx, tx, g, inputs); err != nil {
		return result, err
	}
	receipt, found, err := prepareRealtimeSettlementReceipt(ctx, tx, g.id, req.IdempotencyKey, "batch", inputs, &result)
	if err != nil {
		return result, err
	}
	if found {
		return result, tx.Commit(ctx)
	}
	for _, input := range inputs {
		item := RealtimeConsumerGroupBatchSettlementItem{Sequence: input.request.Sequence, Action: input.request.Action}
		attempts, err := readRealtimeSettlementAttempts(ctx, tx, g.id, input.sequence, input.hash)
		if err == nil {
			item.RealtimeConsumerGroupSettlementResult, err = settleRealtimeGroupMessage(ctx, tx, g, input.sequence, input.hash, attempts, input.request)
		}
		if errors.Is(err, ErrRealtimeConsumerGroupLease) {
			item.RealtimeConsumerGroupSettlementResult = RealtimeConsumerGroupSettlementResult{Status: "lease_lost"}
		} else if err != nil {
			return RealtimeConsumerGroupBatchSettlementResult{}, err
		}
		result.Results = append(result.Results, item)
	}
	if err = saveRealtimeSettlementReceipt(ctx, tx, receipt, result); err != nil {
		return RealtimeConsumerGroupBatchSettlementResult{}, err
	}
	return result, tx.Commit(ctx)
}
