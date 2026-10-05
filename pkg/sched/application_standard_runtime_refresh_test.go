// adr: 593
package sched

// These tests prove scheduler orchestration with a simulated legacy native
// consumer; they do not prove protocol-2 artifact consumption or metal safety.

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func installRefreshTestRevision(t *testing.T, s *state.MemStore, app state.App) state.ApplicationStandardOperation {
	t.Helper()
	assignments, err := s.ListApplicationStandardAssignments(t.Context(), app.OrgID)
	if err != nil || len(assignments) != 1 {
		t.Fatal(assignments, err)
	}
	a := assignments[0]
	v, err := s.GetApplicationStandardVersion(t.Context(), app.OrgID, "runtime-company", 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.PublishApplicationStandardVersion(t.Context(), state.ApplicationStandardPublish{OrgID: app.OrgID, ActorID: app.AccountID, Slug: v.Slug, CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{ExpectedVersion: 1, Definition: json.RawMessage(`{"egress_cidrs":{"mode":"mandatory","value":["1.1.1.0/24"]}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.PreviewApplicationStandardAssignment(t.Context(), app.OrgID, app.AccountID, state.ApplicationStandardReviewRequest{AssignmentID: a.ID, ExpectedRevision: 1, Scope: a.Scope, ScopeID: a.ScopeID, StandardID: a.StandardID, AdmissionVersion: 2, Active: true, BatchSize: 1})
	if err != nil || len(p.Blockers) != 0 {
		t.Fatal("runtime refresh review", p.Blockers, err)
	}
	if _, err := s.ApproveApplicationStandardReview(t.Context(), app.OrgID, app.AccountID, p.ID, p.ApprovalHash); err != nil {
		t.Fatal(err)
	}
	c, err := s.ClaimApplicationStandardOperation(t.Context(), "scheduler-refresh-installer")
	if err != nil {
		t.Fatal(err)
	}
	o, err := s.MaterializeNextApplicationStandardTarget(t.Context(), c)
	if err != nil || len(o.Targets) != 1 || o.Targets[0].State != "persisted" {
		t.Fatal("runtime refresh install", o, err)
	}
	return o
}

func TestApplicationStandardRuntimeRefreshKeepsIdleCold(t *testing.T) {
	s, app, _ := newStandardCaptureService(t)
	vmm := &fakeVMM{}
	e := newEngine(t, s, vmm, &fakeNotifier{}, "1.10.0")
	r, err := s.GetApplicationStandardRuntimeRefresh(t.Context(), app.OrgID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if out, err := e.RefreshApplicationStandard(t.Context(), r); err != nil || out.Instance != nil {
			t.Fatal("idle handoff woke a service", out, err)
		}
	}
	if vmm.coldBoots != 0 || vmm.destroys != 0 || vmm.snapshots != 0 {
		t.Fatal("idle revision performed VM work")
	}
	if _, stamped, err := s.AppRuntimeConfigChangedAt(t.Context(), app.ID); err != nil || stamped {
		t.Fatal("standard refresh stamped environment intent", err)
	}
}

func TestApplicationStandardRuntimeRefreshRollsResidentAndReplays(t *testing.T) {
	s, app, dep := newStandardCaptureService(t)
	vmm := newStandardNativeTestVMM(t, s, &fakeVMM{}, standardNativeTestNodeID(t, s))
	e := newEngine(t, s, vmm, &fakeNotifier{}, "1.10.0")
	if _, err := e.Wake(t.Context(), app.ID, dep.ID, "", TriggerGateway); err != nil {
		t.Fatal(err)
	}
	old, _ := s.ListInstancesForApp(t.Context(), app.ID)
	prior, _ := s.GetApplicationStandardRuntimeRefresh(t.Context(), app.OrgID, app.ID)
	_ = installRefreshTestRevision(t, s, app)
	r, err := s.GetApplicationStandardRuntimeRefresh(t.Context(), app.OrgID, app.ID)
	if err != nil || r.WakeID == prior.WakeID {
		t.Fatal("new revision reused old runtime work", r, err)
	}
	if out, err := e.RefreshApplicationStandard(t.Context(), prior); err != nil || out.Instance != nil || vmm.coldBoots != 1 {
		t.Fatal("old queued revision performed work", out, err)
	}
	for i := 0; i < 2; i++ {
		if _, err := e.RefreshApplicationStandard(t.Context(), r); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.ListInstancesForApp(t.Context(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	running := 0
	for _, ins := range rows {
		if ins.State == string(state.StateRunning) {
			running++
			capture, err := s.GetInstanceApplicationStandardAdmission(t.Context(), ins.ID)
			if err != nil || capture.DesiredRevision != r.Standard.DesiredRevision || capture.EffectiveHash != r.Standard.EffectiveHash {
				t.Fatal("replacement has old captured standard", capture.DesiredRevision, capture.EffectiveHash, err)
			}
		}
		if ins.ID == old[0].ID && ins.State != string(state.StateStopped) {
			t.Fatal("old serving process was retained", ins)
		}
	}
	if running != 1 || vmm.coldBoots != 2 || vmm.destroys != 1 || vmm.snapshots != 0 {
		t.Fatal("rolling handoff replay duplicated VM work", running, vmm.coldBoots, vmm.destroys, vmm.snapshots)
	}
	enrollment, _ := s.GetApplicationStandardEnrollment(t.Context(), app.OrgID, app.ID)
	if enrollment.ObservedRevision != 0 {
		t.Fatal("scheduler ACK fabricated whole-app observation")
	}
}

func TestApplicationStandardRuntimeRefreshPauseDuringReplacement(t *testing.T) {
	s, app, dep := newStandardCaptureService(t)
	vmm := newStandardNativeTestVMM(t, s, &fakeVMM{}, standardNativeTestNodeID(t, s))
	e := newEngine(t, s, vmm, &fakeNotifier{}, "1.10.0")
	if _, err := e.Wake(t.Context(), app.ID, dep.ID, "", TriggerGateway); err != nil {
		t.Fatal(err)
	}
	o := installRefreshTestRevision(t, s, app)
	r, err := s.GetApplicationStandardRuntimeRefresh(t.Context(), app.OrgID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	var paused state.ApplicationStandardOperation
	vmm.beforeNative = func() {
		var err error
		paused, err = s.ControlApplicationStandardOperation(t.Context(), app.OrgID, app.AccountID, o.ID, o.UpdatedAt, state.ApplicationStandardOperationPause)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.RefreshApplicationStandard(t.Context(), r); !errors.Is(err, state.ErrApplicationStandardRefreshDeferred) {
		t.Fatal("in-flight pause acknowledged runtime work", err)
	}
	if vmm.coldBoots != 2 || vmm.destroys != 0 {
		t.Fatal("paused rollout retired serving predecessor", vmm.coldBoots, vmm.destroys)
	}
	vmm.beforeNative = nil
	if _, err := s.ControlApplicationStandardOperation(t.Context(), app.OrgID, app.AccountID, o.ID, paused.UpdatedAt, state.ApplicationStandardOperationResume); err != nil {
		t.Fatal(err)
	}
	if _, err := e.RefreshApplicationStandard(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if vmm.coldBoots != 2 || vmm.destroys != 1 || vmm.snapshots != 0 {
		t.Fatal("resume did not reuse the ready replacement", vmm.coldBoots, vmm.destroys, vmm.snapshots)
	}
}

func TestApplicationStandardRuntimeRefreshPauseDefersLoopAndResume(t *testing.T) {
	s, app, _ := newStandardCaptureService(t)
	o := installRefreshTestRevision(t, s, app)
	paused, err := s.ControlApplicationStandardOperation(t.Context(), app.OrgID, app.AccountID, o.ID, o.UpdatedAt, state.ApplicationStandardOperationPause)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := s.GetApplicationStandardRuntimeRefresh(t.Context(), app.OrgID, app.ID)
	payload, _ := json.Marshal(r)
	vmm := &fakeVMM{}
	e := newEngine(t, s, vmm, &fakeNotifier{}, "1.10.0")
	l := NewLoop(nil, e, e.log)
	if err := l.handleRuntimeConfigRestart(t.Context(), db.Notification{Payload: string(payload)}); !errors.Is(err, db.ErrNotificationDeferred) {
		t.Fatal("paused loop consumed its durable work", err)
	}
	if _, err := s.ControlApplicationStandardOperation(t.Context(), app.OrgID, app.AccountID, o.ID, paused.UpdatedAt, state.ApplicationStandardOperationResume); err != nil {
		t.Fatal(err)
	}
	if err := l.handleRuntimeConfigRestart(t.Context(), db.Notification{Payload: string(payload)}); err != nil {
		t.Fatal(err)
	}
	if vmm.coldBoots != 0 || vmm.destroys != 0 {
		t.Fatal("pause or resume woke idle service")
	}
}
