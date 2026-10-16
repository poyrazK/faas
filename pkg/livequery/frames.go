package livequery

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/livequery/liveproto"
)

// Frames renders a delta as the live frames one subscriber receives: a
// reset as live.rows (split under the frame limit), anything else as one
// live.changes. base is the result version the subscriber holds; a frame
// moves it to base+1. An empty delta renders nothing: the round's
// live.progress advances the subscriber's snapshot instead.
func (d Delta) Frames(plan *Plan, database string, base uint64, expires int64) ([]any, error) {
	if d.Empty() {
		return nil, nil
	}
	if d.Reset {
		rows := liveproto.Rows{
			Subscription: d.Query,
			Database:     database,
			Version:      base + 1,
			Snapshot:     d.Snapshot,
			Expires:      expires,
			Columns:      plan.Columns,
			Ordered:      plan.Order != nil && plan.Order.Ordered,
			Rows:         protoRows(d.Rows),
		}
		parts, err := rows.Split(api.LiveQueryServerFrameMaxBytes)
		if err != nil {
			return nil, err
		}
		out := make([]any, len(parts))
		for i := range parts {
			out[i] = &parts[i]
		}
		return out, nil
	}
	changes := make([]liveproto.Change, 0, len(d.Removes)+len(d.Upserts))
	for _, id := range d.Removes {
		changes = append(changes, liveproto.Change{Op: liveproto.OpRemove, ID: id})
	}
	for _, r := range d.Upserts {
		changes = append(changes, liveproto.Change{Op: liveproto.OpUpsert, ID: r.ID, Values: r.Values})
	}
	return []any{&liveproto.Changes{
		Type:         liveproto.TypeChanges,
		Subscription: d.Query,
		Base:         base,
		Version:      base + 1,
		Snapshot:     d.Snapshot,
		Xids:         liveproto.FormatXids(d.Xids),
		Changes:      changes,
		Order:        d.Order,
	}}, nil
}

// ProgressFrame says every live subscription on the database reflects the
// round's snapshot. realtimed sends it after the round's other frames.
func ProgressFrame(database, snapshot string) *liveproto.Progress {
	return &liveproto.Progress{Type: liveproto.TypeProgress, Database: database, Snapshot: snapshot}
}

func protoRows(rows []Row) []liveproto.Row {
	out := make([]liveproto.Row, len(rows))
	for i, r := range rows {
		out[i] = liveproto.Row{ID: r.ID, Values: r.Values}
	}
	return out
}
