package state

// adr: 1576
// Durable authority tests simulate consumption, not native effects.

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type functionMigrationAdmissionTestStore interface {
	standardSnapshotTestStore
	FunctionMigrationAdmissionStore
	FunctionMigrationSourceStore
	MigrationLeaseStore
}

func functionMigrationAdmissionFixture(t *testing.T, s functionMigrationAdmissionTestStore) (Instance, FunctionMigrationCaptureRequest) {
	t.Helper()
	ins, parent := issueConsumedNativeFixture(t, s)
	ins, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, parent)
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.NewString()
	memory := SnapshotCaptureMemKey(ins.DeploymentID, SnapshotTierWarm, token)
	source := FunctionMigrationSource{InstanceID: ins.ID, SourceNodeID: ins.NodeID, SourceWakeID: ins.WakeID, LeaseToken: FunctionMigrationTokenPrefix + uuid.NewString()}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := s.ReserveMigrationLease(t.Context(), MigrationLease{InstanceID: ins.ID, SourceNodeID: ins.NodeID, LeaseToken: source.LeaseToken, CreatedAt: now, LeaseExpiresAt: now.Add(time.Minute), Pending: true}); err != nil {
		t.Fatal(err)
	}
	return ins, FunctionMigrationCaptureRequest{Source: source, Parent: parent, MemoryKey: memory, VMStateKey: SnapshotVMStateKey(Snapshot{StorageKey: memory}), FCVersion: "1.12.1"}
}

func functionMigrationAdmissionFence(t *testing.T, s functionMigrationAdmissionTestStore, req FunctionMigrationCaptureRequest) {
	t.Helper()
	if err := s.CompleteMigrationLease(t.Context(), req.Source.LeaseToken, req.MemoryKey, req.VMStateKey); err != nil {
		t.Fatal(err)
	}
	if resolution, err := s.FenceFunctionMigrationSource(t.Context(), req.Source.InstanceID, req.Source.LeaseToken); err != nil || resolution != MigrationCommitAborted {
		t.Fatal("cannot fence original source", resolution, err)
	}
}

func functionMigrationAdmissionLifecycle(t *testing.T, s functionMigrationAdmissionTestStore) {
	t.Helper()
	ins, req := functionMigrationAdmissionFixture(t, s)
	ctx := t.Context()
	g, err := s.IssueFunctionMigrationCapture(ctx, req)
	if err != nil || g.Validate(time.Now()) != nil || !g.Parent.Equal(req.Parent) {
		t.Fatal("capture permission was not bound to serving bytes", err)
	}
	retry, err := s.IssueFunctionMigrationCapture(ctx, req)
	if err != nil || !retry.Equal(g) {
		t.Fatal("retry renewed capture permission", err)
	}
	if err := s.CheckFunctionMigrationCapture(ctx, g); err != nil {
		t.Fatal(err)
	}
	lease, err := s.GetMigrationLease(ctx, ins.ID, req.Source.LeaseToken)
	source, sourceErr := s.GetFunctionMigrationSource(ctx, ins.ID, req.Source.LeaseToken)
	if err != nil || sourceErr != nil || !lease.Pending || lease.MemStorageKey != req.MemoryKey || lease.VMStateStorageKey != req.VMStateKey || source.CaptureToken != g.CaptureToken || source.Phase != FunctionMigrationPreparing || g.ExpiresAtUnixNano > lease.LeaseExpiresAt.UnixNano() {
		t.Fatal("atomic permission omitted pending source or handles", err, sourceErr)
	}
	if _, err := s.IssueFunctionMigrationRecovery(ctx, ins.ID, source.LeaseToken); err == nil {
		t.Fatal("recovery permission preceded schedd fence")
	}
	if err := s.MarkInstanceMigrating(ctx, ins.ID, ins.NodeID, source.LeaseToken); err == nil {
		t.Fatal("capture permission invented capture ACK")
	}
	changed := retry.Clone()
	changed.Parent.ArtifactConsumption.Drives[0].DriveID = "caller-mutated"
	if err := s.CheckFunctionMigrationCapture(ctx, changed); err == nil {
		t.Fatal("changed parent accepted")
	}
	if err := s.CheckFunctionMigrationCapture(ctx, g); err != nil {
		t.Fatal("caller mutated persisted grant", err)
	}
	if err := s.CompleteMigrationLease(ctx, source.LeaseToken, req.MemoryKey, req.VMStateKey); err != nil {
		t.Fatal(err)
	}
	if err := s.PrepareFunctionMigrationSource(ctx, ins.ID, source.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkInstanceMigrating(ctx, ins.ID, ins.NodeID, source.LeaseToken); err != nil {
		t.Fatal("fresh prepared source could not enter migration", err)
	}
	if resolution, err := s.FenceFunctionMigrationSource(ctx, ins.ID, source.LeaseToken); err != nil || resolution != MigrationCommitAborted {
		t.Fatal(resolution, err)
	}
	if err := s.CheckFunctionMigrationCapture(ctx, g); err == nil {
		t.Fatal("capture permission survived rollback fence")
	}
	if err := s.CompleteFunctionMigrationSource(ctx, ins.ID, source.LeaseToken, false); err == nil {
		t.Fatal("RUNNING publication omitted recovery permission")
	}
	r, err := s.IssueFunctionMigrationRecovery(ctx, ins.ID, source.LeaseToken)
	if err != nil || r.Token == g.Token || !r.SameSource(g) || r.Purpose != runtimeadmission.FunctionMigrationRecover {
		t.Fatal("recovery permission lost original source", err)
	}
	r2, err := s.IssueFunctionMigrationRecovery(ctx, ins.ID, source.LeaseToken)
	if err != nil || !r.Equal(r2) {
		t.Fatal("duplicate issuance revoked in-flight recovery", err)
	}
	if err := s.CheckFunctionMigrationRecovery(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteFunctionMigrationSource(ctx, ins.ID, source.LeaseToken, false); err != nil {
		t.Fatal("fresh recovery could not publish source", err)
	}
	if err := s.CompleteFunctionMigrationSource(ctx, ins.ID, source.LeaseToken, false); err != nil {
		t.Fatal("lost completion ACK was not idempotent", err)
	}
	if _, err := s.IssueFunctionMigrationRecovery(ctx, ins.ID, source.LeaseToken); err == nil {
		t.Fatal("completed source issued new recovery effects")
	}
	if err := s.DeleteMigrationLease(ctx, source.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFunctionMigrationSource(ctx, ins.ID, source.LeaseToken); !errors.Is(err, ErrNotFound) {
		t.Fatal("source authority survived parent cleanup", err)
	}
}

func functionMigrationAdmissionExpiry(t *testing.T, s functionMigrationAdmissionTestStore) {
	t.Helper()
	ins, req := functionMigrationAdmissionFixture(t, s)
	g, err := s.IssueFunctionMigrationCapture(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RenewMigrationLease(t.Context(), req.Source.LeaseToken, ins.NodeID, time.Now().UTC().Add(-time.Microsecond)); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckFunctionMigrationCapture(t.Context(), g); err == nil {
		t.Fatal("changed lease expiry retained old capture authority")
	}
	if _, err := s.IssueFunctionMigrationCapture(t.Context(), req); err == nil {
		t.Fatal("expired capture renewed itself")
	}
	functionMigrationAdmissionFence(t, s, req)
	r, err := s.IssueFunctionMigrationRecovery(t.Context(), ins.ID, req.Source.LeaseToken)
	if err != nil || r.Validate(time.Now()) != nil || r.LeaseExpiresAtUnixNano >= r.IssuedAtUnixNano || !r.SameSource(g) || r.Token == g.Token {
		t.Fatal("fresh fenced recovery unavailable after lease expiry", err)
	}
	if err := s.CheckFunctionMigrationRecovery(t.Context(), r); err != nil {
		t.Fatal(err)
	}
}

func functionMigrationAdmissionDrift(t *testing.T, s functionMigrationAdmissionTestStore, restart, recovering bool) {
	t.Helper()
	ins, req := functionMigrationAdmissionFixture(t, s)
	g, err := s.IssueFunctionMigrationCapture(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if recovering {
		functionMigrationAdmissionFence(t, s, req)
		g, err = s.IssueFunctionMigrationRecovery(t.Context(), ins.ID, req.Source.LeaseToken)
		if err != nil {
			t.Fatal(err)
		}
	}
	if restart {
		b := req.Parent.Binding
		b.Incarnation = uuid.NewString()
		registerConsumedNativeIdentity(t, s, b, runtimeadmission.ArtifactProtocolVersion)
	} else {
		policy := api.AppSecurityPolicyEnforce
		if _, err := s.UpdateApp(t.Context(), ins.AppID, UpdateAppParams{SetSecurityPolicy: true, SecurityPolicy: &policy}); err != nil {
			t.Fatal(err)
		}
	}
	if recovering {
		if err := s.CheckFunctionMigrationRecovery(t.Context(), g); err == nil {
			t.Fatal("changed runtime input retained recovery permission")
		}
		if _, err := s.IssueFunctionMigrationRecovery(t.Context(), ins.ID, req.Source.LeaseToken); err == nil {
			t.Fatal("stale physical parent gained new recovery permission")
		}
		if err := s.CompleteFunctionMigrationSource(t.Context(), ins.ID, req.Source.LeaseToken, false); err == nil {
			t.Fatal("stale physical parent published RUNNING")
		}
		if err := s.CompleteFunctionMigrationSource(t.Context(), ins.ID, req.Source.LeaseToken, true); err != nil {
			t.Fatal("retirement required expired runtime authority", err)
		}
	} else {
		if err := s.CheckFunctionMigrationCapture(t.Context(), g); err == nil {
			t.Fatal("changed runtime input retained capture permission")
		}
		if _, err := s.IssueFunctionMigrationCapture(t.Context(), req); err == nil {
			t.Fatal("stale parent renewed capture permission")
		}
	}
}

func TestFunctionMigrationAdmissionMem(t *testing.T) {
	t.Run("lifecycle", func(t *testing.T) { functionMigrationAdmissionLifecycle(t, NewMemStore()) })
	t.Run("expired lease recovery", func(t *testing.T) { functionMigrationAdmissionExpiry(t, NewMemStore()) })
	for _, restart := range []bool{false, true} {
		for _, recovery := range []bool{false, true} {
			t.Run(map[bool]string{true: "restart", false: "policy"}[restart]+map[bool]string{true: "/recovery", false: "/capture"}[recovery], func(t *testing.T) {
				functionMigrationAdmissionDrift(t, NewMemStore(), restart, recovery)
			})
		}
	}
}

func TestFunctionMigrationAdmissionStrictJSON(t *testing.T) {
	s := NewMemStore()
	_, req := functionMigrationAdmissionFixture(t, s)
	g, err := s.IssueFunctionMigrationCapture(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range [][]byte{nil, []byte(`{}`), append(append([]byte{}, raw...), []byte(` {}`)...), append(append([]byte{}, raw[:len(raw)-1]...), []byte(`,"unknown":true}`)...)} {
		if _, err := decodeFunctionMigrationGrant(raw, runtimeadmission.FunctionMigrationCapture); err == nil {
			t.Fatal("malformed or extended persisted grant accepted")
		}
	}
}

func functionMigrationAdmissionRenewal(t *testing.T, s functionMigrationAdmissionTestStore, expire func(runtimeadmission.FunctionMigrationGrant)) {
	t.Helper()
	ins, req := functionMigrationAdmissionFixture(t, s)
	capture, err := s.IssueFunctionMigrationCapture(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	functionMigrationAdmissionFence(t, s, req)
	r, err := s.IssueFunctionMigrationRecovery(t.Context(), ins.ID, req.Source.LeaseToken)
	if err != nil {
		t.Fatal(err)
	}
	expired := r.Clone()
	expired.IssuedAtUnixNano = time.Now().Add(-time.Second).UnixNano()
	expired.ExpiresAtUnixNano = time.Now().Add(-time.Microsecond).UnixNano()
	expire(expired)
	if err := s.CheckFunctionMigrationRecovery(t.Context(), r); err == nil {
		t.Fatal("superseded permission survived durable expiry")
	}
	if err := s.CompleteFunctionMigrationSource(t.Context(), ins.ID, req.Source.LeaseToken, false); err == nil {
		t.Fatal("expired recovery published RUNNING")
	}
	renewed, err := s.IssueFunctionMigrationRecovery(t.Context(), ins.ID, req.Source.LeaseToken)
	if err != nil || renewed.Token == r.Token || !renewed.SameSource(capture) {
		t.Fatal("expired recovery did not obtain a new scoped nonce", err)
	}
	if err := s.CheckFunctionMigrationRecovery(t.Context(), r); err == nil {
		t.Fatal("old nonce survived recovery renewal")
	}
	if err := s.CheckFunctionMigrationRecovery(t.Context(), renewed); err != nil {
		t.Fatal(err)
	}
}

func TestFunctionMigrationAdmissionMemRenewal(t *testing.T) {
	s := NewMemStore()
	functionMigrationAdmissionRenewal(t, s, func(g runtimeadmission.FunctionMigrationGrant) {
		s.mu.Lock()
		defer s.mu.Unlock()
		p := s.functionMigrationPermissions[g.LeaseToken]
		p.recovery = &g
		s.functionMigrationPermissions[g.LeaseToken] = p
	})
}

func functionMigrationAdmissionAtomicRefusal(t *testing.T, s functionMigrationAdmissionTestStore) {
	t.Helper()
	ins, req := functionMigrationAdmissionFixture(t, s)
	for _, kind := range []string{"keys", "parent", "FC version"} {
		bad := req
		switch kind {
		case "keys":
			bad.VMStateKey += "-different"
		case "parent":
			bad.Parent = req.Parent.Clone()
			bad.Parent.LeaseUID++
		case "FC version":
			bad.FCVersion = "bad/path"
		}
		if _, err := s.IssueFunctionMigrationCapture(t.Context(), bad); err == nil {
			t.Fatal("invalid capture committed", kind)
		}
		lease, err := s.GetMigrationLease(t.Context(), ins.ID, req.Source.LeaseToken)
		_, sourceErr := s.GetFunctionMigrationSource(t.Context(), ins.ID, req.Source.LeaseToken)
		if err != nil || !lease.Pending || lease.MemStorageKey != "" || lease.VMStateStorageKey != "" || !errors.Is(sourceErr, ErrNotFound) {
			t.Fatal("invalid permission partially registered handles or source", kind, err, sourceErr)
		}
	}
}

func TestFunctionMigrationAdmissionMemAtomicRefusal(t *testing.T) {
	functionMigrationAdmissionAtomicRefusal(t, NewMemStore())
}
