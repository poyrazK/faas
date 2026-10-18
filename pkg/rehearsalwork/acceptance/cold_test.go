package acceptance

import (
	"github.com/onebox-faas/faas/pkg/actioncompare"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalcold"
	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
	"strings"
	"testing"
)

func (f *fixture) coldPrepare(t *testing.T, name string) rehearsalcold.Preparation {
	t.Helper()
	ctx := t.Context()
	id := f.identities[name]
	if id.InstanceID == "" {
		store := f.store.(rehearsalcold.Store)
		p, err := store.BeginScenarioColdPreparation(ctx, f.account.ID, id.AppID)
		if err != nil {
			t.Fatal(err)
		}
		if p.State != "retired" || p.InstanceID != "" || p.RecorderSessionID != "" {
			t.Fatal("no-VM preparation invented a retirement receipt")
		}
		if err = store.CompleteScenarioColdPreparation(ctx, f.account.ID, id.AppID); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if err := f.store.UpdateInstanceState(ctx, id.InstanceID, "running"); err != nil {
		t.Fatal(err)
	}
	store := f.store.(rehearsalcold.Store)
	p, err := store.BeginScenarioColdPreparation(ctx, f.account.ID, id.AppID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ClaimScenarioColdPreparation(ctx, id.AppID); err != nil {
		t.Fatal(err)
	}
	owner := f.store.(rehearsalrecorder.ColdStore)
	if err = owner.FenceScenarioColdRetirement(ctx, id, p.ID, f.recorders[name]); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.(rehearsalrecorder.Store).BeginScenarioRecorderRequest(ctx, id, f.recorders[name]); err == nil {
		t.Fatal("fenced old VM admitted effects")
	}
	if err = owner.CompleteScenarioColdRetirement(ctx, id, p.ID, f.recorders[name]); err != nil {
		t.Fatal(err)
	}
	if err = store.CompleteScenarioColdPreparation(ctx, f.account.ID, id.AppID); err == nil {
		t.Fatal("preparation completed before scheduler retired instance")
	}
	if err = f.store.UpdateInstanceState(ctx, id.InstanceID, "stopped"); err != nil {
		t.Fatal(err)
	}
	if err = store.CompleteScenarioColdPreparation(ctx, f.account.ID, id.AppID); err != nil {
		t.Fatal(err)
	}
	return p
}
func (f *fixture) coldBoot(t *testing.T, name string, ms int64) {
	t.Helper()
	ctx := t.Context()
	id := f.identities[name]
	nodeID := f.nodeID
	if id.InstanceID != "" {
		old, err := f.store.InstanceByID(ctx, id.InstanceID)
		if err != nil {
			t.Fatal(err)
		}
		nodeID = old.NodeID
	}
	i, err := f.store.CreateInstance(ctx, id.AppID, id.DeploymentID, "cold_booting", 128, nodeID, "")
	if err != nil {
		t.Fatal(err)
	}
	id.InstanceID = i.ID
	r, err := f.store.(rehearsalrecorder.Store).StartScenarioRecorder(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	owner := f.store.(rehearsalrecorder.ColdStore)
	if err = owner.RecordScenarioColdBoot(ctx, id, r, "restore", ms); err == nil {
		t.Fatal("restore substituted for cold boot")
	}
	if err = owner.RecordScenarioColdBoot(ctx, id, r, "cold_boot", ms); err != nil {
		t.Fatal(err)
	}
	f.identities[name], f.recorders[name] = id, r
}

func TestColdStartRetirementBootAndEffectComparison(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			run := func(duplicate bool, ms int64) api.ScenarioActionSnapshot {
				f := newFixtureWithCold(t, backend, false, true)
				for _, name := range []string{"primary", "worker", "reader", "unbound"} {
					f.coldPrepare(t, name)
					f.coldBoot(t, name, ms)
				}
				j := f.claim(t, f.enqueue(t).ID)
				f.notify(t)
				if duplicate {
					f.notify(t)
				}
				f.complete(t, j)
				s := f.finish(t, "")
				if s.Status != "complete" || s.ColdStart == nil || s.ColdStart.Validate(s.Workloads) != nil {
					t.Fatalf("cold evidence incomplete: %+v", s)
				}
				return s
			}
			wrap := func(s api.ScenarioActionSnapshot) *actioncompare.Evidence {
				return &actioncompare.Evidence{Version: 1, Status: "complete", Provenance: actioncompare.SimulationProvenance, ContractSHA256: strings.Repeat("a", 64), FixtureSHA256: strings.Repeat("b", 64), Snapshot: &s}
			}
			baseline := wrap(run(false, 100))
			for _, tc := range []struct {
				name      string
				duplicate bool
				ms        int64
				want      string
			}{{"same_behavior", false, 200, "passed"}, {"duplicate_notification", true, 200, "failed"}, {"startup_budget", false, 120001, "failed"}} {
				t.Run(tc.name, func(t *testing.T) {
					r := actioncompare.Compare(baseline, wrap(run(tc.duplicate, tc.ms)))
					if r.Status != tc.want {
						t.Fatalf("want %s: %+v", tc.want, r)
					}
				})
			}
		})
	}
}

func TestColdStartRefusesUnsettledEffectsAndMissingBootProof(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend+"/unsettled", func(t *testing.T) {
			f := newFixtureWithCold(t, backend, false, true)
			id := f.identities["worker"]
			if err := f.store.UpdateInstanceState(t.Context(), id.InstanceID, "running"); err != nil {
				t.Fatal(err)
			}
			_ = f.workerRequest(t)
			if _, err := f.store.(rehearsalcold.Store).BeginScenarioColdPreparation(t.Context(), f.account.ID, id.AppID); err == nil {
				t.Fatal("unsettled outbound obligation was discarded")
			}
		})
		t.Run(backend+"/missing_boot", func(t *testing.T) {
			f := newFixtureWithCold(t, backend, false, true)
			for _, name := range []string{"primary", "worker", "reader", "unbound"} {
				f.coldPrepare(t, name)
				if name != "reader" {
					f.coldBoot(t, name, 100)
				}
			}
			j := f.claim(t, f.enqueue(t).ID)
			f.complete(t, j)
			if s := f.finish(t, ""); s.Status == "complete" || s.ColdStart == nil || s.ColdStart.Status == "complete" {
				t.Fatalf("missing boot passed: %+v", s)
			}
		})
	}
}

func TestColdStartFirstBootNeedsNoInventedPriorOwner(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newFeatureFixture(t, backend, false, fixtureFeatures{cold: true, unbooted: true})
			for _, name := range []string{"primary", "worker", "reader", "unbound"} {
				f.coldPrepare(t, name)
				f.coldBoot(t, name, 100)
			}
			j := f.claim(t, f.enqueue(t).ID)
			f.complete(t, j)
			s := f.finish(t, "")
			if s.Status != "complete" || s.ColdStart == nil || len(s.ColdStart.Preparations) != 4 {
				t.Fatalf("first cold boot missing proof: %+v", s)
			}
			for _, p := range s.ColdStart.Preparations {
				if p.InstanceID != "" || p.RecorderSessionID != "" || p.BootInstanceID == "" {
					t.Fatal("unbooted preview invented old lifecycle evidence")
				}
			}
		})
	}
}

func TestColdStartSQLProofAndEnrollmentAreRetainedAndImmutable(t *testing.T) {
	f := newFixtureWithCold(t, "postgres", false, true)
	id := f.identities["worker"]
	if err := f.store.UpdateInstanceState(t.Context(), id.InstanceID, "running"); err != nil {
		t.Fatal(err)
	}
	p, err := f.store.(rehearsalcold.Store).BeginScenarioColdPreparation(t.Context(), f.account.ID, id.AppID)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []struct {
		sql string
		arg string
	}{
		{"DELETE FROM scenario_cold_preparations WHERE id=$1", p.ID},
		{"UPDATE scenario_cold_preparations SET state='ready' WHERE id=$1", p.ID},
		{"UPDATE scenario_cold_preparations SET instance_id=gen_random_uuid() WHERE id=$1", p.ID},
		{"UPDATE scenario_test_action_policies SET cold_start_spec=jsonb_set(cold_start_spec,'{within_ms}','1') WHERE app_id=$1", p.AppID},
		{"UPDATE scenario_recorder_sessions SET status='closed' WHERE id=$1", p.RecorderSessionID},
	} {
		if _, err = f.pool.Exec(t.Context(), query.sql, query.arg); err == nil {
			t.Fatalf("raw write discarded or forged lifecycle evidence: %s", query.sql)
		}
	}
}
