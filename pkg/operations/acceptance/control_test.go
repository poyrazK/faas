// adr: 521
package acceptance_test

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

type operationControlFixtureStore interface {
	operationLifecycleStore
	state.OperationExecutionControlStore
}

func operationControlClaim(t *testing.T, s operationControlFixtureStore) (state.Operation, state.Invocation, state.OperationExecutionAuthority) {
	t.Helper()
	ctx, acct, app, def, alice, _ := operationFixture(t, s)
	nodeID := uuid.NewString()
	if node, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName); err == nil {
		nodeID = node.ID
	}
	instance, err := s.CreateInstance(ctx, app.ID, def.DeploymentID, "stopped", 128, nodeID, "")
	if err != nil {
		t.Fatal(err)
	}
	op, _, err := s.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID, PlatformTenantID: alice.ID, IdempotencyKey: "control-read", Input: []byte(`{"count":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	inv, err := s.ClaimInvocationWithCap(ctx, op.CurrentInvocationID, instance.ID, 60, 100)
	if err != nil {
		t.Fatal(err)
	}
	var headers map[string]string
	if err := json.Unmarshal(inv.Headers, &headers); err != nil {
		t.Fatal(err)
	}
	proof := state.OperationExecutionAuthority{AccountID: acct.ID, AppID: app.ID, InstanceID: instance.ID, InvocationID: inv.ID, Attempt: inv.Attempts, Capability: headers[api.OperationCapabilityHeader]}
	return op, inv, proof
}

func testOperationControlRead(t *testing.T, s operationControlFixtureStore) {
	t.Helper()
	ctx := t.Context()
	op, inv, proof := operationControlClaim(t, s)
	before, err := s.OperationByID(ctx, proof.AccountID, op.PlatformTenantID, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		control, err := s.OperationExecutionControl(ctx, op.ID, proof)
		if err != nil || control.OperationID != op.ID || control.InvocationID != inv.ID || control.Attempt != inv.Attempts || control.CancellationRequested || !control.LeaseExpiresAt.Equal(*inv.LeaseExpiresAt) || !control.DeadlineAt.Equal(*inv.DeadlineAt) || !control.LeaseExpiresAt.After(control.ObservedAt) {
			t.Fatalf("live observation: %+v %v", control, err)
		}
	}
	after, err := s.OperationByID(ctx, proof.AccountID, op.PlatformTenantID, op.ID)
	current, invErr := s.InvocationByID(ctx, inv.ID)
	if err != nil || invErr != nil || after.LatestSequence != before.LatestSequence || after.ReportCount != before.ReportCount || !current.LeaseExpiresAt.Equal(*inv.LeaseExpiresAt) {
		t.Fatal("control read changed durable events, reports or lease")
	}
	stale := proof
	stale.Attempt++
	if _, err := s.OperationExecutionControl(ctx, op.ID, stale); !errors.Is(err, state.ErrOperationStaleAttempt) {
		t.Fatal("replacement attempt was accepted", err)
	}
	foreign := proof
	foreign.AccountID = uuid.NewString()
	if _, err := s.OperationExecutionControl(ctx, op.ID, foreign); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign account read control", err)
	}
	if _, err := s.CancelOperation(ctx, proof.AccountID, op.PlatformTenantID, op.ID, 1); err != nil {
		t.Fatal(err)
	}
	control, err := s.OperationExecutionControl(ctx, op.ID, proof)
	if err != nil || !control.CancellationRequested {
		t.Fatal("cancellation intent was not observable", err)
	}
	if err := s.CompleteKeyedInvocation(ctx, inv.ID, inv.Attempts, []byte(`{"file":"already-generated.csv"}`)); err != nil {
		t.Fatal(err)
	}
	settled, err := s.OperationByID(ctx, proof.AccountID, op.PlatformTenantID, op.ID)
	if err != nil || settled.State != api.OperationSucceeded || !settled.CancellationRequested {
		t.Fatal("cancellation rewrote a confirmed outcome", err)
	}
	if _, err := s.OperationExecutionControl(ctx, op.ID, proof); !errors.Is(err, state.ErrOperationStaleAttempt) {
		t.Fatal("settled claim retained control authority", err)
	}
}

func TestMemOperationExecutionControl(t *testing.T) { testOperationControlRead(t, state.NewMemStore()) }
func TestPgOperationExecutionControl(t *testing.T) {
	s, _ := pgStore(t)
	testOperationControlRead(t, s)
}

func TestPgOperationExpiredDeadlineFencesRuntime(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	s := state.NewPgStore(pool)
	op, inv, proof := operationControlClaim(t, s)
	ctx := t.Context()
	artifact := api.OperationArtifactRequest{ReportID: "file", Name: "export.csv", URI: fmt.Sprintf("obj://%s/%s/export.csv", op.AppID, uuid.NewString()),
		SizeBytes: 3, SHA256: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("csv")))}
	blob, err := s.ReserveOperationArtifact(ctx, op.ID, proof, artifact)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE invocations SET deadline_at=clock_timestamp()-interval '1 second' WHERE id=$1", inv.ID); err != nil {
		t.Fatal(err)
	}
	// The earlier lease remains live; every runtime boundary must independently
	// fence the expired deadline, including the retained-copy commit path.
	for _, read := range []struct {
		name string
		call func() error
	}{
		{"control", func() error { _, err := s.OperationExecutionControl(ctx, op.ID, proof); return err }},
		{"progress", func() error {
			_, err := s.ReportOperationProgress(ctx, op.ID, proof, api.OperationReportRequest{ReportID: "progress", Stage: "generating", Total: 1})
			return err
		}},
		{"reserve", func() error { _, err := s.ReserveOperationArtifact(ctx, op.ID, proof, artifact); return err }},
		{"attach", func() error {
			_, err := s.AttachVerifiedOperationArtifact(ctx, op.ID, proof, artifact, blob.ID)
			return err
		}},
	} {
		t.Run(read.name, func(t *testing.T) {
			if err := read.call(); !errors.Is(err, state.ErrOperationStaleAttempt) {
				t.Fatal("expired deadline accepted by runtime", err)
			}
		})
	}
	got, err := s.OperationByID(ctx, proof.AccountID, op.PlatformTenantID, op.ID)
	if err != nil || got.ReportCount != 0 || len(got.Artifacts) != 0 || got.LatestSequence != 2 {
		t.Fatal("expired deadline mutated work", err)
	}
}
