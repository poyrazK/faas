package runtimequotahost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

func fixture(t *testing.T) (*Journal, string, runtimequota.Placement) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "journal")
	j, err := Open(dir, uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := j.Close(); err != nil {
			t.Error(err)
		}
	})
	policy, _ := api.RuntimeQuotaPolicyForPlan(api.PlanPro)
	g := runtimequota.Grant{IssuerID: uuid.NewString(), GlobalAppID: uuid.NewString(), OwnerID: uuid.NewString(), AllocationID: uuid.NewString(), Generation: 1, IssuedAtMicros: 1, Policy: policy, Concurrency: 2,
		Binding: runtimequota.Binding{CellID: uuid.NewString(), Region: "eu", CellGeneration: 1, RegionalAccountID: uuid.NewString(), RegionalAppID: uuid.NewString(), RegionalAppSlug: "host-test", APIBase: "https://cell.example.com"}}
	p := runtimequota.CapacityPermit{Grant: g, ID: uuid.NewString(), InstanceID: uuid.NewString(), RunID: uuid.NewString(), DeploymentID: uuid.NewString(), NodeID: j.Identity().NodeID, HostEpochID: j.Identity().EpochID, Kind: runtimequota.CapacityWarm}
	return j, dir, runtimequota.Placement{Permit: p, Seat: &runtimequota.ServingSeat{Permit: p, ID: uuid.NewString()}}
}

func launch(t *testing.T, j *Journal, p runtimequota.Placement) func() {
	t.Helper()
	done, err := j.BeginLaunch(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	return done
}

func TestHostJournalRestartAndKernelEpoch(t *testing.T) {
	j, dir, p := fixture(t)
	identity := j.Identity()
	launch(t, j, p)()
	if _, err := Open(dir, identity.NodeID, identity.BootID); !errors.Is(err, ErrBusy) {
		t.Fatalf("second owner: %v", err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir, identity.NodeID, identity.BootID)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Identity() != identity || reopened.Check(p) != nil {
		t.Fatal("daemon restart lost identity or exact attempt")
	}
	if _, err := reopened.BeginLaunch(t.Context(), p); !errors.Is(err, ErrBinding) {
		t.Fatalf("replayed launch could create a ghost: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	rebooted, err := Open(dir, identity.NodeID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rebooted.Close() })
	if rebooted.Identity().EpochID == identity.EpochID {
		t.Fatal("kernel reboot reused host epoch")
	}
	if _, err := rebooted.Begin(t.Context(), p); !errors.Is(err, ErrBinding) {
		t.Fatalf("old epoch admitted: %v", err)
	}
	if _, err := rebooted.FencePermit(p.Permit, uuid.NewString()); err != nil {
		t.Fatal("historical physical attempt could not be fenced", err)
	}
	if err := rebooted.Close(); err != nil {
		t.Fatal(err)
	}
	if rollback, err := Open(dir, identity.NodeID, identity.BootID); err == nil {
		_ = rollback.Close()
		t.Fatal("historical boot epoch reopened")
	}
}

func TestHostJournalFenceBarrierRetainsInFlightOperation(t *testing.T) {
	j, dir, p := fixture(t)
	done := launch(t, j, p)
	fence := uuid.NewString()
	r, err := j.FencePermit(p.Permit, fence)
	if err != nil || r.FenceID != fence || !r.Registered {
		t.Fatalf("fence: %+v %v", r, err)
	}
	if _, err := j.Begin(t.Context(), p); !errors.Is(err, ErrFenced) {
		t.Fatalf("late resume: %v", err)
	}
	if err := j.Close(); !errors.Is(err, ErrBusy) {
		t.Fatalf("released lock before cleanup: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := j.WaitPermit(ctx, p.Permit); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled barrier: %v", err)
	}
	ctx, cancel = context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if err := j.WaitPermit(ctx, p.Permit); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("operation disappeared during fence: %v", err)
	}
	done()
	done() // an idempotent release cannot undercount another operation
	if err := j.WaitPermit(t.Context(), p.Permit); err != nil {
		t.Fatal(err)
	}
	if retry, err := j.FencePermit(p.Permit, uuid.NewString()); err != nil || retry != r {
		t.Fatalf("replaced irreversible fence: %+v %v", retry, err)
	}
	i := j.Identity()
	_ = j.Close()
	j, err = Open(dir, i.NodeID, i.BootID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = j.Close() }()
	if err := j.Check(p); !errors.Is(err, ErrFenced) {
		t.Fatalf("restart reopened fenced attempt: %v", err)
	}
}

func TestHostJournalSeatReplacementAndPhysicalRetention(t *testing.T) {
	j, _, p := fixture(t)
	launch(t, j, p)()
	active, err := j.Begin(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = j.FenceSeat(*p.Seat, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	next := p.Clone()
	next.Seat.ID = uuid.NewString()
	if _, err = j.Begin(t.Context(), next); !errors.Is(err, ErrBinding) {
		t.Fatalf("new seat overlapped old operation: %v", err)
	}
	active()
	if err := j.WaitSeat(t.Context(), *p.Seat); err != nil {
		t.Fatal(err)
	}
	// This test checks bookkeeping only. Operational replacement additionally
	// requires the cell's verified pause proof before it issues the new seat.
	done, err := j.Begin(t.Context(), next)
	if err != nil {
		t.Fatal(err)
	}
	done()
	if j.permits[p.Permit.ID].FenceID != "" {
		t.Fatal("serving pause destroyed the retained physical attempt")
	}
	if _, err = j.Begin(t.Context(), p); !errors.Is(err, ErrFenced) {
		t.Fatalf("old seat resumed: %v", err)
	}
}

func TestHostJournalFenceBeforeRegistrationAndExactPins(t *testing.T) {
	for _, seat := range []bool{false, true} {
		t.Run(map[bool]string{false: "physical", true: "seat"}[seat], func(t *testing.T) {
			j, dir, p := fixture(t)
			if seat {
				_, err := j.FenceSeat(*p.Seat, uuid.NewString())
				if err != nil {
					t.Fatal(err)
				}
			} else {
				_, err := j.FencePermit(p.Permit, uuid.NewString())
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := j.BeginLaunch(t.Context(), p); !errors.Is(err, ErrFenced) {
				t.Fatalf("late registration ignored fence: %v", err)
			}
			mutations := []func(*runtimequota.CapacityPermit){
				func(p *runtimequota.CapacityPermit) { p.RunID = uuid.NewString() },
				func(p *runtimequota.CapacityPermit) { p.ID = uuid.NewString() },
				func(p *runtimequota.CapacityPermit) { p.Grant.AllocationID = uuid.NewString() },
				func(p *runtimequota.CapacityPermit) { p.DeploymentID = uuid.NewString() },
				func(p *runtimequota.CapacityPermit) { p.NodeID = uuid.NewString() },
				func(p *runtimequota.CapacityPermit) { p.HostEpochID = uuid.NewString() },
			}
			for _, mutate := range mutations {
				bad := p.Permit
				mutate(&bad)
				if _, err := j.FencePermit(bad, uuid.NewString()); !errors.Is(err, ErrBinding) {
					t.Fatalf("rebound immutable pins: %v", err)
				}
			}
			i := j.Identity()
			_ = j.Close()
			reopened, err := Open(dir, i.NodeID, i.BootID)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = reopened.Close() }()
			if _, err := reopened.BeginLaunch(t.Context(), p); !errors.Is(err, ErrFenced) {
				t.Fatalf("restart lost early fence: %v", err)
			}
		})
	}
}

func TestHostJournalStorageCorruptionFailsClosed(t *testing.T) {
	for _, kind := range []string{"truncated", "unknown", "duplicate", "oversize", "symlink", "public", "missing-identity"} {
		t.Run(kind, func(t *testing.T) {
			j, dir, p := fixture(t)
			launch(t, j, p)()
			i := j.Identity()
			_ = j.Close()
			path := filepath.Join(dir, "permit-"+p.Permit.ID+".json")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "truncated":
				raw = raw[:len(raw)/2]
			case "unknown":
				raw = append([]byte(`{"unknown":1,`), raw[1:]...)
			case "duplicate":
				raw = append([]byte(`{"Version":1,`), raw[1:]...)
			case "oversize":
				raw = []byte(strings.Repeat(" ", api.RegionalRuntimeHostRecordMaxBytes+1))
			case "symlink":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("identity.json", path); err != nil {
					t.Fatal(err)
				}
			case "public":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "missing-identity":
				if err := os.Remove(filepath.Join(dir, "identity.json")); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "truncated" || kind == "unknown" || kind == "duplicate" || kind == "oversize" {
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if reopened, err := Open(dir, i.NodeID, i.BootID); err == nil {
				_ = reopened.Close()
				t.Fatal("corrupt journal became available")
			}
		})
	}
}

func TestHostJournalUncertainFsyncPoisonsOwner(t *testing.T) {
	j, dir, p := fixture(t)
	launch(t, j, p)()
	j.beforeSync = func() error { return errors.New("injected directory sync failure") }
	if _, err := j.FencePermit(p.Permit, uuid.NewString()); !errors.Is(err, ErrStorage) {
		t.Fatalf("uncertain fence reported durable: %v", err)
	}
	if _, err := j.Begin(t.Context(), p); !errors.Is(err, ErrStorage) {
		t.Fatalf("uncertain owner resumed: %v", err)
	}
	i := j.Identity()
	_ = j.Close()
	reopened, err := Open(dir, i.NodeID, i.BootID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	if err := reopened.Check(p); !errors.Is(err, ErrFenced) {
		t.Fatalf("persisted uncertain tomb lost after restart: %v", err)
	}
}

func TestHostJournalIdentityEpochCopyRecovery(t *testing.T) {
	j, dir, _ := fixture(t)
	i := j.Identity()
	_ = j.Close()
	if err := os.Remove(filepath.Join(dir, "epoch-"+i.EpochID+".json")); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir, i.NodeID, i.BootID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	raw, err := os.ReadFile(filepath.Join(dir, "epoch-"+i.EpochID+".json"))
	var recovered Identity
	if err != nil || json.Unmarshal(raw, &recovered) != nil || recovered != i {
		t.Fatal("epoch copy not recovered from durable identity")
	}
}
