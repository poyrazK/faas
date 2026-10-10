// adr: 568 — snapshot headroom belongs to the original physical capture owner.
package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"

	"github.com/onebox-faas/faas/pkg/api"
)

type nativeSnapshotMemoryPhase string

const (
	nativeSnapshotMemoryPlanned   nativeSnapshotMemoryPhase = "planned"
	nativeSnapshotMemoryRaised    nativeSnapshotMemoryPhase = "raised"
	nativeSnapshotMemoryRestoring nativeSnapshotMemoryPhase = "restoring"
	nativeSnapshotMemoryRestored  nativeSnapshotMemoryPhase = "restored"
	nativeSnapshotMemoryRemoved   nativeSnapshotMemoryPhase = "removed"
)

// The surrounding output frame binds this scope to its boot, physical
// generation, PID/start time and one-shot capture. It grants no restart replay.
type nativeSnapshotMemoryFrame struct {
	Group nativeHostHelperGroup     `json:"group"`
	Phase nativeSnapshotMemoryPhase `json:"phase"`
}

func (f *nativeSnapshotMemoryFrame) UnmarshalJSON(data []byte) error {
	fields, err := nativeJournalObjectFields(data, []string{"group", "phase"})
	if err != nil {
		return err
	}
	if _, err := nativeJournalObjectFields(fields["group"], []string{"path", "device", "inode"}); err != nil {
		return err
	}
	type plain nativeSnapshotMemoryFrame
	return json.Unmarshal(data, (*plain)(f))
}

func nativeSnapshotMemoryPath(owner nativeLaunchRecord) string {
	return filepath.Join(ParentCgroupFor(owner.Lease.Plan), PerInstanceScope(owner.Lease.Instance))
}

func nativeSnapshotMemoryLimits(owner nativeLaunchRecord) (uint64, uint64, error) {
	if owner.Lease.MemoryMaxMiB < 1 || owner.Lease.IsBuilder || owner.Lease.MemoryMaxMiB > api.RAMAdmissionCeilingMB {
		return 0, 0, errors.New("native snapshot memory: original tenant RAM policy is required")
	}
	return uint64(api.BillableRAMMB(owner.Lease.MemoryMaxMiB)) << 20, uint64(api.SnapshotMemoryMaxMB(owner.Lease.MemoryMaxMiB)) << 20, nil
}

func (f nativeSnapshotMemoryFrame) validate(owner nativeLaunchRecord) error {
	if _, _, err := nativeSnapshotMemoryLimits(owner); err != nil {
		return err
	}
	if f.Group.Path != nativeSnapshotMemoryPath(owner) || f.Group.Device == 0 || f.Group.Inode == 0 {
		return errors.New("native snapshot memory: original cgroup identity is required")
	}
	switch f.Phase {
	case nativeSnapshotMemoryPlanned, nativeSnapshotMemoryRaised, nativeSnapshotMemoryRestoring, nativeSnapshotMemoryRestored:
		if owner.ResourcesRemoved {
			return errors.New("native snapshot memory: finished owner retains an undisposed scope")
		}
	case nativeSnapshotMemoryRemoved:
		if !owner.Revoked || !owner.ExitConfirmed {
			return errors.New("native snapshot memory: scope disposal precedes original process retirement")
		}
	default:
		return errors.New("native snapshot memory: invalid headroom phase")
	}
	return nil
}

type nativeSnapshotMemoryAllowance interface {
	Raise(context.Context) error
	Restore(context.Context) error
	RequireRestored(context.Context) error
	Close() error
}

type nativeSnapshotMemoryBackend interface {
	Check(context.Context, *JailerVMM, nativeLaunchRecord) error
	Prepare(context.Context, *JailerVMM, nativeLaunchRecord) (nativeSnapshotMemoryAllowance, error)
}

// Only a live original producer receives this pinned capability. Recovery can
// dispose its original empty cgroup, but cannot construct an allowance handle.
type nativeSnapshotMemoryIO interface {
	Check(context.Context) error
	Read() (uint64, error)
	Write(uint64, uint64) error
	Close() error
}

type nativeSnapshotMemoryGrant struct {
	j                 *nativeHostHelperJournal
	owner             nativeLaunchRecord
	record            nativeHostHelperRecord
	io                nativeSnapshotMemoryIO
	checkOwner        func(context.Context) error
	checkRestoreOwner func(context.Context) error
	closed            bool
	pending           nativeSnapshotMemoryPhase
	raiseStarted      bool
}

func (g *nativeSnapshotMemoryGrant) current(ctx context.Context) (nativeHostHelperRecord, error) {
	return g.currentWithOwner(ctx, g.checkOwner)
}

func (g *nativeSnapshotMemoryGrant) currentWithOwner(ctx context.Context, check func(context.Context) error) (nativeHostHelperRecord, error) {
	if g.closed {
		return nativeHostHelperRecord{}, errors.New("native snapshot memory: original capability is closed")
	}
	if err := errors.Join(check(ctx), g.io.Check(ctx)); err != nil {
		return nativeHostHelperRecord{}, err
	}
	records, err := g.j.records(g.owner)
	if err != nil {
		return nativeHostHelperRecord{}, err
	}
	for _, record := range records {
		if record.Launch.Generation != g.record.Launch.Generation {
			continue
		}
		if record.SnapshotOutput == nil || record.SnapshotOutput.Memory == nil {
			break
		}
		// A failed fsync may have published either adjacent phase. The original
		// immutable scope must still match before any bounded restoration.
		if phase := record.SnapshotOutput.Memory.Phase; phase != g.record.SnapshotOutput.Memory.Phase && (g.pending == "" || phase != g.pending) {
			break
		}
		expected := g.record
		output := *expected.SnapshotOutput
		memory := *output.Memory
		memory.Phase = record.SnapshotOutput.Memory.Phase
		output.Memory, expected.SnapshotOutput = &memory, &output
		if !reflect.DeepEqual(expected, record) {
			break
		}
		return record, nil
	}
	return nativeHostHelperRecord{}, errors.New("native snapshot memory: original durable scope changed")
}

func (g *nativeSnapshotMemoryGrant) phase(record nativeHostHelperRecord, phase nativeSnapshotMemoryPhase) error {
	output := *record.SnapshotOutput
	memory := *output.Memory
	memory.Phase = phase
	output.Memory, record.SnapshotOutput = &memory, &output
	g.pending = phase
	if err := g.j.write(g.owner, record); err != nil {
		return err
	}
	g.record = record
	g.pending = ""
	return nil
}

func (g *nativeSnapshotMemoryGrant) Raise(ctx context.Context) error {
	record, err := g.current(ctx)
	if err != nil {
		return err
	}
	if g.raiseStarted || record.SnapshotOutput.Memory.Phase != nativeSnapshotMemoryPlanned {
		return errors.New("native snapshot memory: allowance cannot be replayed")
	}
	base, raised, err := nativeSnapshotMemoryLimits(g.owner)
	if err != nil {
		return err
	}
	g.raiseStarted = true
	if err := g.io.Write(base, raised); err != nil {
		return err
	}
	if _, err := g.current(ctx); err != nil {
		return err
	}
	return g.phase(record, nativeSnapshotMemoryRaised)
}

func (g *nativeSnapshotMemoryGrant) Restore(ctx context.Context) error {
	record, err := g.currentWithOwner(ctx, g.checkRestoreOwner)
	if err != nil {
		return err
	}
	if record.SnapshotOutput.Memory.Phase == nativeSnapshotMemoryRestored {
		base, _, err := nativeSnapshotMemoryLimits(g.owner)
		value, readErr := g.io.Read()
		if value != base {
			return errors.New("native snapshot memory: restored original limit changed")
		}
		if err := errors.Join(err, readErr); err != nil {
			return err
		}
		if g.pending != "" {
			return g.phase(record, nativeSnapshotMemoryRestored)
		}
		return nil
	}
	if record.SnapshotOutput.Memory.Phase == nativeSnapshotMemoryRemoved {
		return errors.New("native snapshot memory: original scope was disposed")
	}
	base, raised, err := nativeSnapshotMemoryLimits(g.owner)
	if err != nil {
		return err
	}
	if err := g.phase(record, nativeSnapshotMemoryRestoring); err != nil {
		return err
	}
	value, err := g.io.Read()
	if err != nil {
		return err
	}
	if value != base && value != raised {
		return errors.New("native snapshot memory: limit changed outside original allowance")
	}
	if err := g.io.Write(value, base); err != nil {
		return err
	}
	record, err = g.currentWithOwner(ctx, g.checkRestoreOwner)
	if err != nil {
		return err
	}
	return g.phase(record, nativeSnapshotMemoryRestored)
}

func (g *nativeSnapshotMemoryGrant) RequireRestored(ctx context.Context) error {
	record, err := g.current(ctx)
	if err != nil {
		return err
	}
	base, _, err := nativeSnapshotMemoryLimits(g.owner)
	if err != nil {
		return err
	}
	value, err := g.io.Read()
	if err != nil {
		return err
	}
	if g.pending != "" || g.record.SnapshotOutput.Memory.Phase != nativeSnapshotMemoryRestored || record.SnapshotOutput.Memory.Phase != nativeSnapshotMemoryRestored || value != base {
		return errors.New("native snapshot memory: original VM fence is not restored")
	}
	return ctx.Err()
}

func (g *nativeSnapshotMemoryGrant) Close() error {
	if g.closed {
		return nil
	}
	g.closed = true
	return g.io.Close()
}

func (j *nativeHostHelperJournal) snapshotMemoryRecord(owner nativeLaunchRecord) (*nativeHostHelperRecord, error) {
	records, err := j.records(owner)
	if err != nil {
		return nil, err
	}
	var match *nativeHostHelperRecord
	for i := range records {
		if records[i].SnapshotOutput == nil {
			continue
		}
		if match != nil {
			return nil, errors.New("native snapshot memory: multiple original capture scopes")
		}
		match = &records[i]
	}
	return match, nil
}

func (j *nativeHostHelperJournal) requireSnapshotMemoryDisposed(owner nativeLaunchRecord) error {
	record, err := j.snapshotMemoryRecord(owner)
	if err != nil {
		return err
	}
	if record != nil && record.SnapshotOutput.Memory != nil && record.SnapshotOutput.Memory.Phase != nativeSnapshotMemoryRemoved {
		return errors.New("native snapshot memory: original headroom scope disposal is incomplete")
	}
	return nil
}
