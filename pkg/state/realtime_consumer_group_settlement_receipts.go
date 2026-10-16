package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	receiptsqlc "github.com/onebox-faas/faas/pkg/state/realtimebulk/sqlc"
)

var ErrRealtimeSettlementKeyConflict = errors.New("state: realtime settlement idempotency key reused")

type realtimeSettlementReceipt struct {
	group, principal, key string
	hash                  []byte
}

func validateRealtimeSettlementKey(key string) error {
	if key == "" {
		return nil
	}
	if len(key) != api.RealtimeConsumerGroupSettlementIdempotencyKeyBytes {
		return ErrRealtimeConsumerGroupInvalid
	}
	if _, err := uuid.Parse(key); err != nil {
		return ErrRealtimeConsumerGroupInvalid
	}
	return nil
}

// Fingerprint normalized validated content, never persist raw claim tokens.
func realtimeSettlementFingerprint(kind string, inputs []realtimeBatchSettlementInput) []byte {
	requests := make([]RealtimeConsumerGroupSettlement, len(inputs))
	for i, input := range inputs {
		request := input.request
		request.IdempotencyKey = ""
		request.Sequence = strconv.FormatInt(input.sequence, 10)
		request.ClaimToken = hex.EncodeToString(input.hash)
		requests[i] = request
	}
	body, _ := json.Marshal(struct {
		Kind     string
		Requests []RealtimeConsumerGroupSettlement
	}{kind, requests})
	hash := sha256.Sum256(body)
	return hash[:]
}

func prepareRealtimeSettlementReceipt(ctx context.Context, tx pgx.Tx, group, key, kind string, inputs []realtimeBatchSettlementInput, result any) (realtimeSettlementReceipt, bool, error) {
	receipt := realtimeSettlementReceipt{group: group, key: key}
	if key == "" {
		return receipt, false, nil
	}
	receipt.principal = "account"
	if worker, ok := realtimeWorkerFromContext(ctx); ok {
		receipt.principal = worker.grant.Info.ID
	}
	receipt.hash = realtimeSettlementFingerprint(kind, inputs)
	queries := receiptsqlc.New()
	if err := queries.PruneSettlementReceipts(ctx, tx, group); err != nil {
		return receipt, false, err
	}
	row, err := queries.ReadSettlementReceipt(ctx, tx, receiptsqlc.ReadSettlementReceiptParams{GroupID: group, Principal: receipt.principal, IdempotencyKey: key})
	if err == nil {
		if !bytes.Equal(row.RequestHash, receipt.hash) {
			return receipt, false, ErrRealtimeSettlementKeyConflict
		}
		err = json.Unmarshal(row.Result, result)
		return receipt, true, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return receipt, false, err
	}
	count, err := queries.CountSettlementReceipts(ctx, tx, group)
	if err == nil && count >= api.RealtimeConsumerGroupSettlementReceiptsMax {
		err = &RealtimeConsumerGroupCapacityError{Resource: "settlement receipts", Limit: api.RealtimeConsumerGroupSettlementReceiptsMax, Observed: int(count)}
	}
	return receipt, false, err
}

func saveRealtimeSettlementReceipt(ctx context.Context, tx pgx.Tx, receipt realtimeSettlementReceipt, result any) error {
	if receipt.key == "" {
		return nil
	}
	body, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return receiptsqlc.New().SaveSettlementReceipt(ctx, tx, receiptsqlc.SaveSettlementReceiptParams{
		GroupID: receipt.group, Principal: receipt.principal, IdempotencyKey: receipt.key, RequestHash: receipt.hash, Result: body,
		RetentionSeconds: api.RealtimeConsumerGroupSettlementReceiptRetentionSeconds,
	})
}
