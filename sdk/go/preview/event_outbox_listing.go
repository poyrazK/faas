package preview

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
)

// EventOutboxListOptions selects blocked/unconfirmed rows in producer-key order.
// Empty State includes both. After is the opaque cursor returned by the last page.
type EventOutboxListOptions struct {
	State string
	After string
	Limit int
}
type EventOutboxPage struct {
	Items     []EventOutboxStatus `json:"items"`
	NextAfter string              `json:"next_after,omitempty"`
}
type eventOutboxCursor struct {
	Version int    `json:"v"`
	Account string `json:"account"`
	App     string `json:"app"`
	State   string `json:"state"`
	Key     string `json:"key"`
}

// ListEventOutbox returns at most 100 payload-free rows. Cursors bind owner and
// filter, not a historical snapshot or authorization boundary. Concurrent state
// changes may omit rows from later pages; periodically restart from the first page.
func ListEventOutbox(ctx context.Context, db *sql.DB, accountID, appID string, options EventOutboxListOptions) (EventOutboxPage, error) {
	var out EventOutboxPage
	if ctx == nil || db == nil {
		return out, ErrInvalidEventConsumerRequest
	}
	account, app, err := eventIntentScope(accountID, appID)
	if err != nil {
		return out, err
	}
	if options.Limit == 0 {
		options.Limit = 50
	}
	if options.Limit < 1 || options.Limit > 100 || options.State != "" && options.State != "blocked" && options.State != "unconfirmed" || len(options.After) > 2048 {
		return out, ErrInvalidEventConsumerRequest
	}
	after := ""
	if options.After != "" {
		data, decodeErr := base64.RawURLEncoding.DecodeString(options.After)
		var cursor eventOutboxCursor
		if decodeErr != nil || json.Unmarshal(data, &cursor) != nil || cursor.Version != 1 || cursor.Account != account || cursor.App != app || cursor.State != options.State || !eventOutboxKey(cursor.Key) {
			return out, ErrInvalidEventConsumerRequest
		}
		after = cursor.Key
	}
	rows, err := db.QueryContext(ctx, `SELECT `+eventOutboxStatusColumns+` FROM public.gregale_event_outbox WHERE account_id=$1::uuid AND app_id=$2::uuid AND state IN ('blocked','unconfirmed') AND ($3::text='' OR state=$3::text) AND producer_key COLLATE "C">$4::text COLLATE "C" ORDER BY producer_key COLLATE "C" LIMIT $5::integer`, account, app, options.State, after, options.Limit+1)
	if err != nil {
		return out, eventOutboxStoreError(ctx)
	}
	defer func() { _ = rows.Close() }()
	out.Items = make([]EventOutboxStatus, 0, options.Limit)
	for rows.Next() {
		status, scanErr := eventOutboxStatus(rows)
		if scanErr != nil {
			return EventOutboxPage{}, eventOutboxStoreError(ctx)
		}
		if len(out.Items) == options.Limit {
			data, marshalErr := json.Marshal(eventOutboxCursor{Version: 1, Account: account, App: app, State: options.State, Key: out.Items[len(out.Items)-1].ProducerKey})
			if marshalErr != nil {
				return EventOutboxPage{}, ErrEventOutboxStore
			}
			out.NextAfter = base64.RawURLEncoding.EncodeToString(data)
			break
		}
		out.Items = append(out.Items, status)
	}
	if err := rows.Close(); err != nil {
		return EventOutboxPage{}, eventOutboxStoreError(ctx)
	}
	if rows.Err() != nil {
		return EventOutboxPage{}, eventOutboxStoreError(ctx)
	}
	return out, nil
}
