package livequery

import (
	"errors"
	"reflect"
)

// ErrReleased is returned by Round while the engine has released the
// database.
var ErrReleased = errors.New("livequery: the engine has released the database")

// Release lets go of the database so it can scale to zero (ADR-20261016151947,
// "Parked databases"): it closes the replication connection, after a final
// confirmation, and every pooled connection. A database that commits nothing
// cannot change any result, so the results stay in memory and Current and
// Join serve them. Resume attaches a new capture on the same slot. Release
// must not run concurrently with Round; a checkpointed engine should write
// its checkpoint first, so the slot keeps as little WAL as it can.
func (e *Engine) Release() error {
	if e.capture == nil {
		return nil
	}
	err := e.capture.Close()
	e.capture = nil
	// Decoded but unapplied transactions stream again from the slot's
	// confirmed position.
	e.pending = nil
	e.pool.Reset()
	return err
}

// Released reports whether the engine has released the database.
func (e *Engine) Released() bool { return e.capture == nil }

// Resume attaches a capture on the engine's slot after Release. Transactions
// the last round already applied stream again and are skipped. A capture
// that had to create the slot (it was lost while released) cannot replay
// what happened since, so every result is read again at the next round and
// its subscribers receive it whole.
func (e *Engine) Resume(capture *Capture) error {
	if e.capture != nil {
		return errors.New("livequery: resume: the engine has not released the database")
	}
	e.capture = capture
	if !capture.Created {
		return nil
	}
	for _, g := range e.groups {
		for _, m := range g.members {
			if err := m.reset(); err != nil {
				return err
			}
		}
	}
	return nil
}

// Join attaches subscription id to a result the engine already holds for the
// same query, session and arguments, without the database: the caller sends
// Current(id) at once, and later rounds send its changes as for the other
// subscribers. It reports false when no ready result matches exactly, and
// RegisterAs must then compile or read.
func (e *Engine) Join(id, sql string, session *Session, args ...any) bool {
	if _, dup := e.byID[id]; dup {
		return false
	}
	g := e.byKey[sql+session.key()]
	if g == nil || g.skip || g.pendingPlan != nil {
		return false
	}
	for _, m := range g.members {
		if m.ready && len(m.args) == len(args) && reflect.DeepEqual(m.args, args) {
			m.ids = append(m.ids, id)
			e.byID[id] = m
			return true
		}
	}
	return false
}
