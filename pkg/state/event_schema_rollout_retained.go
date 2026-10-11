package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/eventcontract"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) eventSchemaRolloutRetained(ctx context.Context, account string, req api.EventSchemaRolloutRequest, cutoff time.Time) (eventSchemaRolloutObservation, error) {
	out := eventSchemaRolloutObservation{}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	candidates, err := q.EventSchemaRolloutCandidates(ctx, tx, sqlc.EventSchemaRolloutCandidatesParams{AccountID: mustPgUUID(account), FromAt: pgtypeFromTime(*req.From), CutoffAt: pgtypeFromTime(cutoff), ScanLimit: api.EventSchemaRolloutRetainedScanMax + 1})
	if err != nil {
		return out, err
	}
	if len(candidates) > api.EventSchemaRolloutRetainedScanMax {
		out.truncated = true
		candidates = candidates[:api.EventSchemaRolloutRetainedScanMax]
	}
	out.scanned = len(candidates)
	limit := req.RetainedLimit
	if limit == 0 {
		limit = api.EventSchemaRolloutRetainedMax
	}
	ids := []int64{}
	for _, row := range candidates {
		if row.Source == req.Source && row.EventType == req.Type {
			if len(ids) == limit {
				out.truncated = true
				break
			}
			ids = append(ids, row.ID)
		}
	}
	if len(ids) == 0 {
		return out, tx.Commit(ctx)
	}
	rows, err := q.EventSchemaRolloutPayloads(ctx, tx, sqlc.EventSchemaRolloutPayloadsParams{AccountID: mustPgUUID(account), Ids: ids, PayloadLimit: api.EventSchemaRolloutSampleMaxBytes + 1})
	if err != nil {
		return out, err
	}
	for _, row := range rows {
		out.rows = append(out.rows, eventSchemaRolloutRow{id: row.EventID, accepted: timeFromPgtype(row.CreatedAt), payload: []byte(row.Payload)})
	}
	return out, tx.Commit(ctx)
}
func (m *MemStore) eventSchemaRolloutRetained(ctx context.Context, account string, req api.EventSchemaRolloutRequest, cutoff time.Time) (eventSchemaRolloutObservation, error) {
	if err := ctx.Err(); err != nil {
		return eventSchemaRolloutObservation{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	candidates := []*PublishedEventWork{}
	for _, row := range m.eventFanout {
		if row.CreatedAt.Before(*req.From) || !row.CreatedAt.Before(cutoff) || !sameMemUUID(eventRoutingAccount(row), account) {
			continue
		}
		candidates = append(candidates, row)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].CreatedAt.Equal(candidates[j].CreatedAt) {
			return candidates[i].ID > candidates[j].ID
		}
		return candidates[i].CreatedAt.After(candidates[j].CreatedAt)
	})
	out := eventSchemaRolloutObservation{}
	if len(candidates) > api.EventSchemaRolloutRetainedScanMax {
		out.truncated = true
		candidates = candidates[:api.EventSchemaRolloutRetainedScanMax]
	}
	out.scanned = len(candidates)
	limit := req.RetainedLimit
	if limit == 0 {
		limit = api.EventSchemaRolloutRetainedMax
	}
	for _, row := range candidates {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		var e eventcontract.Envelope
		if json.Unmarshal(row.Payload, &e) != nil || e.Source != req.Source || e.Type != req.Type {
			continue
		}
		if len(out.rows) == limit {
			out.truncated = true
			break
		}
		payload := row.Payload
		if len(payload) > api.EventSchemaRolloutSampleMaxBytes {
			payload = payload[:api.EventSchemaRolloutSampleMaxBytes+1]
		}
		out.rows = append(out.rows, eventSchemaRolloutRow{id: e.ID, accepted: row.CreatedAt, payload: append([]byte(nil), payload...)})
	}
	return out, nil
}
