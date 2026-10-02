// adr: 429 — captured intent fences scheduler runtime publication.

package sched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func newStandardCaptureService(t *testing.T) (*state.MemStore, state.App, state.Deployment) {
	t.Helper()
	s := state.NewMemStore()
	o := standardAdmissionOwner(t, s)
	approveEgressAdmission(t, s, o)
	c, err := s.ClaimApplicationStandardOperation(t.Context(), "capture-empty-admission")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MaterializeNextApplicationStandardTarget(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(t.Context(), state.App{AccountID: o.Account.ID, OrgID: o.PersonalOrg.ID, Slug: "runtime-capture-race", RAMMB: 128, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimApplicationStandardEnrollment(t.Context(), "capture-runtime-repair")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MaterializeApplicationStandardEnrollment(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
	app, err = s.AppByID(t.Context(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:capture-race", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	return s, app, dep
}

func TestApplicationStandardChangeDuringBootDestroysStaleRuntime(t *testing.T) {
	for _, prime := range []bool{false, true} {
		t.Run(map[bool]string{false: "wake", true: "prime"}[prime], func(t *testing.T) {
			s, app, dep := newStandardCaptureService(t)
			vmm := &fakeVMM{}
			vmm.coldBootHook = func() {
				if _, err := s.ScheduleAppDeletion(t.Context(), app.ID, time.Now().Add(time.Hour)); err != nil {
					t.Fatal(err)
				}
				if _, err := s.RestoreApp(t.Context(), app.ID, api.MustLimitsFor(api.PlanPro)); err != nil {
					t.Fatal(err)
				}
				claim, err := s.ClaimApplicationStandardEnrollment(t.Context(), "capture-mid-boot-repair")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.MaterializeApplicationStandardEnrollment(t.Context(), claim); err != nil {
					t.Fatal(err)
				}
			}
			e := newEngine(t, s, newStandardNativeTestVMM(t, s, vmm, standardNativeTestNodeID(t, s)), &fakeNotifier{}, "1.10.0")
			var err error
			if prime {
				err = e.Prime(t.Context(), app.ID, dep.ID)
			} else {
				_, err = e.Wake(t.Context(), app.ID, dep.ID, "", TriggerGateway)
			}
			if !errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
				t.Fatalf("stale boot reported success: %v", err)
			}
			if vmm.coldBoots != 1 || vmm.destroys != 1 || vmm.snapshots != 0 || e.Ledger().ResidentRAM() != 0 {
				t.Fatalf("stale VM leaked or primed: boots=%d destroys=%d snapshots=%d RAM=%d", vmm.coldBoots, vmm.destroys, vmm.snapshots, e.Ledger().ResidentRAM())
			}
			rows, err := s.ListInstancesForApp(t.Context(), app.ID)
			if err != nil || len(rows) != 1 || rows[0].State != string(state.StateFailed) || rows[0].HostIP != "" {
				t.Fatalf("stale runtime published: %+v %v", rows, err)
			}
			enrollment, err := s.GetApplicationStandardEnrollment(t.Context(), app.OrgID, app.ID)
			if err != nil || enrollment.ObservedRevision != 0 {
				t.Fatalf("stale boot acknowledged policy: %+v %v", enrollment, err)
			}
		})
	}
}

type staleRuntimeCaptureStore struct {
	*state.MemStore
	beforeInsert func()
}

func (s *staleRuntimeCaptureStore) CreateInstanceWithMode(ctx context.Context, appID, depID, status string, ram int, nodeID, wakeID, mode string) (state.Instance, error) {
	s.beforeInsert()
	return s.MemStore.CreateInstanceWithMode(ctx, appID, depID, status, ram, nodeID, wakeID, mode)
}

func TestApplicationStandardCaptureRejectsOldSchedulerRead(t *testing.T) {
	s, app, dep := newStandardCaptureService(t)
	wrapped := &staleRuntimeCaptureStore{MemStore: s, beforeInsert: func() {
		// This ungoverned setting changes after resolveApp but before the durable
		// capture. A later new revision must not bless the older scheduler read.
		signed := true
		if _, err := s.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{SetRequireSigned: true, RequireSigned: &signed}); err != nil {
			t.Fatal(err)
		}
	}}
	vmm := &fakeVMM{}
	e := newEngine(t, wrapped, vmm, &fakeNotifier{}, "1.10.0")
	if _, err := e.Wake(t.Context(), app.ID, dep.ID, "", TriggerGateway); !errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
		t.Fatalf("old read booted under newer capture: %v", err)
	}
	rows, _ := s.ListInstancesForApp(t.Context(), app.ID)
	if len(rows) != 0 || vmm.coldBoots != 0 || vmm.destroys != 0 || e.Ledger().ResidentRAM() != 0 {
		t.Fatalf("rejected input performed VM admission: rows=%d boots=%d destroys=%d RAM=%d", len(rows), vmm.coldBoots, vmm.destroys, e.Ledger().ResidentRAM())
	}
}
