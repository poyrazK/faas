// adr: 20261106214916

package sched

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
	"github.com/onebox-faas/faas/pkg/state"
)

// SnapshotRefreshDetailVCPUs is a would_refresh candidate's detail when its
// snapshot was captured at another guest vCPU count than its app boots with
// now: vmmd would refuse the restore (fcvm.ErrSnapshotVCPUMismatch) and the
// wake would cold-boot (ADR-20261101160231).
const SnapshotRefreshDetailVCPUs = "vcpu_count_changed"

// vcpuShapeRevisionLimit is how many revisions of each CPU shape key a pass
// reads. A key's oldest revision read carries the value before it, so an
// older snapshot is judged by that value.
const vcpuShapeRevisionLimit = 200

// vcpuShapeHistory replays the global revisions of the CPU shape keys, so a
// pass can tell the guest vCPU count a snapshot was captured with: the count
// the settings in force at its capture gave its app. Snapshots do not record
// the count, and vmmd learns it only by restoring one.
type vcpuShapeHistory struct {
	// initial is the settings before the oldest change read.
	initial runtimeconfig.CPUShapeSettings
	// changes, oldest first.
	changes []vcpuShapeChange
}

type vcpuShapeChange struct {
	at      time.Time
	version int64
	key     string
	value   json.RawMessage
}

// readVCPUShapeHistory reads the CPU shape keys' revisions. ok is false when
// the history cannot explain the list and shape the engine boots with now: a
// read failed, or a value was set without a revision. A pass then leaves the
// vCPU count to vmmd's restore.
func (e *Engine) readVCPUShapeHistory(ctx context.Context) (vcpuShapeHistory, bool, error) {
	var h vcpuShapeHistory
	for _, key := range runtimeconfig.CPUShapeKeys {
		revs, err := e.store.ListRuntimeConfigRevisions(ctx, key, state.RuntimeConfigScopeGlobal, "", vcpuShapeRevisionLimit)
		if err != nil {
			return h, false, fmt.Errorf("sched: snapshot refresh: read %s revisions: %w", key, err)
		}
		if len(revs) == 0 {
			continue
		}
		// Newest first: the oldest read holds the value before the history.
		applyVCPUShapeValue(&h.initial, key, revs[len(revs)-1].OldValue)
		for _, rev := range revs {
			h.changes = append(h.changes, vcpuShapeChange{at: rev.CreatedAt, version: rev.Version, key: key, value: rev.NewValue})
		}
	}
	sort.SliceStable(h.changes, func(i, j int) bool {
		if !h.changes[i].at.Equal(h.changes[j].at) {
			return h.changes[i].at.Before(h.changes[j].at)
		}
		return h.changes[i].version < h.changes[j].version
	})
	apps, shape := h.at(time.Time{}, true)
	return h, guestVCPUShapeKey(apps, shape) == e.guestVCPUShapeKey(), nil
}

// at is the list and shape in force at t, or after every change when latest.
func (h vcpuShapeHistory) at(t time.Time, latest bool) (*guestVCPUAppSet, api.CPUShape) {
	s := h.initial
	s.Apps = append([]string(nil), s.Apps...)
	for _, c := range h.changes {
		if !latest && c.at.After(t) {
			break
		}
		applyVCPUShapeValue(&s, c.key, c.value)
	}
	apps, _ := parseGuestVCPUApps(s.Apps)
	return apps, s.Shape
}

// vcpusAt is the guest vCPU count app booted with at t.
func (h vcpuShapeHistory) vcpusAt(t time.Time, app state.App, limits api.Limits) int {
	apps, shape := h.at(t, false)
	return guestVCPUsUnder(apps, shape, app, limits)
}

// applyVCPUShapeValue sets one key as schedd applied it: no value is the
// compiled behavior, and a value schedd refused leaves the last one.
func applyVCPUShapeValue(s *runtimeconfig.CPUShapeSettings, key string, value json.RawMessage) {
	if v := bytes.TrimSpace(value); len(v) == 0 || bytes.Equal(v, []byte("null")) {
		s.Reset(key)
		return
	}
	next := *s
	if _, err := next.Apply(key, value); err != nil {
		return
	}
	if key == runtimeconfig.KeyGuestVCPUFromCPUApps {
		if _, err := parseGuestVCPUApps(next.Apps); err != nil {
			return
		}
	}
	*s = next
}
