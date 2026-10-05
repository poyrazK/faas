package runtimequotahost

// adr: 628
import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

// MigrationRecord survives lease expiry, instance collection and daemon restart.
// Source is immutable. Completed records acknowledge a past guarded resume;
// neither capture nor completion is native pause/stop proof or a capacity release.
type MigrationRecord struct {
	Version                          int
	InstanceKey, LeaseToken, FenceID string
	MemStorageKey, VMStateStorageKey string
	Source                           runtimequota.Placement
	Resume                           *runtimequota.Placement
	Completed                        bool
}

func validMigrationToken(token string) bool {
	return len(token) == api.RegionalMigrationLeaseTokenBytes && strings.IndexFunc(token, func(r rune) bool {
		return (r < '0' || r > '9') && (r < 'a' || r > 'f')
	}) == -1
}
func migrationFile(token string) string { return "migration-" + token + ".json" }
func samePlacement(a, b runtimequota.Placement) bool {
	return a.Permit == b.Permit && a.Seat != nil && b.Seat != nil && *a.Seat == *b.Seat
}
func validMigrationRecord(r MigrationRecord) bool {
	if r.Version != 1 || !validMigrationToken(r.LeaseToken) || !validID(r.FenceID) || r.MemStorageKey == "" || r.VMStateStorageKey == "" || r.Source.Validate() != nil || r.Source.Seat == nil || !runtimequota.MatchesID(r.Source.Permit.InstanceID, r.InstanceKey) {
		return false
	}
	if r.Source.Permit.Kind != runtimequota.CapacityServing && r.Source.Permit.Kind != runtimequota.CapacityWarm {
		return false
	}
	return (!r.Completed || r.Resume != nil) && (r.Resume == nil || validMigrationResume(r, *r.Resume))
}
func validMigrationResume(r MigrationRecord, p runtimequota.Placement) bool {
	return p.Validate() == nil && p.Seat != nil && p.Permit == r.Source.Permit && p.Seat.ID != r.Source.Seat.ID && p.Seat.Rollout == r.Source.Seat.Rollout
}
func cloneMigration(r MigrationRecord) MigrationRecord {
	r.Source = r.Source.Clone()
	if r.Resume != nil {
		p := r.Resume.Clone()
		r.Resume = &p
	}
	return r
}

// MigrationSource reads an exact retained binding, including a historical epoch.
// Absence is distinct from storage uncertainty; callers must retain leases on
// uncertainty, and must never infer an independent source from it.
func (j *Journal) MigrationSource(instance, token string) (MigrationRecord, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.ready(); err != nil {
		return MigrationRecord{}, err
	}
	r, ok := j.migrations[token]
	if !ok {
		return MigrationRecord{}, ErrMigrationNotFound
	}
	if r.InstanceKey != instance {
		return MigrationRecord{}, ErrBinding
	}
	return cloneMigration(r), nil
}

// MigrationInventory is independent of mutable/cascading cell lease and
// instance rows. Recovery must reconcile these retained physical sources too.
func (j *Journal) MigrationInventory() ([]MigrationRecord, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.ready(); err != nil {
		return nil, err
	}
	rows := make([]MigrationRecord, 0, len(j.migrations))
	for _, r := range j.migrations {
		rows = append(rows, cloneMigration(r))
	}
	sort.Slice(rows, func(i, k int) bool { return rows[i].LeaseToken < rows[k].LeaseToken })
	return rows, nil
}

// BeginMigrationCapture pins the source before fencing its seat or host work.
// A write lost between source persistence and seat fencing is recovered fenced
// on Open. No normal command/resume can bypass an unresolved migration record.
func (j *Journal) BeginMigrationCapture(ctx context.Context, instance, token string, p runtimequota.Placement, memKey, vmstateKey string) (func(), error) {
	p = p.Clone()
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := j.checkPlacement(p, true); err != nil {
		return nil, err
	}
	r := MigrationRecord{Version: 1, InstanceKey: instance, LeaseToken: token, Source: p.Clone(), FenceID: uuid.NewString(), MemStorageKey: memKey, VMStateStorageKey: vmstateKey}
	if !validMigrationRecord(r) {
		return nil, ErrBinding
	}
	if _, ok := j.migrations[token]; ok || j.migrationPending[p.Permit.ID] != "" {
		return nil, ErrBinding
	}
	if j.active[p.Permit.ID] != 0 || j.resuming[p.Permit.ID] {
		return nil, ErrBusy
	}
	if err := j.write(migrationFile(token), r); err != nil {
		return nil, err
	}
	j.migrations[token] = r
	j.migrationPending[p.Permit.ID] = token
	s := j.seats[p.Seat.ID]
	s.FenceID = r.FenceID
	if err := j.write("seat-"+s.Seat.ID+".json", s); err != nil {
		return nil, err
	}
	j.seats[s.Seat.ID] = s
	id := p.Permit.ID
	j.active[id]++
	j.seatOps[p.Seat.ID]++
	j.resuming[id] = true
	var once sync.Once
	return func() {
		once.Do(func() {
			j.mu.Lock()
			defer j.mu.Unlock()
			j.active[id]--
			j.seatOps[p.Seat.ID]--
			delete(j.resuming, id)
			close(j.changed)
			j.changed = make(chan struct{})
		})
	}, nil
}

// BeginMigrationResume requires the retained source and a fresh cell-issued seat.
// The caller checks current cell authority; this journal cannot issue a seat or
// prove that the old seat's native pause has been independently authenticated.
func (j *Journal) BeginMigrationResume(ctx context.Context, instance, token string, p runtimequota.Placement) (func(), error) {
	if err := j.pinMigrationResume(ctx, instance, token, p); err != nil {
		return nil, err
	}
	return j.begin(ctx, p, false, true, token)
}
func (j *Journal) pinMigrationResume(ctx context.Context, instance, token string, p runtimequota.Placement) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := j.ready(); err != nil {
		return err
	}
	r, ok := j.migrations[token]
	if !ok {
		return ErrMigrationNotFound
	}
	if r.InstanceKey != instance || r.Completed || !validMigrationResume(r, p) || j.migrationPending[p.Permit.ID] != token {
		return ErrBinding
	}
	if r.Resume != nil && !samePlacement(*r.Resume, p) {
		return ErrBinding
	}
	if err := j.checkPlacement(p, false); err != nil {
		return err
	}
	if j.active[p.Permit.ID] != 0 || j.resuming[p.Permit.ID] {
		return ErrBusy
	}
	if r.Resume == nil {
		copy := p.Clone()
		r.Resume = &copy
		if err := j.write(migrationFile(token), r); err != nil {
			return err
		}
		j.migrations[token] = r
	}
	return nil
}

// CompleteMigrationResume is called only after native hook/readiness completion
// and current authority checks, while the exclusive resume operation is held.
func (j *Journal) CompleteMigrationResume(ctx context.Context, instance, token string, p runtimequota.Placement) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := j.checkPlacement(p, true); err != nil {
		return err
	}
	r, ok := j.migrations[token]
	if !ok || r.InstanceKey != instance || r.Resume == nil || !samePlacement(*r.Resume, p) || j.migrationPending[p.Permit.ID] != token || !j.resuming[p.Permit.ID] || j.active[p.Permit.ID] != 1 {
		return ErrBinding
	}
	r.Completed = true
	if err := j.write(migrationFile(token), r); err != nil {
		return err
	}
	j.migrations[token] = r
	delete(j.migrationPending, p.Permit.ID)
	return nil
}

func (j *Journal) loadMigrations() error {
	for token, r := range j.migrations {
		parent, ok := j.permits[r.Source.Permit.ID]
		seat, hasSeat := j.seats[r.Source.Seat.ID]
		if !ok || !parent.Registered || parent.Permit != r.Source.Permit || !hasSeat || !seat.Registered || seat.Seat != *r.Source.Seat {
			return ErrBinding
		}
		// Resume registration and completion occur after source fencing.
		if seat.FenceID == "" {
			if r.Resume != nil {
				return ErrBinding
			}
			seat.FenceID = r.FenceID
			if err := j.write("seat-"+seat.Seat.ID+".json", seat); err != nil {
				return err
			}
			j.seats[seat.Seat.ID] = seat
		} else if seat.FenceID != r.FenceID {
			return ErrBinding
		}
		if r.Completed {
			resume, ok := j.seats[r.Resume.Seat.ID]
			if !ok || !resume.Registered || resume.Seat != *r.Resume.Seat {
				return ErrBinding
			}
		} else {
			if j.migrationPending[parent.Permit.ID] != "" {
				return ErrBinding
			}
			j.migrationPending[parent.Permit.ID] = token
		}
	}
	return nil
}
