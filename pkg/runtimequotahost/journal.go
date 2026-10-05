// Package runtimequotahost retains local host identities and irreversible
// fences. Its operation counts are barriers, never evidence of native VM drain.
package runtimequotahost

// adr: 493
import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

var (
	ErrFenced  = errors.New("regional host runtime is fenced")
	ErrBinding = errors.New("regional host identity conflicts")
	ErrStorage = errors.New("regional host journal is unavailable")
	ErrBusy    = errors.New("regional host operations are in flight")
)

type Identity struct {
	Version                 int
	NodeID, EpochID, BootID string
}

type PermitRecord struct {
	Version    int
	Permit     runtimequota.CapacityPermit
	Registered bool
	FenceID    string
}

type SeatRecord struct {
	Version    int
	Seat       runtimequota.ServingSeat
	Registered bool
	FenceID    string
}

// Journal has one daemon owner. Failed durable writes poison this owner until
// restart so an uncertain rename/fsync cannot accidentally permit execution.
type Journal struct {
	mu          sync.Mutex
	root        *os.Root
	lock        *os.File
	identity    Identity
	epochs      map[string]Identity
	permits     map[string]PermitRecord
	seats       map[string]SeatRecord
	attempts    map[string]string
	parentPins  map[string]runtimequota.CapacityPermit
	permitSeats map[string]map[string]bool
	active      map[string]int
	seatOps     map[string]int
	resuming    map[string]bool
	changed     chan struct{}
	poison      error
	closed      bool
	// Private fault seam exercises uncertain persistence boundaries.
	beforeSync func() error
}

func validID(id string) bool {
	u, err := uuid.Parse(id)
	return err == nil && u != uuid.Nil && u.String() == id
}

func (j *Journal) Identity() Identity { return j.identity }

// RuntimeHostEpoch exposes only a healthy owner's current incarnation. A
// closed or poisoned journal must not advertise a usable permit destination.
func (j *Journal) RuntimeHostEpoch() (runtimequota.HostEpoch, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.ready(); err != nil {
		return runtimequota.HostEpoch{}, err
	}
	return runtimequota.HostEpoch{NodeID: j.identity.NodeID, HostEpochID: j.identity.EpochID}, nil
}

func attemptKey(p runtimequota.CapacityPermit) string {
	// Lifecycle RPCs address instance IDs. Rebinding the same instance on this
	// epoch would let a delayed resume address a different physical attempt.
	return p.HostEpochID + "/" + p.InstanceID
}

func (j *Journal) ready() error {
	if j.closed || j.poison != nil {
		return errors.Join(ErrStorage, j.poison)
	}
	return nil
}

func (j *Journal) checkPermit(p runtimequota.CapacityPermit, current bool) error {
	if p.Validate() != nil {
		return runtimequota.ErrInvalid
	}
	epoch, ok := j.epochs[p.HostEpochID]
	if !ok || epoch.NodeID != p.NodeID || p.NodeID != j.identity.NodeID || (current && p.HostEpochID != j.identity.EpochID) {
		return ErrBinding
	}
	if old, ok := j.permits[p.ID]; ok && old.Permit != p {
		return ErrBinding
	}
	if old, ok := j.parentPins[p.ID]; ok && old != p {
		return ErrBinding
	}
	if id, ok := j.attempts[attemptKey(p)]; ok && id != p.ID {
		return ErrBinding
	}
	return nil
}

func (j *Journal) checkPlacement(p runtimequota.Placement, existing bool) error {
	if err := j.ready(); err != nil {
		return err
	}
	if p.Validate() != nil {
		return runtimequota.ErrInvalid
	}
	if err := j.checkPermit(p.Permit, true); err != nil {
		return err
	}
	r, ok := j.permits[p.Permit.ID]
	if ok && r.FenceID != "" {
		return ErrFenced
	}
	if existing && (!ok || !r.Registered) {
		return ErrBinding
	}
	if p.Seat == nil {
		return nil
	}
	s, ok := j.seats[p.Seat.ID]
	if ok && s.Seat != *p.Seat {
		return ErrBinding
	}
	if ok && s.FenceID != "" {
		return ErrFenced
	}
	if existing && (!ok || !s.Registered) {
		return ErrBinding
	}
	for id := range j.permitSeats[p.Permit.ID] {
		old := j.seats[id]
		if old.Seat.ID != p.Seat.ID && old.Registered && (old.FenceID == "" || j.seatOps[old.Seat.ID] != 0) {
			return ErrBinding
		}
	}
	if !s.Registered && j.active[p.Permit.ID] != 0 {
		return ErrBusy
	}
	return nil
}

func (j *Journal) Check(p runtimequota.Placement) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.checkPlacement(p, true)
}

// Begin retains a resume/command operation on an already registered attempt.
// The caller must check its cell's current authority first. No local record
// grants it. A new seat is registered only after the old seat has been fenced
// and its operations have returned; native pause must be proved separately.
func (j *Journal) Begin(ctx context.Context, p runtimequota.Placement) (func(), error) {
	return j.begin(ctx, p, false, false)
}

// BeginLaunch durably registers a fresh physical attempt before any host work.
// Replaying a registered launch is refused even after restart: a failed RPC or
// empty live map cannot establish that the previous guest has been stopped.
func (j *Journal) BeginLaunch(ctx context.Context, p runtimequota.Placement) (func(), error) {
	return j.begin(ctx, p, true, false)
}

// BeginResume excludes concurrent host operations while a serving seat is
// installed and native resume is checked. This barrier is not native pause or
// stop proof; the cell must independently authorize the exact issued placement.
func (j *Journal) BeginResume(ctx context.Context, p runtimequota.Placement) (func(), error) {
	if p.Seat == nil {
		return nil, runtimequota.ErrInvalid
	}
	return j.begin(ctx, p, false, true)
}

func (j *Journal) begin(ctx context.Context, p runtimequota.Placement, launch, resume bool) (func(), error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := j.checkPlacement(p, false); err != nil {
		return nil, err
	}
	if j.resuming[p.Permit.ID] || (resume && j.active[p.Permit.ID] != 0) {
		return nil, ErrBusy
	}
	r := j.permits[p.Permit.ID]
	if launch == r.Registered {
		return nil, ErrBinding
	}
	if !r.Registered {
		r = PermitRecord{Version: 1, Permit: p.Permit, Registered: true}
		if err := j.write("permit-"+p.Permit.ID+".json", r); err != nil {
			return nil, err
		}
		j.permits[p.Permit.ID] = r
		j.attempts[attemptKey(p.Permit)] = p.Permit.ID
		j.parentPins[p.Permit.ID] = p.Permit
	}
	seatID := ""
	if p.Seat != nil {
		seatID = p.Seat.ID
		if !j.seats[seatID].Registered {
			s := SeatRecord{Version: 1, Seat: *p.Seat, Registered: true}
			if err := j.write("seat-"+seatID+".json", s); err != nil {
				return nil, err
			}
			j.seats[seatID] = s
			j.indexSeat(s)
		}
	}
	permitID := p.Permit.ID
	j.active[permitID]++
	if resume {
		j.resuming[permitID] = true
	}
	if seatID != "" {
		j.seatOps[seatID]++
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			j.mu.Lock()
			defer j.mu.Unlock()
			j.active[permitID]--
			if resume {
				delete(j.resuming, permitID)
			}
			if seatID != "" {
				j.seatOps[seatID]--
			}
			close(j.changed)
			j.changed = make(chan struct{})
		})
	}, nil
}

// FencePermit also accepts a known historical epoch or a fence arriving before
// registration. It never overwrites the first fence or reopens a tombstone.
func (j *Journal) FencePermit(p runtimequota.CapacityPermit, fenceID string) (PermitRecord, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.ready(); err != nil {
		return PermitRecord{}, err
	}
	if !validID(fenceID) {
		return PermitRecord{}, runtimequota.ErrInvalid
	}
	if err := j.checkPermit(p, false); err != nil {
		return PermitRecord{}, err
	}
	r, ok := j.permits[p.ID]
	if ok && r.FenceID != "" {
		return r, nil
	}
	if !ok {
		r = PermitRecord{Version: 1, Permit: p}
	}
	r.FenceID = fenceID
	if err := j.write("permit-"+p.ID+".json", r); err != nil {
		return PermitRecord{}, err
	}
	j.permits[p.ID] = r
	j.attempts[attemptKey(p)] = p.ID
	j.parentPins[p.ID] = p
	return r, nil
}

func (j *Journal) FenceSeat(s runtimequota.ServingSeat, fenceID string) (SeatRecord, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.ready(); err != nil {
		return SeatRecord{}, err
	}
	if s.Validate() != nil || !validID(fenceID) {
		return SeatRecord{}, runtimequota.ErrInvalid
	}
	if err := j.checkPermit(s.Permit, false); err != nil {
		return SeatRecord{}, err
	}
	r, ok := j.seats[s.ID]
	if ok && r.Seat != s {
		return SeatRecord{}, ErrBinding
	}
	if ok && r.FenceID != "" {
		return r, nil
	}
	if !ok {
		r = SeatRecord{Version: 1, Seat: s}
	}
	r.FenceID = fenceID
	if err := j.write("seat-"+s.ID+".json", r); err != nil {
		return SeatRecord{}, err
	}
	j.seats[s.ID] = r
	// The seat's immutable parent also reserves the lifecycle address when
	// its physical registration has not arrived yet.
	j.attempts[attemptKey(s.Permit)] = s.Permit.ID
	j.parentPins[s.Permit.ID] = s.Permit
	j.indexSeat(r)
	return r, nil
}

func (j *Journal) indexSeat(r SeatRecord) {
	id := r.Seat.Permit.ID
	if j.permitSeats[id] == nil {
		j.permitSeats[id] = make(map[string]bool)
	}
	j.permitSeats[id][r.Seat.ID] = true
}

// WaitPermit/WaitSeat drain in-flight host operations after a durable fence.
// They do not count resident, paused, untracked or legacy guests. A successful
// wait must never be translated directly into a signed capacity/seat stop.
func (j *Journal) WaitPermit(ctx context.Context, p runtimequota.CapacityPermit) error {
	return j.wait(ctx, runtimequota.Placement{Permit: p}, false)
}
func (j *Journal) WaitSeat(ctx context.Context, s runtimequota.ServingSeat) error {
	return j.wait(ctx, runtimequota.Placement{Permit: s.Permit, Seat: &s}, true)
}
func (j *Journal) wait(ctx context.Context, p runtimequota.Placement, seat bool) error {
	for {
		j.mu.Lock()
		err := j.ready()
		if err == nil {
			err = j.checkPermit(p.Permit, false)
		}
		n := j.active[p.Permit.ID]
		fenced := j.permits[p.Permit.ID].FenceID != ""
		if seat {
			r, ok := j.seats[p.Seat.ID]
			if !ok || r.Seat != *p.Seat {
				err = ErrBinding
			}
			fenced = fenced || r.FenceID != ""
			n = j.seatOps[p.Seat.ID]
		}
		if !fenced && err == nil {
			err = ErrFenced
		}
		changed := j.changed
		j.mu.Unlock()
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

// Close refuses to release exclusive ownership while operations remain live.
func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return nil
	}
	for _, n := range j.active {
		if n != 0 {
			return ErrBusy
		}
	}
	j.closed = true
	close(j.changed)
	return errors.Join(syscall.Flock(int(j.lock.Fd()), syscall.LOCK_UN), j.lock.Close(), j.root.Close())
}

func (j *Journal) fail(err error) error {
	j.poison = fmt.Errorf("host journal: %w", err)
	return errors.Join(ErrStorage, j.poison)
}
