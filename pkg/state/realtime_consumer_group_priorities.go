package state

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	prioritysqlc "github.com/onebox-faas/faas/pkg/state/realtimebulk/sqlc"
)

type RealtimeConsumerGroupPriorityBacklog struct {
	Priority int `json:"priority"`
	Ready    int `json:"ready"`
	Leased   int `json:"leased"`
	Dead     int `json:"dead"`
}
type RealtimeConsumerGroupPriorityStatus struct {
	MetadataKey     string                                 `json:"metadata_key"`
	FairTurns       int                                    `json:"fair_turns"`
	NextClaimOldest bool                                   `json:"next_claim_oldest"`
	Backlog         []RealtimeConsumerGroupPriorityBacklog `json:"backlog"`
}
type RealtimeConsumerGroupPriorityStore interface {
	ReadRealtimeConsumerGroupPriority(context.Context, string, string, string) (RealtimeConsumerGroupPriorityStatus, error)
}

func (s *PgStore) ReadRealtimeConsumerGroupPriority(ctx context.Context, ep, ch, name string) (RealtimeConsumerGroupPriorityStatus, error) {
	result := RealtimeConsumerGroupPriorityStatus{FairTurns: api.RealtimeConsumerGroupPriorityFairTurns, Backlog: []RealtimeConsumerGroupPriorityBacklog{}}
	if _, ok := realtimeWorkerFromContext(ctx); ok {
		return result, ErrRealtimeWorkerCredentialDenied
	}
	tx, g, err := s.lockRealtimeGroup(ctx, ep, ch, name)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result.MetadataKey = g.priorityField
	result.NextClaimOldest = g.priorityField == "" || g.priorityTurn == api.RealtimeConsumerGroupPriorityFairTurns-1
	rows, err := prioritysqlc.New().ReadPriorityBacklog(ctx, tx, g.id)
	if err != nil {
		return result, err
	}
	for _, row := range rows {
		result.Backlog = append(result.Backlog, RealtimeConsumerGroupPriorityBacklog{Priority: int(row.Priority), Ready: int(row.Ready), Leased: int(row.Leased), Dead: int(row.Dead)})
	}
	return result, tx.Commit(ctx)
}
func freezeRealtimeGroupPriority(ctx context.Context, tx pgx.Tx, g realtimeGroupRow, seq int64, metadata map[string]string) error {
	value := metadata[g.priorityField]
	priority := 0
	if len(value) == 1 && value[0] >= '0' && value[0] <= '0'+api.RealtimeConsumerGroupPriorityMax {
		priority = int(value[0] - '0')
	}
	return prioritysqlc.New().FreezeMessagePriority(ctx, tx, prioritysqlc.FreezeMessagePriorityParams{GroupID: g.id, Sequence: seq, Priority: int32(priority)})
}
func prefillRealtimePriorityWindow(ctx context.Context, tx pgx.Tx, g *realtimeGroupRow, ep, ch string, total, scanned *int) error {
	for *total < api.RealtimeConsumerGroupOutstandingMax && *scanned < api.RealtimeConsumerGroupFilterScanMax {
		seq, assigned, err := assignRealtimeGroupMessage(ctx, tx, *g, ep, ch)
		*scanned++
		// Already materialized work remains claimable while source history requires resync.
		var deduplicationFull *RealtimeConsumerGroupDeduplicationFullError
		if errors.Is(err, ErrRealtimeConsumerGroupGap) || errors.As(err, &deduplicationFull) {
			return nil
		}
		if err != nil {
			return err
		}
		if seq == 0 {
			return nil
		}
		if assigned {
			*total++
		}
		g.assigned = seq
	}
	return nil
}
