//go:build linux || darwin

package fcvm

// adr: 629
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
	"github.com/onebox-faas/faas/pkg/runtimequotahost"
	"github.com/onebox-faas/faas/pkg/state"
)

func preparedNativeRegionalJournal(t *testing.T) (*nativeLaunchJournal, Lease, runtimequota.Placement) {
	t.Helper()
	_, _, req := regionalHostFixture(t, &fakeVMM{})
	lease := leaseForSlot(req.Instance, 3)
	lease.Plan, lease.MemoryMaxMiB = req.Plan, req.MemSizeMiB
	j := nativeJournalFixture(filepath.Join(t.TempDir(), "journal"))
	if err := j.prepareWithPlacement(t.Context(), lease, req.RegionalRuntime); err != nil {
		t.Fatal(err)
	}
	return j, lease, req.RegionalRuntime.Clone()
}

func TestNativeRegionalBindingPrecedesAuthorizationAndSurvivesRestart(t *testing.T) {
	j, lease, p := preparedNativeRegionalJournal(t)
	prepared, err := j.read(lease.Instance)
	if err != nil || prepared.Version != 2 || prepared.Authorized || !sameOptionalRuntimePlacement(prepared.RegionalRuntime, &p) {
		t.Fatal("prepared frame lost regional provenance", err)
	}
	p.Seat.ID = uuid.NewString()
	ticket, err := j.beginLaunch(t.Context(), lease)
	if err != nil {
		t.Fatal(err)
	}
	if err := ticket.authorize(42, 101); err != nil {
		t.Fatal(err)
	}
	if err := ticket.close(); err != nil {
		t.Fatal(err)
	}
	restarted := nativeJournalFixture(j.root)
	r, err := restarted.read(lease.Instance)
	if err != nil || r.Version != 2 || r.Generation != prepared.Generation || r.PID != 42 || r.StartTime != 101 || !sameOptionalRuntimePlacement(r.RegionalRuntime, prepared.RegionalRuntime) {
		t.Fatal("authorization/restart replaced original placement", err)
	}
	if r.RegionalRuntime.Seat.ID == p.Seat.ID {
		t.Fatal("caller mutation replaced durable source")
	}
	if records, err := restarted.records(t.Context()); err != nil || len(records) != 1 || records[0].RegionalRuntime == nil {
		t.Fatal("restart inventory lost regional source", err)
	}
}

func TestNativeRegionalRecordRejectsAmbiguousOrUnboundPlacement(t *testing.T) {
	for _, scenario := range []string{"legacy", "missing", "null", "duplicate", "nested-duplicate", "unknown", "wrong-instance", "wrong-plan", "missing-serving-seat", "builder", "networkless", "unbound-version", "future-version"} {
		t.Run(scenario, func(t *testing.T) {
			j, lease, _ := preparedNativeRegionalJournal(t)
			r, err := j.read(lease.Instance)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "wrong-instance":
				r.Lease.Instance = uuid.NewString()
				r.Lease.Netns = "fc-" + r.Lease.Instance
			case "wrong-plan":
				r.Lease.Plan = api.PlanHobby
			case "missing-serving-seat":
				r.RegionalRuntime.Seat = nil
			case "builder":
				r.Lease.IsBuilder = true
			case "networkless":
				r.Lease.Networkless = true
			case "unbound-version":
				r.Version = 1
			case "future-version":
				r.Version = 3
			case "legacy":
				r.Version, r.RegionalRuntime = 1, nil
			}
			raw, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "missing":
				delete(fields, "regional_runtime")
				raw, err = json.Marshal(fields)
			case "null":
				fields["regional_runtime"] = json.RawMessage("null")
				raw, err = json.Marshal(fields)
			case "duplicate":
				raw = append(raw[:len(raw)-1], []byte(`,"regional_runtime":`+string(fields["regional_runtime"])+`}`)...)
			case "nested-duplicate":
				raw = []byte(strings.Replace(string(raw), `"Permit":{`, `"Seat":null,"Permit":{`, 1))
			case "unknown":
				raw = []byte(strings.Replace(string(raw), `"Permit":{`, `"Unknown":true,"Permit":{`, 1))
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(j.path(lease.Instance), raw, 0o600); err != nil {
				t.Fatal(err)
			}
			decoded, err := j.read(lease.Instance)
			if scenario == "legacy" {
				if err != nil || decoded.RegionalRuntime != nil || decoded.Version != 1 {
					t.Fatal("legacy independent record was reinterpreted", err)
				}
			} else if err == nil {
				t.Fatal("ambiguous regional frame became native authority")
			}
		})
	}
}

func TestNativeRegionalFallbackPreservesFullOriginalBindingAndArchive(t *testing.T) {
	j, lease, p := preparedNativeRegionalJournal(t)
	old, err := j.retire(t.Context(), lease.Instance, nativeProcessRetirer{probe: nativeProcessProbe{root: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	changed := lease
	changed.MemoryMaxMiB++
	if _, err := j.replace(t.Context(), changed, old.Generation, false); err == nil {
		t.Fatal("regional fallback changed original memory lease")
	}
	other := p.Clone()
	other.Seat.ID = uuid.NewString()
	if _, err := j.replaceWithPlacement(t.Context(), lease, old.Generation, false, &other); err == nil {
		t.Fatal("regional fallback substituted its initial serving seat")
	}
	fresh, err := j.replace(t.Context(), lease, old.Generation, false)
	if err != nil || fresh.Version != 2 || fresh.Generation == old.Generation || !sameOptionalRuntimePlacement(fresh.RegionalRuntime, &p) {
		t.Fatal("fallback lost regional placement", err)
	}
	archived, err := readNativeLaunchRecord(filepath.Join(j.root, "retired", old.Generation+".json"), lease.Instance)
	if err != nil || !sameNativeRegionalBinding(archived, old) {
		t.Fatal("archive lost original regional source", err)
	}
	retired, err := j.retire(t.Context(), lease.Instance, nativeProcessRetirer{probe: nativeProcessProbe{root: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	if err := j.confirmResourcesRemoved(t.Context(), retired); err != nil {
		t.Fatal(err)
	}
	if _, err := j.replace(t.Context(), lease, retired.Generation, true); err == nil {
		t.Fatal("complete replacement downgraded regional ownership")
	}
	next := p.Clone()
	next.Permit.ID, next.Permit.RunID = uuid.NewString(), uuid.NewString()
	next.Seat.Permit, next.Seat.ID = next.Permit, uuid.NewString()
	if r, err := j.replaceWithPlacement(t.Context(), lease, retired.Generation, true, &next); err != nil || !sameOptionalRuntimePlacement(r.RegionalRuntime, &next) {
		t.Fatal("explicit fully retired replacement failed", err)
	}
}

func TestNativeLegacyFallbackCannotRetrofitRegionalAuthority(t *testing.T) {
	_, lease, p := preparedNativeRegionalJournal(t)
	j := nativeJournalFixture(filepath.Join(t.TempDir(), "legacy-journal"))
	if err := j.prepare(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	old, err := j.retire(t.Context(), lease.Instance, nativeProcessRetirer{probe: nativeProcessProbe{root: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.replaceWithPlacement(t.Context(), lease, old.Generation, false, &p); err == nil {
		t.Fatal("unbound fallback retrofitted a regional permit")
	}
}

func TestNativeRegionalLaunchGateOpensOnlyAfterBoundIncarnationWrite(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "authorized", true: "uncertain-write"}[fail], func(t *testing.T) {
			j, lease, p := preparedNativeRegionalJournal(t)
			marker := filepath.Join(t.TempDir(), "native-effect")
			cmd := nativeJournalFixtureCommand(t, j, lease)
			cmd.Env = append(os.Environ(), "GORACE=atexit_sleep_ms=0", "GREGALE_NATIVE_LAUNCH_FIXTURE=1", "GREGALE_NATIVE_LAUNCH_MARKER="+marker)
			cause := errors.New("bound incarnation fsync uncertain")
			j.writeRecord = func(path string, r nativeLaunchRecord) error {
				if r.Version != 2 || !sameOptionalRuntimePlacement(r.RegionalRuntime, &p) || !r.Authorized || r.PID <= 0 || r.StartTime != 101 {
					t.Error("incarnation write lost original regional placement")
					return runtimequotahost.ErrBinding
				}
				if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
					t.Error("child performed effects before bound incarnation persistence")
					return runtimequotahost.ErrBinding
				}
				if fail {
					return cause
				}
				return writeNativeLaunchRecord(path, r)
			}
			started, err := j.launch(t.Context(), lease, cmd, func(int) (uint64, error) { return 101, nil })
			if !started || fail && !errors.Is(err, cause) || !fail && err != nil {
				t.Fatal("launch gate outcome", err)
			}
			t.Cleanup(func() { _ = cmd.Process.Kill() })
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case childErr := <-done:
				if (childErr == nil) == fail {
					t.Fatal("child outcome differs from durable authorization", childErr)
				}
			case <-time.After(3 * time.Second):
				_ = cmd.Process.Kill()
				<-done
				t.Fatal("fixture did not finish after gate closed")
			}
			_, markerErr := os.Stat(marker)
			if fail && !errors.Is(markerErr, os.ErrNotExist) || !fail && markerErr != nil {
				t.Fatal("child effects differ from bound authorization", markerErr)
			}
		})
	}
}

func TestNativeRegionalLiveBoundaryKeepsPhysicalPermitAcrossFreshSeats(t *testing.T) {
	for _, scenario := range []string{"fresh-seat", "generation", "permit", "lease", "prepared", "revoked", "unbound", "missing-local-producer"} {
		t.Run(scenario, func(t *testing.T) {
			m, v, _, req := nativeRegionalManagerFixture(t)
			lease := leaseForSlot(req.Instance, 3)
			lease.Plan = req.Plan
			if err := v.prepareNativeRegionalLease(t.Context(), lease, *req.RegionalRuntime); err != nil {
				t.Fatal(err)
			}
			owner, err := v.nativeRecovery.journal.read(req.Instance)
			if err != nil {
				t.Fatal(err)
			}
			owner.Authorized, owner.PID, owner.StartTime = true, 42, 101
			inst := &Instance{Lease: lease, nativeGeneration: owner.Generation}
			p := req.RegionalRuntime.Clone()
			p.Seat.ID = uuid.NewString()
			switch scenario {
			case "generation":
				inst.nativeGeneration = uuid.NewString()
			case "permit":
				p.Permit.RunID = uuid.NewString()
				p.Seat.Permit = p.Permit
			case "lease":
				inst.Lease.MemoryMaxMiB++
			case "prepared":
				owner.Authorized, owner.PID, owner.StartTime = false, 0, 0
			case "revoked":
				owner.Revoked = true
			case "unbound":
				owner.Version, owner.RegionalRuntime = 1, nil
			case "missing-local-producer":
				v.nativeRecovery.owned = map[string]string{}
			}
			if err := v.nativeRecovery.journal.write(owner); err != nil {
				t.Fatal(err)
			}
			err = m.checkNativeRegionalInstance(inst, p)
			if scenario == "fresh-seat" && err != nil || scenario != "fresh-seat" && !errors.Is(err, runtimequotahost.ErrBinding) {
				t.Fatal("live boundary did not preserve original physical provenance", err)
			}
		})
	}
}

func TestNativeRegionalConfirmationsCannotBorrowChangedBinding(t *testing.T) {
	for _, confirmation := range []string{"exit", "resources"} {
		for _, change := range []string{"permit", "seat", "lease", "unbound"} {
			t.Run(confirmation+"/"+change, func(t *testing.T) {
				j, lease, _ := preparedNativeRegionalJournal(t)
				r, err := j.revoke(t.Context(), lease.Instance)
				if err != nil {
					t.Fatal(err)
				}
				if confirmation == "resources" {
					if err := j.confirmExit(t.Context(), r); err != nil {
						t.Fatal(err)
					}
					r, err = j.read(lease.Instance)
					if err != nil {
						t.Fatal(err)
					}
				}
				switch change {
				case "permit":
					r.RegionalRuntime.Permit.RunID = uuid.NewString()
					r.RegionalRuntime.Seat.Permit = r.RegionalRuntime.Permit
				case "seat":
					r.RegionalRuntime.Seat.ID = uuid.NewString()
				case "lease":
					r.Lease.MemoryMaxMiB++
				case "unbound":
					r.Version, r.RegionalRuntime = 1, nil
				}
				confirm := j.confirmExit
				if confirmation == "resources" {
					confirm = j.confirmResourcesRemoved
				}
				if err := confirm(t.Context(), r); err == nil {
					t.Fatal("another regional binding borrowed native confirmation")
				}
			})
		}
	}
}

type nativeRegionalInspectionRunner struct {
	journal  *nativeLaunchJournal
	instance string
	record   nativeLaunchRecord
	checked  bool
	failed   bool
	cause    error
}

func (r *nativeRegionalInspectionRunner) Run(_ context.Context, argv []string) error {
	if !r.checked {
		r.checked = true
		var err error
		r.record, err = r.journal.read(r.instance)
		if err != nil {
			return err
		}
	}
	// Stale-object deletion is best effort. Fail the first strict creation
	// batch instead, then permit the fixture's bounded cleanup commands.
	if !r.failed && (strings.Contains(strings.Join(argv, " "), "netns add") || len(argv) > 1 && argv[0] == "ip" && argv[1] == "-batch") {
		r.failed = true
		return r.cause
	}
	return nil
}

func nativeRegionalManagerFixture(t *testing.T) (*Manager, *JailerVMM, *runtimequotahost.Journal, WakeRequest) {
	t.Helper()
	m, v, _ := nativeManagerFixture(t)
	_, host, req := regionalHostFixture(t, &fakeVMM{})
	v.nativeRecovery.journal.bootID = func() (string, error) { return host.Identity().BootID, nil }
	m.WithRuntimeHostJournal(host).WithRuntimeAuthorizer(func(context.Context, RuntimeAdmission) error { return nil })
	return m, v, host, req
}

func TestNativeRegionalManagerPersistsSourceBeforeNetworkForEveryPurpose(t *testing.T) {
	for _, purpose := range []string{runtimequota.CapacityServing, runtimequota.CapacityWarm, runtimequota.CapacityPrime, runtimequota.CapacityAppTask} {
		t.Run(purpose, func(t *testing.T) {
			m, v, host, req := nativeRegionalManagerFixture(t)
			req.RegionalRuntime.Permit.Kind = purpose
			switch purpose {
			case runtimequota.CapacityWarm:
				req.KeepPaused, req.Snapshot, req.RegionalRuntime.Seat = true, usableSnapshot(), nil
			case runtimequota.CapacityPrime:
				req.PrimeOnly, req.RegionalRuntime.Seat = true, nil
			case runtimequota.CapacityAppTask:
				req.AppTaskOnly, req.RegionalRuntime.Seat = true, nil
			}
			runner := &nativeRegionalInspectionRunner{journal: v.nativeRecovery.journal, instance: req.Instance, cause: errors.New("network rejected after inspection")}
			m.run = runner
			if _, err := m.Wake(t.Context(), req); !errors.Is(err, runner.cause) {
				t.Fatal("network fixture did not observe owned preparation", err)
			}
			if !runner.checked || runner.record.Authorized || runner.record.Version != 2 || !sameOptionalRuntimePlacement(runner.record.RegionalRuntime, req.RegionalRuntime) {
				t.Fatal("network effects lacked original regional binding")
			}
			rows, err := host.PermitInventory()
			if err != nil || len(rows) != 1 || !rows[0].Registered || rows[0].Permit != req.RegionalRuntime.Permit {
				t.Fatal("failed network lost physical hold", err)
			}
			if m.alloc.InUse() != 0 || len(m.LiveInstances()) != 0 {
				t.Fatal("portable confirmed empty setup retained serving/allocator state")
			}
		})
	}
}

func TestNativeRegionalWriteFailureRetainsUnattestedOwnership(t *testing.T) {
	m, v, host, req := nativeRegionalManagerFixture(t)
	cause := errors.New("regional prepared frame fsync uncertain")
	v.nativeRecovery.journal.writeRecord = func(_ string, r nativeLaunchRecord) error {
		if r.Version != 2 || !sameOptionalRuntimePlacement(r.RegionalRuntime, req.RegionalRuntime) {
			t.Fatal("prepared write omitted original placement")
		}
		return cause
	}
	if _, err := m.Wake(t.Context(), req); !errors.Is(err, cause) {
		t.Fatal("uncertain preparation acknowledged", err)
	}
	if m.alloc.InUse() != 1 || m.pendingCleanup[req.Instance] == nil || len(m.LiveInstances()) != 0 || len(m.run.(*fakeRunner).commands) != 0 {
		t.Fatal("uncertain write released unattested ownership or reached effects")
	}
	if rows, err := host.PermitInventory(); err != nil || len(rows) != 1 || !rows[0].Registered {
		t.Fatal("uncertain preparation dropped quota ownership", err)
	}
}

func TestNativeRegionalRecoveryRequiresExactRetainedQuotaBinding(t *testing.T) {
	for _, scenario := range []string{"bound", "fenced", "missing-host", "missing-permit", "changed-permit", "changed-seat", "legacy", "different-boot"} {
		t.Run(scenario, func(t *testing.T) {
			m, v, host, req := nativeRegionalManagerFixture(t)
			p := req.RegionalRuntime.Clone()
			if scenario != "missing-permit" {
				finish, err := host.BeginLaunch(t.Context(), p)
				if err != nil {
					t.Fatal(err)
				}
				finish()
			}
			if scenario == "changed-permit" {
				p.Permit.RunID = uuid.NewString()
				p.Seat.Permit = p.Permit
			}
			if scenario == "changed-seat" {
				p.Seat.ID = uuid.NewString()
			}
			lease := leaseForSlot(req.Instance, 3)
			lease.Plan = req.Plan
			placement := &p
			if scenario == "legacy" {
				placement = nil
			}
			if err := v.nativeRecovery.journal.prepareWithPlacement(t.Context(), lease, placement); err != nil {
				t.Fatal(err)
			}
			if scenario == "fenced" {
				if _, err := host.FencePermit(p.Permit, uuid.NewString()); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "missing-host" {
				m.WithRuntimeHostJournal(nil)
			}
			if scenario == "different-boot" {
				v.nativeRecovery.journal.bootID = func() (string, error) { return uuid.NewString(), nil }
			}
			err := m.RecoverNativeProcesses(t.Context())
			valid := scenario == "bound" || scenario == "fenced"
			if (err == nil) != valid || m.nativeRecoveryReady != valid {
				t.Fatal("regional recovery readiness differs from original ownership", err)
			}
			if valid && (m.alloc.InUse() != 1 || len(m.nativeRecovered[req.Instance]) == 0) {
				t.Fatal("recovery failed to quarantine retained source")
			}
			if len(m.LiveInstances()) != 0 || len(m.cidToID) != 0 {
				t.Fatal("recovery inferred serving from physical bookkeeping")
			}
		})
	}
}

func TestRegionalUnsupportedNativeCaptureRefusesBeforeHostMutation(t *testing.T) {
	for _, nativeRecovery := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy-native", true: "journaled-native"}[nativeRecovery], func(t *testing.T) {
			fake := &fakeVMM{}
			m, host, req := regionalHostFixture(t, fake)
			inst, err := m.Wake(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			jailer := NewJailerVMM(t.TempDir(), 0)
			if nativeRecovery {
				jailer.WithNativeProcessRecovery()
			}
			m.vmm = jailer
			if err := m.PreflightRegionalMigration(t.Context(), req.Instance, req.RegionalRuntime.Clone()); !errors.Is(err, state.ErrConflict) {
				t.Fatal("unsupported preflight accepted", err)
			}
			_, err = m.SnapshotKeepAlive(t.Context(), req.Instance, SnapshotSpec{MigrationLeaseToken: regionalMigrationToken, RegionalMigrationSource: req.RegionalRuntime, StorageKey: "snap/source/mem", VMStateStorageKey: "snap/source/vmstate"})
			if !errors.Is(err, state.ErrConflict) || admissionForInstance(inst).RegionalResumePending || inst.Paused {
				t.Fatal("unsupported capture mutated serving state", err)
			}
			if err := host.Check(*req.RegionalRuntime); err != nil {
				t.Fatal("unsupported capture fenced serving seat", err)
			}
			if rows, err := host.MigrationInventory(); err != nil || len(rows) != 0 {
				t.Fatal("unsupported capture retained false source", err)
			}
			m.vmm = fake
			if err := m.Destroy(t.Context(), req.Instance); err != nil {
				t.Fatal(err)
			}
		})
	}
}
