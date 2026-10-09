package acceptance_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOperationWorkflowArtifactResume(t *testing.T) {
	operationWorkflowStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		f := workflowOperationSetup(t, store)
		op := f.start(t)
		artifacts := store.(state.OperationWorkflowArtifactStore)
		blobs := store.(state.OperationResultBlobStore)
		nodeID := uuid.NewString()
		if node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName); err == nil {
			nodeID = node.ID
		}
		instance, err := store.CreateInstance(ctx, f.app.ID, f.definition.DeploymentID, string(state.StateRunning), 128, nodeID, "")
		if err != nil {
			t.Fatal(err)
		}
		req := api.OperationArtifactRequest{ReportID: "csv", Name: "export.csv", URI: fmt.Sprintf("obj://%s/%s/export.csv", f.app.ID, uuid.NewString()), SizeBytes: 3, SHA256: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("csv")))}
		var old state.OperationWorkflowAuthority
		var artifactID, storageKey string
		var uploads, attempts int
		calls := map[string]int{}
		executor := operationWorkflowExecutor{execute: func(ctx context.Context, _ sched.WorkflowStepIdentity, path string, h map[string]string, _ []byte) (int, []byte, error) {
			calls[path]++
			generation, _ := strconv.Atoi(h[api.OperationGenerationHeader])
			attempt, _ := strconv.Atoi(h[api.OperationAttemptHeader])
			a := state.OperationWorkflowAuthority{AccountID: f.account.ID, AppID: f.app.ID, InstanceID: instance.ID, RunID: h[api.OperationWorkflowRunHeader], StepName: h[api.OperationWorkflowStepHeader], Generation: generation, Attempt: attempt, Capability: h[api.OperationWorkflowCapabilityHeader]}
			if a.RunID != op.WorkflowRunID || h[api.OperationExecutionKindHeader] != "workflow" || a.Capability == "" {
				t.Fatal("dispatch omitted native artifact proof")
			}
			if path != "/finish" {
				if _, err := artifacts.ReuseWorkflowOperationArtifact(ctx, op.ID, a, req); !errors.Is(err, state.ErrInvalidArgument) {
					t.Fatal("non-final step artifact authority", err)
				}
				return 200, []byte(`{"rows":[1]}`), nil
			}
			attempts++
			for _, mutate := range []func(*state.OperationWorkflowAuthority){func(a *state.OperationWorkflowAuthority) { a.AccountID = uuid.NewString() }, func(a *state.OperationWorkflowAuthority) { a.AppID = uuid.NewString() }, func(a *state.OperationWorkflowAuthority) { a.InstanceID = uuid.NewString() }, func(a *state.OperationWorkflowAuthority) { a.Generation++ }, func(a *state.OperationWorkflowAuthority) { a.Attempt++ }, func(a *state.OperationWorkflowAuthority) { a.Capability = uuid.NewString() }} {
				invalid := a
				mutate(&invalid)
				if _, err := artifacts.ReuseWorkflowOperationArtifact(ctx, op.ID, invalid, req); err == nil {
					t.Fatal("invalid proof accepted")
				}
			}
			before := f.read(ctx, t, op.ID)
			receipt, err := artifacts.ReuseWorkflowOperationArtifact(ctx, op.ID, a, req)
			if err != nil {
				t.Fatal(err)
			}
			if attempts == 1 {
				if receipt.Available {
					t.Fatal("nonexistent copy available")
				}
				old = a
				blob, _, err := artifacts.ReserveWorkflowOperationArtifact(ctx, op.ID, a, req)
				if err != nil {
					t.Fatal(err)
				}
				storageKey = blob.StorageKey
				uploads++
				// Concurrent acknowledgements (including a replay after a lost response) commit once.
				var wg sync.WaitGroup
				for range 8 {
					wg.Add(1)
					go func() {
						defer wg.Done()
						response, err := artifacts.PrepareVerifiedWorkflowOperationArtifact(ctx, op.ID, a, req, blob.ID)
						if err != nil || !response.Available {
							t.Errorf("prepare=%+v %v", response, err)
						}
					}()
				}
				wg.Wait()
				prepared := f.read(ctx, t, op.ID)
				if len(prepared.Artifacts) != 0 || prepared.ReportCount != before.ReportCount+1 {
					t.Fatalf("premature publication or duplicate reports: %+v", prepared)
				}
				receipt, err = artifacts.ReuseWorkflowOperationArtifact(ctx, op.ID, a, req)
				if err != nil || !receipt.Available {
					t.Fatal("lost response was not recoverable", err)
				}
				artifactID = receipt.Artifact.ID
				duplicateName := req
				duplicateName.ReportID = "different-file"
				if _, err := artifacts.ReuseWorkflowOperationArtifact(ctx, op.ID, a, duplicateName); !errors.Is(err, state.ErrConflict) {
					t.Fatal("preflight ignored duplicate filename", err)
				}
				changed := req
				changed.SHA256 = fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("bad")))
				if _, err := artifacts.ReuseWorkflowOperationArtifact(ctx, op.ID, a, changed); !errors.Is(err, state.ErrOperationInputConflict) {
					t.Fatal("changed declaration reused", err)
				}
				return 503, []byte(`{"error":"lost action response"}`), nil
			}
			if _, err := artifacts.ReuseWorkflowOperationArtifact(ctx, op.ID, old, req); !errors.Is(err, state.ErrOperationStaleAttempt) {
				t.Fatal("old proof reused after resume", err)
			}
			if !receipt.Available || receipt.Artifact.ID != artifactID {
				t.Fatal("approved resume lost verified copy")
			}
			rebound := f.read(ctx, t, op.ID)
			if len(rebound.Artifacts) != 0 || rebound.ArtifactStorageKeys[artifactID] != storageKey {
				t.Fatal("rebind published or replaced copy")
			}
			return 200, []byte(`{"file":"export.csv"}`), nil
		}}
		orchestrator := sched.NewWorkflowOrchestrator(store, executor, nil, nil, nil)
		if err := orchestrator.DispatchTick(ctx); err != nil {
			t.Fatal(err)
		}
		first := f.read(ctx, t, op.ID)
		if first.State != api.OperationRequiresReconciliation || len(first.Artifacts) != 0 || first.CompletionDelivery.State != "awaiting_outcome" {
			t.Fatalf("unconfirmed file exposed: %+v", first)
		}
		if _, err := artifacts.ReuseWorkflowOperationArtifact(ctx, op.ID, old, req); !errors.Is(err, state.ErrOperationStaleAttempt) {
			t.Fatal("settled action retained authority", err)
		}
		if _, err := blobs.ClaimOperationArtifactCleanup(ctx, "live-pending", time.Now().Add(api.OperationArtifactStagingLifetime+time.Second)); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("pending verified result collected", err)
		}
		assertOperationRecoveryPreviewReadOnly(t, f, op.ID)
		reader := store.(state.OperationRecoveryInspectionStore)
		preview, err := reader.PreviewOperationRecovery(ctx, f.account.ID, op.ID, api.OperationRecoveryPreviewRequest{ExpectedGeneration: 1, Resolution: "safe_to_retry"})
		if err != nil || !preview.Eligible || len(preview.Inspection.Artifacts) != 1 || !preview.Inspection.Artifacts[0].Retained || preview.Inspection.Artifacts[0].State != "prepared" || len(preview.ReusableArtifactIDs) != 1 || preview.ReusableArtifactIDs[0] != artifactID || len(preview.ReopenedSteps) != 1 || preview.ReopenedSteps[0] != "finish" || len(preview.ReusedSteps) != 2 {
			t.Fatalf("private file recovery preview=%+v %v", preview, err)
		}
		publication, err := reader.PreviewOperationRecovery(ctx, f.account.ID, op.ID, api.OperationRecoveryPreviewRequest{ExpectedGeneration: 1, Resolution: "succeeded", Result: []byte(`{"file":"export.csv"}`)})
		if err != nil || !publication.Eligible || len(publication.PublishArtifactIDs) != 1 || publication.PublishArtifactIDs[0] != artifactID || len(f.read(ctx, t, op.ID).Artifacts) != 0 {
			t.Fatal("publication preview changed visibility", err)
		}
		_, err = f.ops.RecoverOperation(ctx, f.account.ID, f.tenant.ID, op.ID, api.OperationRecoveryRequest{RecoveryID: "reuse-verified-copy", ExpectedInspectionRevision: preview.Inspection.InspectionRevision, ExpectedGeneration: 1, Resolution: "safe_to_retry", Evidence: "verified copy is retained; final action can reuse it without writing"})
		if err != nil {
			t.Fatal(err)
		}
		if err := orchestrator.DispatchTick(ctx); err != nil {
			t.Fatal(err)
		}
		complete := f.read(ctx, t, op.ID)
		if complete.State != api.OperationSucceeded || len(complete.Artifacts) != 1 || complete.Artifacts[0].ID != artifactID || complete.ArtifactStorageKeys[artifactID] != storageKey || uploads != 1 || calls["/collect"] != 1 || calls["/transform"] != 1 || calls["/finish"] != 2 {
			t.Fatalf("resume failed copy/prefix contract: %+v uploads=%d calls=%v", complete, uploads, calls)
		}
		deliveries, err := store.ClaimDueAppWebhookDeliveries(ctx, 10, time.Now().Add(time.Second))
		if err != nil || len(deliveries) != 1 {
			t.Fatalf("delivery=%v err=%v", deliveries, err)
		}
		delivery := deliveries[0]
		if err := store.MarkAppWebhookDeliveryDead(ctx, delivery.ID, delivery.Attempt, delivery.NextAttemptAt, "receiver unavailable"); err != nil {
			t.Fatal(err)
		}
		zero := 0
		if _, err := store.(state.OperationDeliveryStore).RetryOperationCompletionDelivery(ctx, f.account.ID, op.ID, api.OperationDeliveryRetryRequest{RetryID: "file-delivery-only", DeliveryID: complete.CompletionDelivery.DeliveryID, ExpectedReplayGeneration: &zero}); err != nil {
			t.Fatal(err)
		}
		if err := orchestrator.DispatchTick(ctx); err != nil || calls["/finish"] != 2 {
			t.Fatal("delivery replayed export", err)
		}
		events, err := f.ops.OperationEvents(ctx, f.account.ID, f.tenant.ID, op.ID, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		prepared, published := 0, 0
		for _, event := range events.Events {
			if event.Type == "artifact_prepared" {
				prepared++
			}
			if event.Type == "artifact_attached" {
				published++
			}
		}
		if prepared != 1 || published != 1 {
			t.Fatalf("duplicate file events prepared=%d published=%d", prepared, published)
		}
	})
}

func TestOperationWorkflowArtifactReconciliation(t *testing.T) {
	for _, resolution := range []string{"succeeded", "cancelled"} {
		t.Run(resolution, func(t *testing.T) {
			operationWorkflowStores(t, func(t *testing.T, store state.Store) {
				ctx := t.Context()
				f := workflowOperationSetup(t, store)
				op := f.start(t)
				nodeID := uuid.NewString()
				if node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName); err == nil {
					nodeID = node.ID
				}
				instance, err := store.CreateInstance(ctx, f.app.ID, f.definition.DeploymentID, string(state.StateRunning), 128, nodeID, "")
				if err != nil {
					t.Fatal(err)
				}
				artifacts := store.(state.OperationWorkflowArtifactStore)
				var blob state.OperationResultBlob
				executor := operationWorkflowExecutor{execute: func(ctx context.Context, _ sched.WorkflowStepIdentity, path string, h map[string]string, _ []byte) (int, []byte, error) {
					if path != "/finish" {
						return 200, []byte(`{"rows":[1]}`), nil
					}
					attempt, _ := strconv.Atoi(h[api.OperationAttemptHeader])
					a := state.OperationWorkflowAuthority{AccountID: f.account.ID, AppID: f.app.ID, InstanceID: instance.ID, RunID: op.WorkflowRunID, StepName: "finish", Generation: 1, Attempt: attempt, Capability: h[api.OperationWorkflowCapabilityHeader]}
					req := api.OperationArtifactRequest{ReportID: "csv", Name: "export.csv", URI: fmt.Sprintf("obj://%s/%s/export.csv", f.app.ID, uuid.NewString()), SizeBytes: 3, SHA256: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("csv")))}
					blob, _, err = artifacts.ReserveWorkflowOperationArtifact(ctx, op.ID, a, req)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := artifacts.PrepareVerifiedWorkflowOperationArtifact(ctx, op.ID, a, req, blob.ID); err != nil {
						t.Fatal(err)
					}
					control := store.(state.OperationWorkflowControlStore)
					if _, err := control.WorkflowOperationExecutionControl(ctx, op.ID, a); err != nil {
						t.Fatal("live control denied", err)
					}
					if resolution == "cancelled" {
						// The cancellation fences this live nonce without exposing the verified file.
						if _, err := f.ops.CancelOperation(ctx, f.account.ID, f.tenant.ID, op.ID, 1); err != nil {
							t.Fatal(err)
						}
						if _, err := control.WorkflowOperationExecutionControl(ctx, op.ID, a); !errors.Is(err, state.ErrOperationStaleAttempt) {
							t.Fatal("cancelled control proof remained live", err)
						}
						if _, err := artifacts.ReuseWorkflowOperationArtifact(ctx, op.ID, a, req); !errors.Is(err, state.ErrOperationStaleAttempt) {
							t.Fatal("cancelled proof remained live", err)
						}
					}
					return 503, []byte(`{"error":"unknown"}`), nil
				}}
				orchestrator := sched.NewWorkflowOrchestrator(store, executor, nil, nil, nil)
				if err := orchestrator.DispatchTick(ctx); err != nil {
					t.Fatal(err)
				}
				before := f.read(ctx, t, op.ID)
				if before.State != api.OperationRequiresReconciliation || len(before.Artifacts) != 0 {
					t.Fatal("file exposed before reconciliation")
				}
				recovery := api.OperationRecoveryRequest{RecoveryID: "operator-confirmed", ExpectedGeneration: 1, Resolution: resolution, Evidence: "verified customer result reviewed against provider receipt"}
				if resolution == "succeeded" {
					recovery.Result = []byte(`{"file":"export.csv"}`)
				}
				final, err := f.ops.RecoverOperation(ctx, f.account.ID, f.tenant.ID, op.ID, recovery)
				if err != nil {
					t.Fatal(err)
				}
				expected := 0
				if resolution == "succeeded" {
					expected = 1
				}
				if len(final.Artifacts) != expected {
					t.Fatalf("wrong publication after %s: %+v", resolution, final)
				}
				if len(final.Artifacts) > 0 && !final.Artifacts[0].ExpiresAt.Equal(final.ExpiresAt) {
					t.Fatal("artifact retention not refreshed")
				}
				// Publication and the terminal event share the operation sequence without gaps.
				events, err := f.ops.OperationEvents(ctx, f.account.ID, f.tenant.ID, op.ID, 0, 100)
				if err != nil {
					t.Fatal(err)
				}
				for i, event := range events.Events {
					if event.Sequence != int64(i+1) {
						t.Fatal("reconciliation introduced event sequence gap", event.Sequence)
					}
				}
				blobs := store.(state.OperationResultBlobStore)
				claim, err := blobs.ClaimOperationArtifactCleanup(ctx, "expired-copy", final.ExpiresAt.Add(time.Second))
				if err != nil || claim.ID != blob.ID {
					t.Fatalf("expired receipt leaked: %+v %v", claim, err)
				}
			})
		})
	}
}
