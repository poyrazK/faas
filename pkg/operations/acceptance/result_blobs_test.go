package acceptance_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type resultBlobStore interface {
	artifactStore
	state.OperationMetricsStore
	DeleteAccount(context.Context, string) error
	UpdateAccountStatus(context.Context, string, state.AccountStatus) error
}

func resultBlobFixture(t *testing.T, s artifactStore) (state.Operation, state.Invocation, state.OperationExecutionAuthority, api.OperationArtifactRequest) {
	t.Helper()
	ctx, acct, app, def, alice, _ := operationFixture(t, s)
	op, _, err := s.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID, PlatformTenantID: alice.ID, IdempotencyKey: "retained-file", Input: []byte(`{"count":1}`)})
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
	if err := json.Unmarshal(inv.Headers, &headers); err != nil {
		t.Fatal(err)
	}
	authority := state.OperationExecutionAuthority{AccountID: acct.ID, AppID: app.ID, InstanceID: instance.ID, InvocationID: inv.ID, Attempt: inv.Attempts, Capability: headers[api.OperationCapabilityHeader]}
	req := api.OperationArtifactRequest{ReportID: "file", Name: "export.csv", URI: fmt.Sprintf("obj://%s/%s/export.csv", app.ID, uuid.NewString()), SizeBytes: 3, SHA256: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("csv")))}
	return op, inv, authority, req
}

func testOperationResultBlobRetention(t *testing.T, s resultBlobStore) {
	ctx := t.Context()
	op, inv, authority, req := resultBlobFixture(t, s)
	reserve := func(r api.OperationArtifactRequest) state.OperationResultBlob {
		t.Helper()
		blob, err := s.ReserveOperationArtifact(ctx, op.ID, authority, r)
		if err != nil {
			t.Fatal(err)
		}
		return blob
	}
	blob := reserve(req)
	orphanReq := req
	orphanReq.ReportID, orphanReq.Name = "abandoned", "abandoned.csv"
	orphan := reserve(orphanReq)
	metrics, err := s.OperationMetrics(ctx, time.Now())
	if err != nil || len(metrics.ResultBlobs) != 1 || metrics.ResultBlobs[0].State != "staging" || metrics.ResultBlobs[0].Count != 2 || metrics.ResultBlobs[0].Bytes != 6 {
		t.Fatalf("storage reservation health: %+v %v", metrics.ResultBlobs, err)
	}
	if _, err := s.ClaimOperationArtifactCleanup(ctx, "early", time.Now()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("collected live upload: %v", err)
	}
	attached, err := s.AttachVerifiedOperationArtifact(ctx, op.ID, authority, req, blob.ID)
	if err != nil || attached.ArtifactStorageKeys[attached.Artifacts[0].ID] != blob.StorageKey {
		t.Fatalf("missing atomic result binding: %+v %v", attached, err)
	}
	replay, err := s.ReserveOperationArtifact(ctx, op.ID, authority, req)
	if err != nil || replay.ID != blob.ID || replay.State != "retained" {
		t.Fatalf("duplicate reservation did not reuse receipt: %+v %v", replay, err)
	}
	future := orphan.ExpiresAt.Add(time.Second)
	claim, err := s.ClaimOperationArtifactCleanup(ctx, "first", future)
	if err != nil || claim.ID != orphan.ID {
		t.Fatalf("abandoned upload cleanup: %+v %v", claim, err)
	}
	if _, err := s.AttachVerifiedOperationArtifact(ctx, op.ID, authority, orphanReq, orphan.ID); !errors.Is(err, state.ErrOperationStaleAttempt) {
		t.Fatalf("committed collected upload: %v", err)
	}
	if err := s.CompleteOperationArtifactCleanup(ctx, orphan.ID, "wrong"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unfenced cleanup: %v", err)
	}
	next := future.Add(api.OperationArtifactCleanupRetry)
	if err := s.RetryOperationArtifactCleanup(ctx, orphan.ID, claim.LeaseToken, next); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimOperationArtifactCleanup(ctx, "too-soon", next.Add(-time.Second)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("lost retry backoff: %v", err)
	}
	claim, err = s.ClaimOperationArtifactCleanup(ctx, "second", next)
	if err != nil || claim.ID != orphan.ID {
		t.Fatalf("lost cleanup after restart: %+v %v", claim, err)
	}
	if err := s.CompleteOperationArtifactCleanup(ctx, orphan.ID, "first"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale cleanup owner: %v", err)
	}
	if err := s.CompleteOperationArtifactCleanup(ctx, orphan.ID, claim.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteKeyedInvocation(ctx, inv.ID, inv.Attempts, []byte(`{"file":"export.csv"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimOperationArtifactCleanup(ctx, "pin", next); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("collected retained successful result: %v", err)
	}
	if err := s.UpdateAccountStatus(ctx, op.AccountID, state.AccountDeletedPending); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount(ctx, op.AccountID); err != nil {
		t.Fatal(err)
	}
	claim, err = s.ClaimOperationArtifactCleanup(ctx, "deleted-owner", next)
	if err != nil || claim.ID != blob.ID {
		t.Fatalf("owner deletion lost cleanup intent: %+v %v", claim, err)
	}
	if err := s.CompleteOperationArtifactCleanup(ctx, blob.ID, claim.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimOperationArtifactCleanup(ctx, "empty", next); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cleanup did not drain: %v", err)
	}
}

func testOperationResultBlobQuota(t *testing.T, s artifactStore) {
	ctx := t.Context()
	op, _, authority, req := resultBlobFixture(t, s)
	req.SizeBytes = op.PlanLimits.ArtifactMaxBytes
	count := int(op.PlanLimits.RetainedArtifactBytesPerAccount / req.SizeBytes)
	var wg sync.WaitGroup
	failures := make(chan error, count)
	for i := 0; i < count; i++ {
		r := req
		r.ReportID = fmt.Sprintf("staging-%d", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.ReserveOperationArtifact(ctx, op.ID, authority, r)
			if err != nil {
				failures <- err
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	req.ReportID = "overflow"
	if _, err := s.ReserveOperationArtifact(ctx, op.ID, authority, req); !errors.Is(err, state.ErrOperationQuota) {
		t.Fatalf("unbounded staged bytes: %v", err)
	}
	future := time.Now().Add(api.OperationArtifactStagingLifetime + time.Second)
	for i := 0; i < count; i++ {
		claim, err := s.ClaimOperationArtifactCleanup(ctx, fmt.Sprintf("quota-%d", i), future)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.CompleteOperationArtifactCleanup(ctx, claim.ID, claim.LeaseToken); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.ReserveOperationArtifact(ctx, op.ID, authority, req); err != nil {
		t.Fatalf("cleanup did not release quota: %v", err)
	}
	// Empty files consume no byte budget but still reserve durable receipts.
	// This bound prevents a stream of zero-byte uploads growing the ledger.
	req.SizeBytes = 0
	for i := 1; i < op.PlanLimits.RetainedArtifactsPerAccount; i++ {
		req.ReportID = fmt.Sprintf("empty-%d", i)
		if _, err := s.ReserveOperationArtifact(ctx, op.ID, authority, req); err != nil {
			t.Fatal(err)
		}
	}
	req.ReportID = "empty-overflow"
	if _, err := s.ReserveOperationArtifact(ctx, op.ID, authority, req); !errors.Is(err, state.ErrOperationQuota) {
		t.Fatalf("unbounded empty upload receipts: %v", err)
	}
}

func TestMemOperationResultBlobRetention(t *testing.T) {
	testOperationResultBlobRetention(t, state.NewMemStore())
}
func TestPgOperationResultBlobRetention(t *testing.T) {
	s, _ := pgStore(t)
	testOperationResultBlobRetention(t, s)
}
func TestMemOperationResultBlobQuota(t *testing.T) {
	testOperationResultBlobQuota(t, state.NewMemStore())
}
func TestPgOperationResultBlobQuota(t *testing.T) {
	s, _ := pgStore(t)
	testOperationResultBlobQuota(t, s)
}
