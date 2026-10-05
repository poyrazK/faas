package acceptance_test

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type artifactStore interface {
	operationLifecycleStore
	state.OperationArtifactStore
	state.OperationResultBlobStore
}

func testOperationArtifactBinding(t *testing.T, s artifactStore) {
	ctx, acct, app, def, alice, _ := operationFixture(t, s)
	op, _, err := s.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID, PlatformTenantID: alice.ID, IdempotencyKey: "artifact", Input: []byte(`{"count":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := s.CreateInstance(ctx, app.ID, def.DeploymentID, "stopped", 128, node.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	inv, err := s.ClaimInvocationWithCap(ctx, op.CurrentInvocationID, instance.ID, 60, 100)
	if err != nil {
		t.Fatal(err)
	}
	var headers map[string]string
	_ = json.Unmarshal(inv.Headers, &headers)
	authority := state.OperationExecutionAuthority{AccountID: acct.ID, AppID: app.ID, InstanceID: instance.ID, InvocationID: inv.ID, Attempt: inv.Attempts, Capability: headers[api.OperationCapabilityHeader]}
	req := api.OperationArtifactRequest{ReportID: "export-file", Name: "export.csv", URI: fmt.Sprintf("obj://%s/%s/export.csv", app.ID, uuid.NewString()), SizeBytes: 3, SHA256: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("csv")))}
	blob, err := s.ReserveOperationArtifact(ctx, op.ID, authority, req)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.AttachVerifiedOperationArtifact(ctx, op.ID, authority, req, blob.ID); err != nil {
				t.Errorf("duplicate attachment: %v", err)
			}
		}()
	}
	wg.Wait()
	op, err = s.OperationByID(ctx, acct.ID, alice.ID, op.ID)
	if err != nil || len(op.Artifacts) != 1 || op.ReportCount != 1 || op.LatestSequence != 3 {
		t.Fatalf("atomic artifact receipt: %+v %v", op, err)
	}
	changed := req
	changed.Name = "other.csv"
	if _, err := s.AttachVerifiedOperationArtifact(ctx, op.ID, authority, changed, blob.ID); !errors.Is(err, state.ErrOperationInputConflict) {
		t.Fatalf("attachment conflict: %v", err)
	}
	foreign := req
	foreign.ReportID = "foreign"
	foreign.URI = fmt.Sprintf("obj://%s/%s/export.csv", uuid.NewString(), uuid.NewString())
	if _, err := s.ReserveOperationArtifact(ctx, op.ID, authority, foreign); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign artifact: %v", err)
	}
	stale := authority
	stale.Attempt++
	if _, err := s.AttachVerifiedOperationArtifact(ctx, op.ID, stale, req, blob.ID); !errors.Is(err, state.ErrOperationStaleAttempt) {
		t.Fatalf("stale attachment: %v", err)
	}
	if err := s.CompleteKeyedInvocation(ctx, inv.ID, inv.Attempts, []byte(`{"file":"export.csv"}`)); err != nil {
		t.Fatal(err)
	}
	completed, err := s.OperationByID(ctx, acct.ID, alice.ID, op.ID)
	if err != nil || completed.State != api.OperationSucceeded || len(completed.Artifacts) != 1 || completed.Artifacts[0].ExpiresAt == nil || !completed.Artifacts[0].ExpiresAt.Equal(completed.ExpiresAt) {
		t.Fatalf("artifact result retention: %+v %v", completed, err)
	}
}
func TestMemOperationArtifacts(t *testing.T) { testOperationArtifactBinding(t, state.NewMemStore()) }
func TestPgOperationArtifacts(t *testing.T)  { s, _ := pgStore(t); testOperationArtifactBinding(t, s) }
