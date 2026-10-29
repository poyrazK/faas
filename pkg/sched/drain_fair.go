package sched

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// drainTickWait is how long a tick waits for the dispatches it started before
// it returns (ADR-20261027160930). A dispatch that runs longer finishes in the
// background on its slot, its lease renewed, and the next tick skips it.
const drainTickWait = 2 * time.Second

// drainSlots is the drain's inline dispatch pool, shared by every tick
// (ADR-20261027160930). An app may always hold up to its share of the slots;
// beyond it, it may take another only while more than a share stays free, so
// one slow app cannot occupy the pool other apps need, yet an app alone on
// the node can use most of it. Each app's FIFO queue messages run on one lane.
type drainSlots struct {
	mu      sync.Mutex
	size    int
	share   int
	used    int
	apps    map[string]int
	rows    map[string]struct{}
	lanes   map[string]struct{}
	changed chan struct{}
}

func newDrainSlots(size int) *drainSlots {
	size = max(1, size)
	return &drainSlots{size: size, share: max(1, size/4), apps: map[string]int{},
		rows: map[string]struct{}{}, lanes: map[string]struct{}{}, changed: make(chan struct{})}
}

// inlineSlots returns the drain's pool, sized from dispatchConcurrency on
// first use.
func (d *Drain) inlineSlots() *drainSlots {
	d.slotsOnce.Do(func() { d.slots = newDrainSlots(d.dispatchConcurrency) })
	return d.slots
}

// acquire waits for a slot for row of appID. Within the app's share it takes
// any free slot and waits for one otherwise; extra (beyond the share) it takes
// one only while more than a share stays free, waiting until until. It
// refuses a row already in flight, and, outside extra, an app at its share.
func (s *drainSlots) acquire(ctx context.Context, appID, rowID string, extra bool, until time.Time) bool {
	for {
		s.mu.Lock()
		if _, busy := s.rows[rowID]; busy {
			s.mu.Unlock()
			return false
		}
		within := s.apps[appID] < s.share
		if !within && !extra {
			s.mu.Unlock()
			return false
		}
		if free := s.size - s.used; free > 0 && (within || free > s.share) {
			s.used++
			s.apps[appID]++
			s.rows[rowID] = struct{}{}
			s.mu.Unlock()
			return true
		}
		changed := s.changed
		s.mu.Unlock()
		if !until.IsZero() {
			wait := time.Until(until)
			if wait <= 0 {
				return false
			}
			timer := time.NewTimer(wait)
			select {
			case <-changed:
				timer.Stop()
				continue
			case <-ctx.Done():
				timer.Stop()
				return false
			case <-timer.C:
				return false
			}
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return false
		}
	}
}

func (s *drainSlots) release(appID, rowID string) {
	s.mu.Lock()
	s.used--
	if s.apps[appID]--; s.apps[appID] <= 0 {
		delete(s.apps, appID)
	}
	delete(s.rows, rowID)
	close(s.changed)
	s.changed = make(chan struct{})
	s.mu.Unlock()
}

// lane reserves appID's FIFO queue lane; false while another tick's lane
// for the app is still running.
func (s *drainSlots) lane(appID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, busy := s.lanes[appID]; busy {
		return false
	}
	s.lanes[appID] = struct{}{}
	return true
}

func (s *drainSlots) releaseLane(appID string) {
	s.mu.Lock()
	delete(s.lanes, appID)
	s.mu.Unlock()
}

// handOff dispatches one row on a slot of its own, without waiting for the
// dispatch. It returns false when the row may not have a slot now.
func (d *Drain) handOff(ctx context.Context, inv state.Invocation, extra bool, until time.Time, wg *sync.WaitGroup) bool {
	slots := d.inlineSlots()
	if !slots.acquire(ctx, inv.AppID, inv.ID, extra, until) {
		return false
	}
	wg.Add(1)
	d.inline.Add(1)
	go func() {
		defer d.inline.Done()
		defer wg.Done()
		defer slots.release(inv.AppID, inv.ID)
		d.dispatchOne(ctx, inv)
	}()
	return true
}

// handOffLane delivers an app's FIFO queue messages one at a time, in order,
// on one slot. While the app's lane from an earlier tick still runs, its
// messages wait for a later tick.
func (d *Drain) handOffLane(ctx context.Context, appID string, rows []state.Invocation, wg *sync.WaitGroup) {
	slots := d.inlineSlots()
	if len(rows) == 0 || !slots.lane(appID) {
		return
	}
	laneID := "lane:" + appID
	// Within its share the lane waits for a slot like any row; beyond it,
	// it starts only if a slot is free beyond the others' share now.
	if !slots.acquire(ctx, appID, laneID, false, time.Time{}) && !slots.acquire(ctx, appID, laneID, true, time.Now()) {
		slots.releaseLane(appID)
		return
	}
	wg.Add(1)
	d.inline.Add(1)
	go func() {
		defer d.inline.Done()
		defer wg.Done()
		defer slots.releaseLane(appID)
		defer slots.release(appID, laneID)
		for _, inv := range rows {
			if ctx.Err() != nil {
				return
			}
			d.dispatchOne(ctx, inv)
		}
	}()
}

// waitTick waits for a tick's dispatches until until, then leaves the rest
// running in the background.
func waitTick(ctx context.Context, wg *sync.WaitGroup, until time.Time) {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	timer := time.NewTimer(max(0, time.Until(until)))
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	case <-ctx.Done():
	}
}

// interleaveByApp orders rows round-robin across their apps, each app's rows
// keeping their due order, so a page led by one app's burst serves the
// others' first rows first.
func interleaveByApp(order []string, byApp map[string][]state.Invocation) []state.Invocation {
	var out []state.Invocation
	for round := 0; ; round++ {
		added := false
		for _, appID := range order {
			if rows := byApp[appID]; round < len(rows) {
				out = append(out, rows[round])
				added = true
			}
		}
		if !added {
			return out
		}
	}
}
