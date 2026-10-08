//go:build linux || darwin

package fcvm

// adr: 787
import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/runtimequotahost"
	"github.com/onebox-faas/faas/pkg/state"
)

func preparedNativeRegionalSource(t *testing.T) (*nativeLaunchJournal, Lease, runtimequota.Placement, state.RegionalRuntimeResources, runtimequota.LaunchSource) {
	t.Helper()
	_, _, req := regionalHostFixture(t, &fakeVMM{})
	req.AppTaskID, req.AppTaskLeaseToken = uuid.NewString(), req.RegionalRuntime.Permit.RunID
	req.Instance, _ = runtimequota.AppTaskInstanceID(req.AppTaskID, req.AppTaskLeaseToken)
	req.AppTaskOnly = true
	req.RegionalRuntime.Permit.InstanceID, req.RegionalRuntime.Permit.Kind = req.Instance, runtimequota.CapacityAppTask
	req.RegionalRuntime.Seat = nil
	lease := leaseForSlot(req.Instance, 3)
	lease.Plan, lease.MemoryMaxMiB, lease.CPUMillicores = req.Plan, req.MemSizeMiB, 250
	resources, err := nativeRegionalResources(lease, req.VcpuCount)
	if err != nil {
		t.Fatal(err)
	}
	source, err := nativeRegionalWakeSource(lease, req)
	if err != nil {
		t.Fatal(err)
	}
	j := nativeJournalFixture(filepath.Join(t.TempDir(), "journal"))
	if err := j.prepareWithRegionalSource(t.Context(), lease, req.RegionalRuntime, &resources, &source); err != nil {
		t.Fatal(err)
	}
	return j, lease, req.RegionalRuntime.Clone(), resources, source
}

func TestNativeRegionalSourceSurvivesAuthorizationAndRestart(t *testing.T) {
	j, lease, _, _, source := preparedNativeRegionalSource(t)
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
	records, err := restarted.records(t.Context())
	if err != nil || len(records) != 1 || records[0].Version != 4 || !records[0].Authorized || records[0].PID != 42 || !sameNativeRegionalSource(records[0].RegionalSource, &source) {
		t.Fatal("native restart lost original task or claim", err)
	}
	observation := newNativeRuntimeObservation(records[0])
	copy := cloneNativeRuntimeInventory(NativeRuntimeInventory{Owners: []NativeRuntimeObservation{observation}})
	copy.Owners[0].OriginalSource.TaskID = uuid.NewString()
	if !sameNativeRegionalSource(observation.OriginalSource, &source) || !sameNativeRegionalSource(records[0].RegionalSource, &source) {
		t.Fatal("advisory source aliases original native ownership")
	}
}

func TestNativeRegionalSourceFallbackAndRetirementKeepOriginalClaim(t *testing.T) {
	j, lease, p, resources, source := preparedNativeRegionalSource(t)
	old, err := j.retire(t.Context(), lease.Instance, nativeProcessRetirer{probe: nativeProcessProbe{root: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	wrong := source
	wrong.InstanceKey = strings.ReplaceAll(source.InstanceKey, "-", "")
	if _, err := j.replaceWithRegionalSource(t.Context(), lease, old.Generation, false, &p, &resources, &wrong); err == nil {
		t.Fatal("fallback changed actual native lookup spelling")
	}
	fresh, err := j.replace(t.Context(), lease, old.Generation, false)
	if err != nil || fresh.Version != 4 || fresh.Generation == old.Generation || !sameNativeRegionalSource(fresh.RegionalSource, &source) {
		t.Fatal("fallback lost original task or claim", err)
	}
	archived, err := readNativeLaunchRecord(filepath.Join(j.root, "retired", old.Generation+".json"), lease.Instance)
	if err != nil || !sameNativeRegionalBinding(archived, old) {
		t.Fatal("archive lost original source", err)
	}
	retired, err := j.retire(t.Context(), lease.Instance, nativeProcessRetirer{probe: nativeProcessProbe{root: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	changed := retired
	changed.RegionalSource = &wrong
	if j.confirmExit(t.Context(), changed) == nil || j.confirmResourcesRemoved(t.Context(), changed) == nil {
		t.Fatal("substituted source acknowledged physical absence")
	}
	if err := j.confirmResourcesRemoved(t.Context(), retired); err != nil {
		t.Fatal(err)
	}
	if _, err := j.replaceWithRegionalResources(t.Context(), lease, retired.Generation, true, &p, &resources); err == nil {
		t.Fatal("new generation downgraded retained source to unknown")
	}
}

func TestNativeRegionalSourceRejectsDamagedJournal(t *testing.T) {
	for _, scenario := range []string{"missing", "null", "duplicate", "nested-duplicate", "unknown", "missing-task", "other-task", "other-claim", "lookup-spelling", "downgrade"} {
		t.Run(scenario, func(t *testing.T) {
			j, lease, _, _, source := preparedNativeRegionalSource(t)
			original, err := j.read(lease.Instance)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(original)
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "missing":
				delete(fields, "regional_source")
			case "null":
				fields["regional_source"] = json.RawMessage("null")
			case "nested-duplicate":
				fields["regional_source"] = []byte(strings.TrimSuffix(string(fields["regional_source"]), "}") + `,"TaskID":"` + source.TaskID + `"}`)
			case "unknown":
				fields["regional_source"] = []byte(strings.TrimSuffix(string(fields["regional_source"]), "}") + `,"extra":true}`)
			case "missing-task", "other-task", "other-claim", "lookup-spelling":
				if scenario == "missing-task" {
					source.TaskID = ""
				} else if scenario == "other-task" {
					source.TaskID = uuid.NewString()
				} else if scenario == "other-claim" {
					source.Permit.RunID = uuid.NewString()
				} else {
					source.InstanceKey = strings.ReplaceAll(source.InstanceKey, "-", "")
				}
				fields["regional_source"], _ = json.Marshal(source)
			case "downgrade":
				fields["version"] = json.RawMessage("3")
			}
			raw, _ = json.Marshal(fields)
			if scenario == "duplicate" {
				raw = []byte(strings.TrimSuffix(string(raw), "}") + `,"regional_source":` + string(fields["regional_source"]) + "}")
			}
			if err := os.WriteFile(j.path(lease.Instance), raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := j.read(lease.Instance); err == nil {
				t.Fatal("damaged journal supplied original launch authority")
			}
		})
	}
}

func TestNativeRegionalHistoricalSourceCannotBeBackfilled(t *testing.T) {
	j, lease, p, resources := preparedNativeRegionalResources(t)
	old, err := j.retire(t.Context(), lease.Instance, nativeProcessRetirer{probe: nativeProcessProbe{root: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	source := runtimequota.LaunchSource{Permit: p.Permit, InstanceKey: lease.Instance}
	if _, err := j.replaceWithRegionalSource(t.Context(), lease, old.Generation, false, &p, &resources, &source); err == nil {
		t.Fatal("historical source was invented during fallback")
	}
	fresh, err := j.replace(t.Context(), lease, old.Generation, false)
	if err != nil || fresh.Version != 3 || fresh.RegionalSource != nil {
		t.Fatal("historical fallback acquired launch source", err)
	}
}

func TestNativeRegionalWakeSourceRequiresOriginalTaskClaim(t *testing.T) {
	for _, scenario := range []string{"missing-task", "other-task", "other-claim", "not-task-only", "task-on-serving"} {
		t.Run(scenario, func(t *testing.T) {
			_, _, req := regionalHostFixture(t, &fakeVMM{})
			req.AppTaskID, req.AppTaskLeaseToken = uuid.NewString(), req.RegionalRuntime.Permit.RunID
			req.Instance, _ = runtimequota.AppTaskInstanceID(req.AppTaskID, req.AppTaskLeaseToken)
			req.AppTaskOnly, req.RegionalRuntime.Seat = true, nil
			req.RegionalRuntime.Permit.InstanceID, req.RegionalRuntime.Permit.Kind = req.Instance, runtimequota.CapacityAppTask
			switch scenario {
			case "missing-task":
				req.AppTaskID = ""
			case "other-task":
				req.AppTaskID = uuid.NewString()
			case "other-claim":
				req.AppTaskLeaseToken = uuid.NewString()
			case "not-task-only":
				req.AppTaskOnly = false
			case "task-on-serving":
				req.RegionalRuntime.Permit.Kind = runtimequota.CapacityServing
			}
			if _, err := nativeRegionalWakeSource(Lease{Instance: req.Instance}, req); err == nil {
				t.Fatal("substituted task claim supplied original source")
			}
		})
	}
}

func TestNativeRegionalSourceRejectsProducerMismatchBeforeMutation(t *testing.T) {
	_, v, _, req := nativeRegionalManagerFixture(t)
	lease := leaseForSlot(req.Instance, 3)
	lease.Plan, lease.MemoryMaxMiB = req.Plan, req.MemSizeMiB
	resources, err := nativeRegionalResources(lease, req.VcpuCount)
	if err != nil {
		t.Fatal(err)
	}
	source := runtimequota.LaunchSource{Permit: req.RegionalRuntime.Permit, InstanceKey: strings.ReplaceAll(req.Instance, "-", "")}
	if err := v.prepareNativeRegionalSource(t.Context(), lease, *req.RegionalRuntime, resources, source); err == nil {
		t.Fatal("inexact source reached native producer")
	}
	if _, err := os.Stat(v.nativeRecovery.journal.path(lease.Instance)); !errors.Is(err, os.ErrNotExist) || v.nativeRecovery.generation(lease.Instance) != "" {
		t.Fatal("refused source mutated native ownership", err)
	}
}

func TestNativeRegionalMissingTaskSourcePrecedesLaunchRegistration(t *testing.T) {
	m, v, host, req := nativeRegionalManagerFixture(t)
	req.RegionalRuntime.Permit.Kind, req.RegionalRuntime.Seat, req.AppTaskOnly = runtimequota.CapacityAppTask, nil, true
	if _, err := m.Wake(t.Context(), req); err == nil {
		t.Fatal("missing original task reached native wake")
	}
	rows, err := host.PermitInventory()
	if err != nil || len(rows) != 0 || m.alloc.InUse() != 0 || len(m.run.(*fakeRunner).commands) != 0 {
		t.Fatal("missing source reached launch registration or host effects", err)
	}
	if _, err := os.Stat(v.nativeRecovery.journal.path(req.Instance)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing source created native ownership", err)
	}
}

func TestNativeRegionalTaskControlRequiresCapturedOriginalSource(t *testing.T) {
	m, v, _, _ := nativeRegionalManagerFixture(t)
	j, lease, p, _, source := preparedNativeRegionalSource(t)
	owner, err := j.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	owner.Authorized, owner.PID, owner.StartTime = true, 42, 101
	if err := j.write(owner); err != nil {
		t.Fatal(err)
	}
	v.nativeRecovery.journal = j
	v.nativeRecovery.remember(owner)
	newInstance := func() *Instance {
		return &Instance{Lease: lease, nativeGeneration: owner.Generation, AppTaskOnly: true, AppTaskID: source.TaskID, AppTaskLeaseToken: source.Permit.RunID}
	}
	if err := m.checkNativeRegionalInstance(newInstance(), p); err != nil {
		t.Fatal("original task source did not bind", err)
	}
	for _, change := range []func(*Instance){
		func(i *Instance) { i.AppTaskID = uuid.NewString() },
		func(i *Instance) { i.AppTaskLeaseToken = uuid.NewString() },
		func(i *Instance) { i.AppTaskOnly = false },
	} {
		inst := newInstance()
		change(inst)
		if err := m.checkNativeRegionalInstance(inst, p); err == nil {
			t.Fatal("current task metadata borrowed original native source")
		}
	}
}

func TestNativeRegionalTaskCommandWithoutSourceRetainsOwnership(t *testing.T) {
	m, v, _, _ := nativeRegionalManagerFixture(t)
	j, lease, p, _, source := preparedNativeRegionalSource(t)
	owner, err := j.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	// Model a retained historical frame with no original native task source.
	owner.Version, owner.RegionalSource = 3, nil
	owner.Authorized, owner.PID, owner.StartTime = true, 42, 101
	if err := j.write(owner); err != nil {
		t.Fatal(err)
	}
	v.nativeRecovery.journal = j
	v.nativeRecovery.remember(owner)
	inst := &Instance{Lease: lease, nativeGeneration: owner.Generation, regionalRuntime: &p, AppID: p.Permit.Grant.Binding.RegionalAppID, AccountID: p.Permit.Grant.Binding.RegionalAccountID, DeploymentID: p.Permit.DeploymentID, AppTaskOnly: true, AppTaskID: source.TaskID, AppTaskLeaseToken: source.Permit.RunID}
	m.live[lease.Instance] = inst
	authorizations := 0
	m.WithRuntimeAuthorizer(func(context.Context, RuntimeAdmission) error { authorizations++; return nil })
	if _, err := m.ExecuteAppTask(t.Context(), lease.Instance, appTaskProtocolRequest(source.TaskID)); !errors.Is(err, runtimequotahost.ErrBinding) {
		t.Fatal("historical source authorized a task payload")
	}
	retained, err := j.read(lease.Instance)
	if err != nil || authorizations != 0 || !sameNativeObservedOwner(owner, retained) || m.live[lease.Instance] != inst || len(m.run.(*fakeRunner).commands) != 0 {
		t.Fatal("unqualified command reached effects or generic teardown", err)
	}
}
