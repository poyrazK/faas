//go:build linux || darwin

package fcvm

// adr: 765
// adr: 787
import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/runtimequotahost"
	"github.com/onebox-faas/faas/pkg/state"
)

func preparedNativeRegionalResources(t *testing.T) (*nativeLaunchJournal, Lease, runtimequota.Placement, state.RegionalRuntimeResources) {
	t.Helper()
	_, _, req := regionalHostFixture(t, &fakeVMM{})
	lease := leaseForSlot(req.Instance, 3)
	lease.Plan, lease.MemoryMaxMiB, lease.CPUMillicores = req.Plan, req.MemSizeMiB, 250
	resources := state.RegionalRuntimeResources{RAMMB: req.MemSizeMiB, VCPU: req.VcpuCount, CPUMillicores: api.DefaultAppCPUMillicores}
	j := nativeJournalFixture(filepath.Join(t.TempDir(), "journal"))
	if err := j.prepareWithRegionalResources(t.Context(), lease, req.RegionalRuntime, &resources); err != nil {
		t.Fatal(err)
	}
	return j, lease, req.RegionalRuntime.Clone(), resources
}

func TestNativeRegionalResourcesSurviveAuthorizationAndRestart(t *testing.T) {
	j, lease, _, resources := preparedNativeRegionalResources(t)
	prepared, err := j.read(lease.Instance)
	if err != nil || prepared.Version != 3 || !sameNativeRegionalResources(prepared.RegionalResources, &resources) {
		t.Fatal("prepared record lost resources", err)
	}
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
	resources.VCPU++
	restarted := nativeJournalFixture(j.root)
	records, err := restarted.records(t.Context())
	if err != nil || len(records) != 1 || records[0].PID != 42 || records[0].StartTime != 101 || !sameNativeRegionalBinding(records[0], prepared) || *records[0].RegionalResources == resources {
		t.Fatal("restart changed authorized footprint", err)
	}
}

func TestNativeRegionalResourceRecordRejectsDamagedClaims(t *testing.T) {
	for _, scenario := range []string{"missing", "null", "missing-vcpu", "duplicate-object", "duplicate-vcpu", "unknown", "zero", "negative", "fraction", "overflow", "wrong-ram", "wrong-cpu", "old-version", "no-placement"} {
		t.Run(scenario, func(t *testing.T) {
			j, lease, _, _ := preparedNativeRegionalResources(t)
			r, err := j.read(lease.Instance)
			if err != nil {
				t.Fatal(err)
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
				delete(fields, "regional_resources")
			case "null":
				fields["regional_resources"] = json.RawMessage("null")
			case "missing-vcpu":
				fields["regional_resources"] = json.RawMessage(`{"RAMMB":128,"CPUMillicores":1000}`)
			case "duplicate-vcpu":
				fields["regional_resources"] = json.RawMessage(`{"RAMMB":128,"VCPU":2,"VCPU":2,"CPUMillicores":1000}`)
			case "unknown":
				fields["regional_resources"] = json.RawMessage(`{"RAMMB":128,"VCPU":2,"CPUMillicores":1000,"extra":true}`)
			case "zero", "negative", "fraction", "overflow":
				value := map[string]string{"zero": "0", "negative": "-1", "fraction": "1.5", "overflow": "2147483648"}[scenario]
				fields["regional_resources"] = json.RawMessage(`{"RAMMB":128,"VCPU":` + value + `,"CPUMillicores":1000}`)
			case "wrong-ram":
				fields["regional_resources"] = json.RawMessage(`{"RAMMB":129,"VCPU":2,"CPUMillicores":1000}`)
			case "wrong-cpu":
				fields["regional_resources"] = json.RawMessage(`{"RAMMB":128,"VCPU":2,"CPUMillicores":249}`)
			case "old-version":
				fields["version"] = json.RawMessage("2")
			case "no-placement":
				fields["regional_runtime"] = json.RawMessage("null")
			}
			raw, err = json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "duplicate-object" {
				raw = []byte(strings.TrimSuffix(string(raw), "}") + `,"regional_resources":` + string(fields["regional_resources"]) + "}")
			}
			if err := os.WriteFile(j.path(lease.Instance), raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := j.read(lease.Instance); err == nil {
				t.Fatal("damaged footprint became native ownership")
			}
		})
	}
}

func TestNativeRegionalResourcesRemainImmutableAcrossFallbackAndRetirement(t *testing.T) {
	j, lease, p, resources := preparedNativeRegionalResources(t)
	old, err := j.retire(t.Context(), lease.Instance, nativeProcessRetirer{probe: nativeProcessProbe{root: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	changed := resources
	changed.VCPU++
	if _, err := j.replaceWithRegionalResources(t.Context(), lease, old.Generation, false, &p, &changed); err == nil {
		t.Fatal("fallback changed guest vCPU")
	}
	if _, err := os.Stat(filepath.Join(j.root, "retired", old.Generation+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("refused replacement mutated the archive", err)
	}
	fresh, err := j.replace(t.Context(), lease, old.Generation, false)
	if err != nil || fresh.Version != 3 || !sameNativeRegionalResources(fresh.RegionalResources, &resources) {
		t.Fatal("fallback lost original footprint", err)
	}
	archived, err := readNativeLaunchRecord(filepath.Join(j.root, "retired", old.Generation+".json"), lease.Instance)
	if err != nil || !sameNativeRegionalBinding(archived, old) {
		t.Fatal("archive changed original footprint", err)
	}
	retired, err := j.retire(t.Context(), lease.Instance, nativeProcessRetirer{probe: nativeProcessProbe{root: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	wrong := retired
	wrong.RegionalResources = &changed
	if err := j.confirmExit(t.Context(), wrong); err == nil {
		t.Fatal("different footprint acknowledged exit")
	}
	if err := j.confirmResourcesRemoved(t.Context(), wrong); err == nil {
		t.Fatal("different footprint acknowledged resource removal")
	}
	if err := j.confirmResourcesRemoved(t.Context(), retired); err != nil {
		t.Fatal(err)
	}
	if _, err := j.replaceWithPlacement(t.Context(), lease, retired.Generation, true, &p); err == nil {
		t.Fatal("new generation downgraded known resources")
	}
	next := p.Clone()
	next.Permit.ID, next.Permit.RunID = uuid.NewString(), uuid.NewString()
	next.Seat.ID, next.Seat.Permit = uuid.NewString(), next.Permit
	lease.MemoryMaxMiB++
	changed.RAMMB++
	if r, err := j.replaceWithRegionalResources(t.Context(), lease, retired.Generation, true, &next, &changed); err != nil || r.Generation == retired.Generation || !sameNativeRegionalResources(r.RegionalResources, &changed) {
		t.Fatal("fully retired new launch could not retain its own footprint", err)
	}
}

func TestNativeRegionalResourceRecoveryPreservesHistoricalCPUProfile(t *testing.T) {
	j, lease, p, _ := preparedNativeRegionalResources(t)
	r, err := j.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	// Model a retained launch from a lower legacy plan ceiling. Only a new
	// producer can resolve today's zero quota; recovery retains the old peak.
	lease.CPUMillicores = 0
	r.Lease = lease
	r.RegionalResources.CPUMillicores = api.DefaultAppCPUMillicores / 2
	if err := j.write(r); err != nil {
		t.Fatal(err)
	}
	if err := validateNativeRegionalResources(lease, &p, r.RegionalResources); !errors.Is(err, runtimequotahost.ErrBinding) {
		t.Fatal("new launch accepted an obsolete effective CPU profile", err)
	}
	if ticket, err := j.beginLaunch(t.Context(), lease); !errors.Is(err, runtimequotahost.ErrBinding) || ticket != nil {
		t.Fatal("original prepared generation launched above its retained CPU ceiling", err)
	}
	retired, err := j.retire(t.Context(), lease.Instance, nativeProcessRetirer{probe: nativeProcessProbe{root: t.TempDir()}})
	if err != nil {
		t.Fatal("recovery reinterpreted the retained CPU ceiling", err)
	}
	if _, err := j.replace(t.Context(), lease, retired.Generation, false); !errors.Is(err, runtimequotahost.ErrBinding) {
		t.Fatal("fallback launched above the retained CPU ceiling", err)
	}
	unchanged, err := j.read(lease.Instance)
	if err != nil || unchanged.Generation != retired.Generation || !sameNativeRegionalResources(unchanged.RegionalResources, r.RegionalResources) {
		t.Fatal("refused fallback reinterpreted retained ownership", err)
	}
}

func TestNativeHistoricalRegionalResourcesCannotBeBackfilled(t *testing.T) {
	j, lease, p := preparedNativeRegionalJournal(t)
	old, err := j.retire(t.Context(), lease.Instance, nativeProcessRetirer{probe: nativeProcessProbe{root: t.TempDir()}})
	if err != nil || old.RegionalResources != nil {
		t.Fatal("historical record acquired invented resources", err)
	}
	resources, err := nativeRegionalResources(lease, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.replaceWithRegionalResources(t.Context(), lease, old.Generation, false, &p, &resources); err == nil {
		t.Fatal("historical fallback backfilled guest vCPU")
	}
	fresh, err := j.replace(t.Context(), lease, old.Generation, false)
	if err != nil || fresh.Version != 2 || fresh.RegionalResources != nil {
		t.Fatal("historical fallback invented a footprint", err)
	}
}

func TestNativeRegionalResourceProducerRejectsMismatchBeforeMutation(t *testing.T) {
	for _, dimension := range []string{"ram", "cpu", "vcpu", "placement"} {
		t.Run(dimension, func(t *testing.T) {
			_, v, _, req := nativeRegionalManagerFixture(t)
			lease := leaseForSlot(req.Instance, 3)
			lease.Plan, lease.MemoryMaxMiB, lease.CPUMillicores = req.Plan, req.MemSizeMiB, 250
			resources := state.RegionalRuntimeResources{RAMMB: req.MemSizeMiB, VCPU: 2, CPUMillicores: api.DefaultAppCPUMillicores}
			switch dimension {
			case "ram":
				resources.RAMMB++
			case "cpu":
				resources.CPUMillicores = 250
			case "vcpu":
				resources.VCPU = 0
			case "placement":
				req.RegionalRuntime.Permit.InstanceID = uuid.NewString()
			}
			source := runtimequota.LaunchSource{Permit: req.RegionalRuntime.Permit, InstanceKey: req.Instance}
			if err := v.prepareNativeRegionalSource(t.Context(), lease, *req.RegionalRuntime, resources, source); !errors.Is(err, runtimequotahost.ErrBinding) {
				t.Fatal("invalid resources reached producer", err)
			}
			if _, err := os.Stat(v.nativeRecovery.journal.path(lease.Instance)); !errors.Is(err, os.ErrNotExist) || v.nativeRecovery.generation(lease.Instance) != "" {
				t.Fatal("refused preparation retained invented ownership", err)
			}
		})
	}
}

func TestNativeRegionalResourcesRetainActualCPUCeilingAndOptOut(t *testing.T) {
	for _, plan := range []api.Plan{api.PlanFree, api.PlanHobby, api.PlanPro, api.PlanScale} {
		for _, disabled := range []bool{false, true} {
			for _, configured := range []int{0, 250, api.DefaultAppCPUMillicores} {
				lease := Lease{Plan: plan, MemoryMaxMiB: 224, CPUMillicores: configured, DisableStartupCPUBoost: disabled}
				resources, err := nativeRegionalResources(lease, 2)
				ceiling := int(int64(plan.CPUQuotaUS()) * 1000 / int64(plan.CPUPeriodUS()))
				cpu := configured
				if cpu == 0 {
					cpu = ceiling
				}
				if !disabled {
					cpu = max(cpu, min(api.DefaultAppCPUMillicores, ceiling))
				}
				if err != nil || resources != (state.RegionalRuntimeResources{RAMMB: 224, VCPU: 2, CPUMillicores: cpu}) {
					t.Fatalf("plan=%s disabled=%t configured=%d resources=%+v err=%v", plan, disabled, configured, resources, err)
				}
			}
		}
	}
}
