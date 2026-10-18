package liveproto

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
)

// Result is the reference client for one subscription: it applies server
// frames and refuses any that break the contract. A client that gets an
// error resubscribes with the version it holds.
type Result struct {
	Database string
	Columns  []Column
	Ordered  bool
	Version  uint64
	Snapshot Snapshot
	Expires  int64
	Stale    bool
	Closed   bool
	Err      *Error // the last live.error

	ready     bool
	rows      map[string][]*string
	order     []string
	pending   *Rows
	resyncing bool
}

// Resubscribe records that the client subscribed again (after a reconnect
// or a refused frame): the next live.rows may start a new version sequence,
// since the server may no longer know the versions it sent before.
func (r *Result) Resubscribe() {
	r.resyncing = true
	r.pending = nil
}

// Ready reports whether the first complete result has arrived.
func (r *Result) Ready() bool { return r.ready }

// Apply applies one server frame for this subscription.
func (r *Result) Apply(frame any) error {
	if r.Closed {
		return fmt.Errorf("%w: frame after the subscription closed", ErrProtocol)
	}
	switch f := frame.(type) {
	case *Rows:
		return r.applyRows(f)
	case *Changes:
		return r.applyChanges(f)
	case *Progress:
		return r.applyProgress(f)
	case *Renewed:
		r.Expires = f.Expires
		r.Stale = false
		return nil
	case *Error:
		r.Err = f
		if f.Closed {
			r.Closed = true
		} else {
			r.Stale = true
		}
		return nil
	default:
		return fmt.Errorf("%w: unexpected frame %T", ErrProtocol, frame)
	}
}

func (r *Result) applyRows(f *Rows) error {
	if r.pending != nil && (r.pending.Version != f.Version || r.pending.Snapshot != f.Snapshot) {
		return fmt.Errorf("%w: live.rows for version %d interleaved with version %d", ErrProtocol, f.Version, r.pending.Version)
	}
	if r.pending == nil {
		cp := *f
		cp.Rows = nil
		r.pending = &cp
	}
	r.pending.Rows = append(r.pending.Rows, f.Rows...)
	if !f.Complete {
		return nil
	}
	p := r.pending
	r.pending = nil
	snap, err := ParseSnapshot(p.Snapshot)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrProtocol, err)
	}
	if r.ready && !r.resyncing && p.Version <= r.Version {
		return fmt.Errorf("%w: live.rows version %d does not advance %d", ErrProtocol, p.Version, r.Version)
	}
	rows := make(map[string][]*string, len(p.Rows))
	var order []string
	for _, row := range p.Rows {
		if _, dup := rows[row.ID]; dup {
			return fmt.Errorf("%w: row %s twice", ErrProtocol, row.ID)
		}
		if len(row.Values) != len(p.Columns) {
			return fmt.Errorf("%w: row %s has %d values for %d columns", ErrProtocol, row.ID, len(row.Values), len(p.Columns))
		}
		rows[row.ID] = row.Values
		if p.Ordered {
			order = append(order, row.ID)
		}
	}
	r.Database, r.Columns, r.Ordered = p.Database, p.Columns, p.Ordered
	r.Version, r.Snapshot, r.Expires = p.Version, snap, p.Expires
	r.rows, r.order, r.ready, r.Stale, r.resyncing = rows, order, true, false, false
	return nil
}

func (r *Result) applyChanges(f *Changes) error {
	switch {
	case !r.ready || r.pending != nil:
		return fmt.Errorf("%w: live.changes before a complete live.rows", ErrProtocol)
	case f.Base != r.Version:
		return fmt.Errorf("%w: live.changes from version %d, client holds %d", ErrProtocol, f.Base, r.Version)
	case f.Version <= f.Base:
		return fmt.Errorf("%w: live.changes version %d does not advance %d", ErrProtocol, f.Version, f.Base)
	case r.Stale:
		return fmt.Errorf("%w: live.changes for a stale subscription", ErrProtocol)
	}
	snap, err := r.advance(f.Snapshot)
	if err != nil {
		return err
	}
	for _, x := range f.Xids {
		xid, err := strconv.ParseUint(x, 10, 64)
		if err != nil || !snap.Visible(xid) {
			return fmt.Errorf("%w: transaction %q is not visible in %s", ErrProtocol, x, f.Snapshot)
		}
	}
	rows := make(map[string][]*string, len(r.rows))
	for id, v := range r.rows {
		rows[id] = v
	}
	added := false
	for _, c := range f.Changes {
		switch c.Op {
		case OpUpsert:
			if len(c.Values) != len(r.Columns) {
				return fmt.Errorf("%w: row %s has %d values for %d columns", ErrProtocol, c.ID, len(c.Values), len(r.Columns))
			}
			if _, ok := rows[c.ID]; !ok {
				added = true
			}
			rows[c.ID] = c.Values
		case OpRemove:
			if _, ok := rows[c.ID]; !ok {
				return fmt.Errorf("%w: remove of unknown row %s", ErrProtocol, c.ID)
			}
			delete(rows, c.ID)
		default:
			return fmt.Errorf("%w: change op %q", ErrProtocol, c.Op)
		}
	}
	order := r.order
	switch {
	case f.Order != nil:
		if !r.Ordered {
			return fmt.Errorf("%w: order for an unordered result", ErrProtocol)
		}
		if len(f.Order) != len(rows) {
			return fmt.Errorf("%w: order lists %d rows, result has %d", ErrProtocol, len(f.Order), len(rows))
		}
		seen := make(map[string]bool, len(f.Order))
		for _, id := range f.Order {
			if _, ok := rows[id]; !ok || seen[id] {
				return fmt.Errorf("%w: order is not a permutation of the result", ErrProtocol)
			}
			seen[id] = true
		}
		order = slices.Clone(f.Order)
	case r.Ordered && added:
		return fmt.Errorf("%w: rows added to an ordered result without an order", ErrProtocol)
	case r.Ordered:
		order = slices.DeleteFunc(slices.Clone(order), func(id string) bool { _, ok := rows[id]; return !ok })
	}
	// The server resumed the sequence the client held.
	r.rows, r.order, r.Version, r.Snapshot, r.resyncing = rows, order, f.Version, snap, false
	return nil
}

func (r *Result) applyProgress(f *Progress) error {
	if f.Database != r.Database || !r.ready || r.Stale {
		return nil
	}
	snap, err := r.advance(f.Snapshot)
	if err != nil {
		return err
	}
	r.Snapshot = snap
	return nil
}

// advance parses a frame's snapshot and checks the result does not move
// backwards.
func (r *Result) advance(text string) (Snapshot, error) {
	snap, err := ParseSnapshot(text)
	if err != nil {
		return Snapshot{}, fmt.Errorf("%w: %w", ErrProtocol, err)
	}
	if !snap.Covers(r.Snapshot) {
		return Snapshot{}, fmt.Errorf("%w: snapshot %s is older than %s", ErrProtocol, text, r.Snapshot)
	}
	return snap, nil
}

// Rows returns the result: in the query's order for an ordered result, and
// by row ID otherwise.
func (r *Result) Rows() []Row {
	out := make([]Row, 0, len(r.rows))
	if r.Ordered {
		for _, id := range r.order {
			out = append(out, Row{ID: id, Values: r.rows[id]})
		}
		return out
	}
	for id, v := range r.rows {
		out = append(out, Row{ID: id, Values: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Sees reports whether the result reflects a committed transaction: the
// check behind awaitTxId.
func (r *Result) Sees(xid uint64) bool { return r.ready && r.Snapshot.Visible(xid) }
