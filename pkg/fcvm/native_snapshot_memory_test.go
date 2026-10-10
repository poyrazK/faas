//go:build linux || darwin

package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/jailsetup"
)

// Modeled limit IO tests ordering and durable failure handling; it supplies no
// Linux cgroup or Firecracker acceptance evidence.
type nativeSnapshotMemoryFixtureIO struct {
	value  uint64
	writes int
	closed bool
	before func(uint64, uint64) error
	after  func(uint64) error
}

func (f *nativeSnapshotMemoryFixtureIO) Check(ctx context.Context) error {
	if f.closed {
		return os.ErrClosed
	}
	return ctx.Err()
}
func (f *nativeSnapshotMemoryFixtureIO) Read() (uint64, error) { return f.value, nil }
func (f *nativeSnapshotMemoryFixtureIO) Write(expected, value uint64) error {
	if f.value != expected {
		return errors.New("fixture: changed original limit")
	}
	if f.before != nil {
		if err := f.before(expected, value); err != nil {
			return err
		}
	}
	if value != expected {
		f.writes++
	}
	f.value = value
	if f.after != nil {
		return f.after(value)
	}
	return nil
}
func (f *nativeSnapshotMemoryFixtureIO) Close() error { f.closed = true; return nil }

func nativeSnapshotMemoryFixture(t *testing.T) (*nativeSnapshotMemoryGrant, *nativeSnapshotMemoryFixtureIO) {
	t.Helper()
	j, owner, _, _ := nativeJailDeviceFixture(t)
	owner.Lease.MemoryMaxMiB = 128
	owner.Lease.Plan = api.PlanHobby
	if err := j.owner.write(owner); err != nil {
		t.Fatal(err)
	}
	scope := jailsetup.SnapshotOutputScope{Generation: owner.Generation, BootID: owner.KernelBootID,
		CaptureID: uuid.NewString(), PID: owner.PID, StartTime: owner.StartTime, UID: owner.Lease.UID, GID: owner.Lease.GID,
		ParentPID: 123, ParentStartTime: 456, ParentFDs: [5]int{10, 11, 12, 13, 14},
		Root: jailsetup.DeviceFDIdentity{Device: 1, Inode: 1}, Namespace: jailsetup.DeviceFDIdentity{Device: 2, Inode: 2},
		PIDHandle: jailsetup.DeviceFDIdentity{Device: 3, Inode: 3}, RootMountID: 100,
		Epochs: [2]string{uuid.NewString(), uuid.NewString()}, Outputs: [2]jailsetup.DeviceFDIdentity{{Device: 4, Inode: 4}, {Device: 4, Inode: 5}},
		Targets: [2]jailsetup.DeviceFDIdentity{{Device: 5, Inode: 6}, {Device: 5, Inode: 7}}}
	launch := owner
	launch.Generation, launch.Revoked, launch.ExitConfirmed, launch.ResourcesRemoved = uuid.NewString(), true, true, true
	record := nativeHostHelperRecord{OwnerGeneration: owner.Generation, Launch: launch,
		Group:   nativeHostHelperGroup{Path: filepath.Join("fixture-service", nativeHostHelperScope, launch.Generation), Device: 6, Inode: 6},
		Purpose: nativeHostHelperSnapshotOutputs, SnapshotOutput: &nativeSnapshotOutputFrame{Scope: scope, InputsClosed: true,
			Receipt: &jailsetup.SnapshotOutputReceipt{Scope: scope, MountIDs: [2]uint64{101, 102}},
			Memory:  &nativeSnapshotMemoryFrame{Group: nativeHostHelperGroup{Path: nativeSnapshotMemoryPath(owner), Device: 7, Inode: 7}, Phase: nativeSnapshotMemoryPlanned}}}
	if err := os.MkdirAll(j.root(owner.Generation), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := j.write(owner, record); err != nil {
		t.Fatal(err)
	}
	base, _, err := nativeSnapshotMemoryLimits(owner)
	if err != nil {
		t.Fatal(err)
	}
	io := &nativeSnapshotMemoryFixtureIO{value: base}
	check := func(ctx context.Context) error { return ctx.Err() }
	return &nativeSnapshotMemoryGrant{j: j, owner: owner, record: record, io: io, checkOwner: check, checkRestoreOwner: check}, io
}

func TestNativeSnapshotMemoryJournalPrecedesEffectsAndFencePrecedesReadiness(t *testing.T) {
	g, io := nativeSnapshotMemoryFixture(t)
	base, raised, _ := nativeSnapshotMemoryLimits(g.owner)
	io.before = func(_, next uint64) error {
		record, err := g.j.snapshotMemoryRecord(g.owner)
		if err != nil {
			return err
		}
		want := nativeSnapshotMemoryPlanned
		if next == base {
			want = nativeSnapshotMemoryRestoring
		}
		if record.SnapshotOutput.Memory.Phase != want {
			return errors.New("limit effect preceded its durable intent")
		}
		return nil
	}
	if g.RequireRestored(t.Context()) == nil {
		t.Fatal("planned frame supplied readiness")
	}
	if err := g.Raise(t.Context()); err != nil || io.value != raised {
		t.Fatal("original allowance", err, io.value)
	}
	if g.RequireRestored(t.Context()) == nil || g.Raise(t.Context()) == nil {
		t.Fatal("raised frame supplied readiness or replay")
	}
	if err := g.Restore(t.Context()); err != nil || io.value != base {
		t.Fatal("original restoration", err, io.value)
	}
	io.before = nil
	if err := g.RequireRestored(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := g.Restore(t.Context()); err != nil || io.writes != 2 {
		t.Fatal("restoration replayed a limit effect", err, io.writes)
	}
	if g.j.requireSnapshotMemoryDisposed(g.owner) == nil {
		t.Fatal("restoration acknowledged cgroup disposal")
	}
	if err := g.Close(); err != nil || !io.closed || g.RequireRestored(t.Context()) == nil {
		t.Fatal("closed live capability survived", err)
	}
}

func TestNativeSnapshotMemoryUncertainWritesRestoreOnlyOriginalPolicy(t *testing.T) {
	for _, failure := range []string{"lost_raise", "raised_journal", "restored_journal", "restored_journal_lost_ack", "foreign_limit", "foreign_scope", "owner_lost", "deadline"} {
		t.Run(failure, func(t *testing.T) {
			g, io := nativeSnapshotMemoryFixture(t)
			base, _, _ := nativeSnapshotMemoryLimits(g.owner)
			if failure == "lost_raise" {
				io.after = func(uint64) error { return errors.New("lost original write acknowledgement") }
			}
			if failure == "raised_journal" {
				g.j.writeValue = func(path string, record nativeHostHelperRecord) error {
					if record.SnapshotOutput.Memory.Phase == nativeSnapshotMemoryRaised {
						return errors.New("injected fsync failure")
					}
					return writeNativeJournalValue(path, record)
				}
			}
			err := g.Raise(t.Context())
			if (failure == "lost_raise" || failure == "raised_journal") != (err != nil) {
				t.Fatal("raise outcome", err)
			}
			if g.Raise(t.Context()) == nil {
				t.Fatal("original allowance was replayed")
			}
			io.after, g.j.writeValue = nil, nil
			switch failure {
			case "foreign_limit":
				io.value++
			case "foreign_scope":
				record, err := g.j.snapshotMemoryRecord(g.owner)
				if err != nil {
					t.Fatal(err)
				}
				record.SnapshotOutput.Memory.Group.Inode++
				if err := g.j.write(g.owner, *record); err != nil {
					t.Fatal(err)
				}
			case "owner_lost":
				g.checkRestoreOwner = func(context.Context) error { return errors.New("original owner changed") }
			case "deadline":
				g.checkOwner = func(context.Context) error { return context.DeadlineExceeded }
			case "restored_journal", "restored_journal_lost_ack":
				g.j.writeValue = func(path string, record nativeHostHelperRecord) error {
					if record.SnapshotOutput.Memory.Phase == nativeSnapshotMemoryRestored {
						if failure == "restored_journal_lost_ack" {
							if err := writeNativeJournalValue(path, record); err != nil {
								return err
							}
						}
						return errors.New("injected completion failure")
					}
					return writeNativeJournalValue(path, record)
				}
			}
			before := io.writes
			err = g.Restore(t.Context())
			refuses := failure == "foreign_limit" || failure == "foreign_scope" || failure == "owner_lost" || strings.HasPrefix(failure, "restored_journal")
			if refuses != (err != nil) {
				t.Fatal("restoration outcome", err)
			}
			if refuses && !strings.HasPrefix(failure, "restored_journal") && io.writes != before {
				t.Fatal("foreign authority was modified")
			}
			if !refuses && io.value != base {
				t.Fatal("original policy not restored")
			}
			if strings.HasPrefix(failure, "restored_journal") {
				if g.RequireRestored(t.Context()) == nil {
					t.Fatal("uncertain journal supplied readiness")
				}
				g.j.writeValue = nil
				before := io.writes
				if err := g.Restore(t.Context()); err != nil || io.writes != before {
					t.Fatal("original restoration recovery rewrote limit", err)
				}
			}
			if err := g.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNativeSnapshotMemoryFrameStrictShapesAndRetirement(t *testing.T) {
	g, _ := nativeSnapshotMemoryFixture(t)
	data, err := json.Marshal(g.record.SnapshotOutput)
	if err != nil {
		t.Fatal(err)
	}
	var frame nativeSnapshotOutputFrame
	if err := json.Unmarshal(data, &frame); err != nil || frame.validate(g.owner, g.record.Launch) != nil {
		t.Fatal("original frame restart", err)
	}
	for _, bad := range []string{
		strings.Replace(string(data), `"phase":"planned"`, `"phase":null`, 1),
		strings.Replace(string(data), `"phase":"planned"`, `"phase":"raised","phase":"restored"`, 1),
		strings.Replace(string(data), `"inode":7`, `"unknown":7`, 1),
		strings.Replace(string(data), `"memory":{`, `"memory":null,"extra":{`, 1),
	} {
		if json.Unmarshal([]byte(bad), &frame) == nil {
			t.Fatal("damaged memory frame accepted", bad)
		}
	}
	owner := g.owner
	owner.ResourcesRemoved, owner.Revoked, owner.ExitConfirmed = true, true, true
	if g.record.SnapshotOutput.Memory.validate(owner) == nil {
		t.Fatal("unfinished headroom disappeared with physical acknowledgement")
	}
	memory := *g.record.SnapshotOutput.Memory
	memory.Phase = nativeSnapshotMemoryRemoved
	if err := memory.validate(owner); err != nil {
		t.Fatal(err)
	}
	if memory.validate(g.owner) == nil {
		t.Fatal("live original owner disposed its scope")
	}
}

// The trusted protocol peer has no VM cgroup. Explicitly model only this
// allowance; the separate real VM test uses the Linux backend without override.
type nativeModeledSnapshotMemoryBackend struct{ effect func(string) error }

func (nativeModeledSnapshotMemoryBackend) Check(ctx context.Context, _ *JailerVMM, _ nativeLaunchRecord) error {
	return ctx.Err()
}
func (b nativeModeledSnapshotMemoryBackend) Prepare(ctx context.Context, _ *JailerVMM, _ nativeLaunchRecord) (nativeSnapshotMemoryAllowance, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b.effect != nil {
		if err := b.effect("memory:prepare"); err != nil {
			return nil, err
		}
	}
	return &nativeModeledSnapshotMemoryAllowance{effect: b.effect}, nil
}

type nativeModeledSnapshotMemoryAllowance struct {
	effect           func(string) error
	restored, closed bool
}

func (g *nativeModeledSnapshotMemoryAllowance) Raise(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if g.effect != nil {
		return g.effect("memory:raise")
	}
	return nil
}
func (g *nativeModeledSnapshotMemoryAllowance) Restore(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if g.restored {
		return nil
	}
	g.restored = true
	if g.effect != nil {
		return g.effect("memory:restore")
	}
	return nil
}
func (g *nativeModeledSnapshotMemoryAllowance) RequireRestored(ctx context.Context) error {
	if !g.restored || g.closed {
		return errors.New("modeled original fence is not restored")
	}
	return ctx.Err()
}
func (g *nativeModeledSnapshotMemoryAllowance) Close() error { g.closed = true; return nil }
