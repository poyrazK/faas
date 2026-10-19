package state

// adr: 1525. Simulated native evidence checks durable admission, not KVM.

import (
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

type parkAdmissionTestStore = contractTestStore

func runParkAdmission(t *testing.T, s parkAdmissionTestStore) {
	t.Helper()
	ins, _ := standardSnapshotFixture(t, s, "warm")
	app, err := s.AppByID(t.Context(), ins.AppID)
	if err != nil {
		t.Fatal(err)
	}
	ins, err = s.MigrationInstanceByID(t.Context(), ins.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.InsertOperatorIntent(t.Context(), OperatorIntentKindForcePark, ins.ID, &app.AccountID, app.AccountID, "measured park", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	i, err := s.ClaimPendingOperatorIntent(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	key := SnapshotCaptureMemKey(ins.DeploymentID, SnapshotTierInit, uuid.NewString())
	target := OperatorParkTarget{OperatorRestartTarget: OperatorRestartTarget{InstanceID: ins.ID, AppID: ins.AppID, DeploymentID: ins.DeploymentID, NodeID: ins.NodeID, WakeID: ins.WakeID, Generation: uuid.NewString()}, InitialState: ins.State, Mode: ins.Mode, StartedAt: ins.StartedAt, LastRequestAt: ins.LastRequestAt, RequestCount: ins.RequestCount, Action: OperatorParkCapture, Measured: true, MemoryKey: key, VMStateKey: SnapshotVMStateKey(Snapshot{StorageKey: key}), FCVersion: "1.12.1"}
	e, err := s.PrepareOperatorPark(t.Context(), i.ID, i.ClaimToken, target)
	if err != nil || e.Target.Grant == nil {
		t.Fatalf("measured prepare=%+v %v", e, err)
	}
	grant := e.Target.Grant.Clone()
	again, err := s.PrepareOperatorPark(t.Context(), i.ID, i.ClaimToken, target)
	if err != nil || !again.Target.Grant.Equal(grant) {
		t.Fatalf("grant replay=%+v %v", again, err)
	}
	e, execute, err := s.StartOperatorPark(t.Context(), i.ID, i.ClaimToken, ins.NodeID)
	if err != nil || !execute {
		t.Fatalf("start=%v %v", execute, err)
	}
	ack := standardSnapshotAck(grant)
	r := OperatorParkReceipt{Outcome: string(StateParked), MemBytes: ack.Capture.Memory.Bytes, VMStateBytes: ack.Capture.VMState.Bytes, StoredBytes: 1024, Acknowledgment: &ack}
	if err := s.AcknowledgeOperatorPark(t.Context(), i.ID, e.EffectToken, r); err != nil {
		t.Fatal(err)
	}
	if err := s.AcknowledgeOperatorPark(t.Context(), i.ID, e.EffectToken, r); err != nil {
		t.Fatal("native receipt replay", err)
	}
	completion := OperatorParkCompletion{IntentID: i.ID, ClaimToken: i.ClaimToken, Status: OperatorIntentSucceeded}
	if _, err := s.CompleteOperatorPark(t.Context(), completion); err == nil {
		t.Fatal("capture bypassed native acknowledgment publication")
	}
	if err := s.PublishApplicationStandardSnapshotCapture(t.Context(), ack); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteOperatorPark(t.Context(), completion); err != nil {
		t.Fatal(err)
	}
	current, err := s.InstanceByID(t.Context(), ins.ID)
	if err != nil || current.State != string(StateParked) {
		t.Fatalf("terminal=%+v %v", current, err)
	}
}

func TestOperatorParkAdmissionMemory(t *testing.T) { runParkAdmission(t, NewMemStore()) }
func TestOperatorParkAdmissionPostgres(t *testing.T) {
	runParkAdmission(t, NewPgStore(pgtest.OpenMigrated(t)))
}
