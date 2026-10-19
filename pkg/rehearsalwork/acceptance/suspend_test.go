package acceptance

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/actioncompare"
	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
	"github.com/onebox-faas/faas/pkg/rehearsalresume"
	"github.com/onebox-faas/faas/pkg/state"
)

func (f *fixture) suspend(t *testing.T, resumeMS int64) rehearsalresume.Receipt {
	t.Helper()
	ctx := t.Context()
	id := f.identities["worker"]
	control := f.store.(rehearsalresume.Store)
	owner := f.store.(rehearsalrecorder.SuspendStore)
	if err := f.store.UpdateInstanceState(ctx, id.InstanceID, "running"); err != nil {
		t.Fatal(err)
	}
	e, err := control.BeginScenarioSuspendResume(ctx, f.account.ID, f.run)
	if err != nil {
		t.Fatal(err)
	}
	r := e.Receipts[0]
	again, err := control.BeginScenarioSuspendResume(ctx, f.account.ID, f.run)
	if err != nil || again.Receipts[0].ID != r.ID {
		t.Fatal("request duplicated pause intent")
	}
	if _, err = control.ClaimScenarioSuspendResume(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.store.UpdateInstanceState(ctx, id.InstanceID, "frozen"); err != nil {
		t.Fatal(err)
	}
	step := func(expected, next string, pause, resume int64) {
		t.Helper()
		if _, err := owner.AdvanceScenarioSuspendResume(ctx, id, f.recorders["worker"], r.ID, expected, next, pause, resume); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = owner.AdvanceScenarioSuspendResume(ctx, id, uuid.NewString(), r.ID, "claimed", "fenced", 0, 0); err == nil {
		t.Fatal("adopted recorder supplied lifecycle proof")
	}
	step("claimed", "fenced", 0, 0)
	if _, err = f.store.(rehearsalrecorder.Store).BeginScenarioRecorderRequest(ctx, id, f.recorders["worker"]); err == nil {
		t.Fatal("paused VM admitted an effect")
	}
	step("fenced", "paused", 0, 0)
	if pg, ok := f.store.(*state.PgStore); ok {
		callers, lookupErr := pg.LiveServiceCallersByHostIP(ctx, "", "10.100.0.6")
		if lookupErr != nil || len(callers) != 0 {
			t.Fatal("paused VM retained dependency caller authority")
		}
	}
	if err = control.CompleteScenarioSuspendResume(ctx, r.ID); err == nil {
		t.Fatal("pause acknowledgement substituted for resume")
	}
	step("paused", "resuming", 1, 0)
	if pg, ok := f.store.(*state.PgStore); ok {
		callers, lookupErr := pg.LiveServiceCallersByHostIP(ctx, "", "10.100.0.6")
		if lookupErr != nil || len(callers) != 1 || callers[0].ID != id.InstanceID || callers[0].AppID != id.AppID || callers[0].DeploymentID != id.DeploymentID {
			t.Fatal("original resume hook could not reconnect to contained dependencies")
		}
		if live, lookupErr := pg.LiveInstancesByHostIP(ctx, "", "10.100.0.6"); lookupErr != nil || len(live) != 0 {
			t.Fatal("resuming caller identity became a serving target")
		}
	}
	step("resuming", "resumed", 0, resumeMS)
	if err = control.CompleteScenarioSuspendResume(ctx, r.ID); err == nil {
		t.Fatal("unpublished VM completed lifecycle")
	}
	if err = f.store.UpdateInstanceState(ctx, id.InstanceID, "running"); err != nil {
		t.Fatal(err)
	}
	if err = control.CompleteScenarioSuspendResume(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = control.ClaimScenarioSuspendResume(ctx, r.ID); err == nil {
		t.Fatal("completed pause repeated")
	}
	e, err = control.ScenarioSuspendResumeEvidence(ctx, f.account.ID, f.run)
	if err != nil || e.Validate([]string{"primary", "worker", "reader", "unbound"}) != nil {
		t.Fatalf("lifecycle proof: %+v %v", e, err)
	}
	return e.Receipts[0]
}

func TestSuspendResumeOwnerProofAndPostResumeBehavior(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			run := func(duplicate bool, ms int64) *actioncompare.Evidence {
				f := newFeatureFixture(t, backend, false, fixtureFeatures{resume: true})
				f.suspend(t, ms)
				j := f.claim(t, f.enqueue(t).ID)
				f.notify(t)
				if duplicate {
					f.notify(t)
				}
				f.complete(t, j)
				s := f.finish(t, "")
				if s.Status != "complete" || s.SuspendResume == nil || s.SuspendResume.Validate(s.Workloads) != nil {
					t.Fatalf("sealed lifecycle incomplete: %+v", s)
				}
				return &actioncompare.Evidence{Version: 1, Status: "complete", Provenance: actioncompare.SimulationProvenance, ContractSHA256: strings.Repeat("a", 64), FixtureSHA256: strings.Repeat("b", 64), Snapshot: &s}
			}
			baseline := run(false, 100)
			for _, tc := range []struct {
				name      string
				duplicate bool
				ms        int64
				want      string
			}{{"same_behavior", false, 200, "passed"}, {"duplicate_notification", true, 200, "failed"}, {"resume_budget", false, 1001, "failed"}} {
				t.Run(tc.name, func(t *testing.T) {
					if r := actioncompare.Compare(baseline, run(tc.duplicate, tc.ms)); r.Status != tc.want {
						t.Fatalf("want %s: %+v", tc.want, r)
					}
				})
			}
		})
	}
}

func TestSuspendResumeRefusesUnsettledAndMissingProof(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		for _, mode := range []string{"unsettled", "missing", "lost_owner", "prior_job"} {
			t.Run(backend+"/"+mode, func(t *testing.T) {
				f := newFeatureFixture(t, backend, false, fixtureFeatures{resume: true})
				id := f.identities["worker"]
				if err := f.store.UpdateInstanceState(t.Context(), id.InstanceID, "running"); err != nil {
					t.Fatal(err)
				}
				if mode == "unsettled" {
					_ = f.workerRequest(t)
				}
				if mode == "prior_job" {
					_ = f.enqueue(t)
				}
				control := f.store.(rehearsalresume.Store)
				if mode != "missing" {
					e, err := control.BeginScenarioSuspendResume(t.Context(), f.account.ID, f.run)
					if mode == "unsettled" || mode == "prior_job" {
						if err == nil {
							t.Fatal("busy checkpoint accepted")
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if _, err = control.ClaimScenarioSuspendResume(t.Context(), e.Receipts[0].ID); err != nil {
						t.Fatal(err)
					}
					if err = control.FailScenarioSuspendResume(t.Context(), e.Receipts[0].ID); err != nil {
						t.Fatal(err)
					}
				}
				if s := f.finish(t, ""); s.Status == "complete" || s.SuspendResume == nil || s.SuspendResume.Status == "complete" {
					t.Fatal("missing physical proof passed")
				}
			})
		}
	}
}

func TestSuspendResumeSQLProofCannotBeRewrittenOrDeleted(t *testing.T) {
	f := newFeatureFixture(t, "postgres", false, fixtureFeatures{resume: true})
	id := f.identities["worker"]
	if err := f.store.UpdateInstanceState(t.Context(), id.InstanceID, "running"); err != nil {
		t.Fatal(err)
	}
	e, err := f.store.(rehearsalresume.Store).BeginScenarioSuspendResume(t.Context(), f.account.ID, f.run)
	if err != nil {
		t.Fatal(err)
	}
	r := e.Receipts[0]
	for _, q := range []struct{ sql, arg string }{{"DELETE FROM scenario_suspend_resumes WHERE id=$1", r.ID}, {"UPDATE scenario_suspend_resumes SET state='complete' WHERE id=$1", r.ID}, {"UPDATE scenario_suspend_resumes SET recorder_session_id=gen_random_uuid() WHERE id=$1", r.ID}, {"UPDATE scenario_test_action_policies SET suspend_resume_spec=jsonb_set(suspend_resume_spec,'{pause_ms}','2') WHERE app_id=$1", r.AppID}} {
		if _, err = f.pool.Exec(t.Context(), q.sql, q.arg); err == nil {
			t.Fatal("raw SQL forged or discarded lifecycle proof")
		}
	}
}

func TestSuspendResumeRejectsRuntimeChangesBeforeGuestResume(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newFeatureFixture(t, backend, false, fixtureFeatures{resume: true})
			ctx := t.Context()
			id := f.identities["worker"]
			control := f.store.(rehearsalresume.Store)
			e, err := control.BeginScenarioSuspendResume(ctx, f.account.ID, f.run)
			if err != nil {
				t.Fatal(err)
			}
			r := e.Receipts[0]
			if _, err = control.ClaimScenarioSuspendResume(ctx, r.ID); err != nil {
				t.Fatal(err)
			}
			if err = f.store.UpdateInstanceState(ctx, id.InstanceID, "frozen"); err != nil {
				t.Fatal(err)
			}
			owner := f.store.(rehearsalrecorder.SuspendStore)
			for _, step := range [][2]string{{"claimed", "fenced"}, {"fenced", "paused"}} {
				if _, err = owner.AdvanceScenarioSuspendResume(ctx, id, f.recorders["worker"], r.ID, step[0], step[1], 0, 0); err != nil {
					t.Fatal(err)
				}
			}
			ram := 256
			if _, err = f.store.UpdateApp(ctx, id.AppID, state.UpdateAppParams{RAMMB: &ram}); err != nil {
				t.Fatal(err)
			}
			if _, err = owner.AdvanceScenarioSuspendResume(ctx, id, f.recorders["worker"], r.ID, "paused", "resuming", 1, 0); err == nil {
				t.Fatal("changed runtime regained effect authority before resume")
			}
			if _, err = f.store.(rehearsalrecorder.Store).BeginScenarioRecorderRequest(ctx, id, f.recorders["worker"]); err == nil {
				t.Fatal("changed runtime admitted an effect while paused")
			}
		})
	}
}
