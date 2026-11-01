package sched

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// maxDuePagesPerTick bounds one tick's walk of the due backlog.
const maxDuePagesPerTick = 32

// dueWalkHeadPages is the part of a tick's walk that starts at the head of
// the due backlog every tick (ADR-20261101091437). The rest of the walk
// resumes where the previous tick's walk stopped.
const dueWalkHeadPages = 8

// dueInvocationPager is implemented by stores that can resume the due walk
// after a keyset cursor (PgStore, MemStore).
type dueInvocationPager interface {
	ListDueInvocationsAfter(ctx context.Context, now time.Time, after state.InvocationDueCursor, limit int) ([]state.Invocation, error)
}

// dueRound is the drain's place in its round over the due backlog
// (ADR-20261101091437). A round starts at the head and ends when a walk
// lists the last due row; the next tick then starts a new one.
type dueRound struct {
	mu sync.Mutex
	// resume is the last row the previous tick's walk listed. The zero
	// value resumes right after the head part.
	resume state.InvocationDueCursor
	// heldLanes are the apps whose FIFO queue rows this round listed but
	// could not start on their lane. Their later rows wait for the next
	// round, which lists the skipped ones first, so a lane never delivers a
	// message ahead of an earlier one it skipped.
	heldLanes map[string]struct{}
}

func (r *dueRound) restart() {
	r.resume = state.InvocationDueCursor{}
	r.heldLanes = nil
}

func (r *dueRound) laneHeld(appID string) bool {
	_, held := r.heldLanes[appID]
	return held
}

func (r *dueRound) holdLane(appID string) {
	if r.heldLanes == nil {
		r.heldLanes = map[string]struct{}{}
	}
	r.heldLanes[appID] = struct{}{}
}

// dueWalkEnd is how a part of a walk ended.
type dueWalkEnd int

const (
	// dueWalkTail: the part listed the last due row.
	dueWalkTail dueWalkEnd = iota
	// dueWalkMore: the part stopped with due rows left behind it.
	dueWalkMore
	// dueWalkFailed: a listing failed; the round keeps its place.
	dueWalkFailed
)

// walkDue lists up to maxDuePagesPerTick pages of the due backlog and hands
// their rows off, returning the rows past their app's share. The first
// dueWalkHeadPages pages start at the head, so the oldest due rows and each
// app's earliest queue messages get a chance every tick. The rest resume
// after the last row the previous tick listed, so rows the drain cannot
// dispatch now cannot hide the rows behind them: every due row is listed
// within one round, ⌈backlog / rows the resumed part lists⌉ + 1 ticks.
func (d *Drain) walkDue(ctx context.Context, pager dueInvocationPager, tick *sync.WaitGroup) []state.Invocation {
	r := &d.round
	r.mu.Lock()
	defer r.mu.Unlock()
	now := d.now()
	// The walk lists only apps this node may claim: other nodes' rows would
	// fill its pages without being dispatched.
	owner := d.engine.claimNodeID()
	var extra []state.Invocation
	last, end := d.walkDuePages(ctx, pager, now, state.InvocationDueCursor{OwnerNodeID: owner}, dueWalkHeadPages, false, tick, &extra)
	switch end {
	case dueWalkTail:
		r.restart()
		return extra
	case dueWalkFailed:
		return extra
	}
	from := r.resume
	if dueCursorBefore(from, last) {
		from = last
	}
	from.OwnerNodeID = owner
	last, end = d.walkDuePages(ctx, pager, now, from, maxDuePagesPerTick-dueWalkHeadPages, true, tick, &extra)
	switch end {
	case dueWalkTail:
		r.restart()
	case dueWalkMore:
		r.resume = last
	}
	return extra
}

// walkDuePages lists up to pages pages of due rows after from and hands them
// off. resumed marks the part of the walk after the head, where an app whose
// lane this round held keeps its queue rows waiting.
func (d *Drain) walkDuePages(ctx context.Context, pager dueInvocationPager, now time.Time, from state.InvocationDueCursor, pages int, resumed bool, tick *sync.WaitGroup, extra *[]state.Invocation) (state.InvocationDueCursor, dueWalkEnd) {
	after := from
	for range pages {
		rows, err := pager.ListDueInvocationsAfter(ctx, now, after, d.batchSize)
		if err != nil {
			d.log.Warn("drain: list-due", "err", err)
			return after, dueWalkFailed
		}
		if len(rows) == 0 {
			return after, dueWalkTail
		}
		triggerSkipped := d.dispatchDuePage(ctx, rows, &d.round, resumed, tick, extra)
		last := rows[len(rows)-1]
		after = state.InvocationDueCursor{DueAt: last.DueAt, ID: last.ID, OwnerNodeID: from.OwnerNodeID}
		if len(rows) < d.batchSize {
			return after, dueWalkTail
		}
		// A bound queue trigger owns its source rows; the trigger poller
		// will claim them on its event-driven tick. Stopping here keeps the
		// generic drain from selecting them again while the push consumer
		// processes them.
		if triggerSkipped {
			return after, dueWalkMore
		}
	}
	return after, dueWalkMore
}

// walkDueFromHead serves a store without a due cursor: it can only re-list
// from the head, and an identical page means nothing in it could be claimed.
func (d *Drain) walkDueFromHead(ctx context.Context, tick *sync.WaitGroup) []state.Invocation {
	var extra []state.Invocation
	var prevPage string
	for {
		rows, err := d.store.ListDueInvocations(ctx, d.now(), d.batchSize)
		if err != nil {
			d.log.Warn("drain: list-due", "err", err)
			return extra
		}
		if len(rows) == 0 {
			return extra
		}
		key := invocationPageKey(rows)
		if key == prevPage {
			return extra
		}
		prevPage = key
		if d.dispatchDuePage(ctx, rows, nil, false, tick, &extra) || len(rows) < d.batchSize {
			return extra
		}
	}
}

// dispatchDuePage hands a page's rows off round-robin across their apps and
// each app's FIFO queue messages to its lane. Rows past their app's share
// are appended to extra. It reports whether it skipped a row a bound queue
// trigger owns. round, when set, tracks the lanes the page could not start.
func (d *Drain) dispatchDuePage(ctx context.Context, rows []state.Invocation, round *dueRound, resumed bool, tick *sync.WaitGroup, extra *[]state.Invocation) bool {
	byApp := map[string][]state.Invocation{}
	order := []string{}
	for _, r := range rows {
		if _, seen := byApp[r.AppID]; !seen {
			order = append(order, r.AppID)
		}
		byApp[r.AppID] = append(byApp[r.AppID], r)
	}
	queueRowsByApp := make(map[string][]state.Invocation)
	queueTriggers := make(map[string][]sqlc.Trigger)
	queueTriggerSkipped := false
	for _, inv := range interleaveByApp(order, byApp) {
		if inv.Source == state.InvocationQueue || inv.Source == state.InvocationDelayedTask {
			if inv.EnvironmentID == "" && inv.WorkPolicyName == "" && d.queueSourceBound(ctx, inv, queueTriggers) {
				queueTriggerSkipped = true
				continue
			}
			// Each app's FIFO queue messages run in order on its lane.
			if inv.Source == state.InvocationQueue && inv.WorkPolicyName == "" {
				queueRowsByApp[inv.AppID] = append(queueRowsByApp[inv.AppID], inv)
				continue
			}
		}
		if !d.handOff(ctx, inv, false, time.Time{}, tick) {
			*extra = append(*extra, inv)
		}
	}
	for _, appID := range order {
		lane := queueRowsByApp[appID]
		if len(lane) == 0 {
			continue
		}
		// The head part lists each app's earliest rows, so it always may
		// start a lane; later in the round, rows of an app whose lane
		// skipped earlier rows wait for the next round.
		if round != nil && resumed && round.laneHeld(appID) {
			continue
		}
		if !d.handOffLane(ctx, appID, lane, tick) && round != nil {
			round.holdLane(appID)
		}
	}
	return queueTriggerSkipped
}

// dueCursorBefore reports whether a sorts before b in the walk's (due_at, id)
// order. The zero cursor sorts before every row.
func dueCursorBefore(a, b state.InvocationDueCursor) bool {
	if a.ID == "" {
		return b.ID != ""
	}
	if b.ID == "" {
		return false
	}
	if !a.DueAt.Equal(b.DueAt) {
		return a.DueAt.Before(b.DueAt)
	}
	return a.ID < b.ID
}

func invocationPageKey(rows []state.Invocation) string {
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(r.ID)
		b.WriteByte(',')
	}
	return b.String()
}
