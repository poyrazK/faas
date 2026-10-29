package preview

import (
	"context"
	"database/sql"
)

// EventOutboxControlHistoryOptions uses a per-publication committed sequence.
// After is exclusive, zero starts at the beginning. Limit defaults to 50 (1..100).
type EventOutboxControlHistoryOptions struct {
	After int64
	Limit int
}
type EventOutboxControlHistoryPage struct {
	Items     []EventOutboxControlReceipt `json:"items"`
	NextAfter int64                       `json:"next_after,omitempty"`
}

// ListEventOutboxControlHistory returns retained command receipts for one owner
// and producer key, including failed observations and rejected controls. It does
// not require the original publication row to remain retained. Sequence counters
// must survive receipt pruning; deleting counters invalidates pagination ordering.
func ListEventOutboxControlHistory(ctx context.Context, db *sql.DB, accountID, appID, key string, options EventOutboxControlHistoryOptions) (EventOutboxControlHistoryPage, error) {
	var out EventOutboxControlHistoryPage
	if ctx == nil || db == nil || !eventOutboxKey(key) {
		return out, ErrInvalidEventConsumerRequest
	}
	account, app, err := eventIntentScope(accountID, appID)
	if err != nil {
		return out, err
	}
	if options.Limit == 0 {
		options.Limit = 50
	}
	if options.After < 0 || options.Limit < 1 || options.Limit > 100 {
		return out, ErrInvalidEventConsumerRequest
	}
	rows, err := db.QueryContext(ctx, `SELECT `+eventOutboxControlColumns+` FROM public.gregale_event_outbox_controls WHERE account_id=$1::uuid AND app_id=$2::uuid AND producer_key=$3 AND sequence>$4::bigint ORDER BY sequence LIMIT $5::integer`, account, app, key, options.After, options.Limit+1)
	if err != nil {
		return out, eventOutboxStoreError(ctx)
	}
	defer func() { _ = rows.Close() }()
	out.Items = make([]EventOutboxControlReceipt, 0, options.Limit)
	for rows.Next() {
		receipt, _, scanErr := eventOutboxControlReceipt(rows)
		if scanErr != nil {
			return EventOutboxControlHistoryPage{}, eventOutboxStoreError(ctx)
		}
		if len(out.Items) == options.Limit {
			out.NextAfter = out.Items[len(out.Items)-1].Sequence
			break
		}
		out.Items = append(out.Items, receipt)
	}
	if err := rows.Close(); err != nil {
		return EventOutboxControlHistoryPage{}, eventOutboxStoreError(ctx)
	}
	if rows.Err() != nil {
		return EventOutboxControlHistoryPage{}, eventOutboxStoreError(ctx)
	}
	return out, nil
}
