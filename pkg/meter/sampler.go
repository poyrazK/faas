package meter

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Sampler writes one minute of billable usage per live instance. It walks
// every app on the box (one-box scale; schedd's ListAllApps is the canonical
// source) and lists its instances; for each one in a state that counts
// against the RAM ledger it appends (ram_mb + 8) * 60 mb_seconds to
// usage_minutes for the truncated minute.
//
// Billing rule (spec §4.7): bill on plan RAM + 8 MB, not sampled RSS. The
// admission MB is the source of truth — schedd's ledger already charges the
// same number, so a row in usage_minutes matches what schedd counted toward
// invariant §6.2-2. Tests assert this parity.
//
// PR #75 (#71 in flight on this branch at PR open): the inline
// `ram_mb + api.PerVMOverheadMB` constant folded into api.BillableRAMMB; the
// AppendUsage idempotency on (instance_id, minute) is the meterd↔storage
// contract that prevents silent double-billing under any restart — see
// pkg/state/store.go::Store.AppendUsage.
type Sampler struct {
	store state.Store
	now   func() time.Time // injectable for tests
}

// NewSampler wires the sampler. now defaults to time.Now if nil.
func NewSampler(store state.Store, now func() time.Time) *Sampler {
	if now == nil {
		now = time.Now
	}
	return &Sampler{store: store, now: now}
}

// RolledRow is one (instance, minute) billable line. Returned alongside any
// error so callers (the test surface, telemetry) can observe what was
// billed without re-reading the store.
type RolledRow struct {
	InstanceID  string
	AppID       string
	AccountID   string
	Minute      time.Time
	MBSeconds   int64
	AdmissionMB int
}

// SampleAndRoll walks every app's live instances and appends one minute of
// billable usage per instance to usage_minutes. It is safe to call from a
// single goroutine; the Store implementation is responsible for concurrent
// safety (MemStore holds a single mutex; PgStore's INSERT … ON CONFLICT is
// atomic per row).
//
// The function returns the rows it wrote so tests can assert on the
// exact set without re-querying; production logs the count and moves on.
func (s *Sampler) SampleAndRoll(ctx context.Context) ([]RolledRow, error) {
	minute := MinuteKey(s.now())
	apps, err := s.store.ListAllApps(ctx)
	if err != nil {
		return nil, err
	}
	var out []RolledRow
	for _, app := range apps {
		if app.Status == state.AppDeleted {
			continue
		}
		ins, err := s.store.ListInstancesForApp(ctx, app.ID)
		if err != nil {
			return nil, err
		}
		for _, ins := range ins {
			if !state.State(ins.State).CountsForRAM() {
				continue
			}
			row := RolledRow{
				InstanceID:  ins.ID,
				AppID:       app.ID,
				AccountID:   app.AccountID,
				Minute:      minute,
				AdmissionMB: api.BillableRAMMB(ins.RAMMB),
				MBSeconds:   MBSecondsPerMinute(api.BillableRAMMB(ins.RAMMB)),
			}
			if err := s.store.AppendUsage(ctx, app.AccountID, app.ID, ins.ID, minute, row.MBSeconds, 0); err != nil {
				return out, err
			}
			out = append(out, row)
		}
	}
	return out, nil
}
