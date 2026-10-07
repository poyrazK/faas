// adr: 603
package acceptance_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/state"
)

func jobArtifactDeclaration(op state.Operation) api.OperationArtifactRequest {
	return api.OperationArtifactRequest{ReportID: "csv", Name: "export.csv", URI: fmt.Sprintf("obj://%s/%s/export.csv", op.AppID, uuid.NewString()), SizeBytes: 3, SHA256: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("csv")))}
}

func TestOperationJobArtifactConcurrentReplayAndPublication(t *testing.T) {
	operationWorkflowStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		f := jobOperationSetup(t, store)
		op := f.start(t)
		a, lease := f.claim(t, op, time.Now().Add(time.Minute))
		files := store.(state.OperationJobArtifactStore)
		blobs := store.(state.OperationResultBlobStore)
		req := jobArtifactDeclaration(op)
		absent, err := files.ReuseJobOperationArtifact(ctx, op.ID, a, req)
		if err != nil || absent.Available || absent.Artifact != nil {
			t.Fatal("false receipt", err)
		}
		// Reserve every competing copy before any commits, exercising orphan GC.
		intents := make([]state.OperationResultBlob, 8)
		for i := range intents {
			intents[i], _, err = files.ReserveJobOperationArtifact(ctx, op.ID, a, req)
			if err != nil || intents[i].JobRunID != op.JobRunID || intents[i].ExecutionID != "" || intents[i].WorkflowRunID != "" {
				t.Fatal("mixed intent", err)
			}
		}
		var wg sync.WaitGroup
		ids := make(chan string, len(intents))
		for _, blob := range intents {
			wg.Add(1)
			go func(blob state.OperationResultBlob) {
				defer wg.Done()
				response, err := files.PrepareVerifiedJobOperationArtifact(ctx, op.ID, a, req, blob.ID)
				if err != nil || !response.Available || response.Artifact == nil {
					t.Error("prepare", err)
					return
				}
				ids <- response.Artifact.ID
			}(blob)
		}
		wg.Wait()
		close(ids)
		var artifactID string
		for id := range ids {
			if artifactID != "" && artifactID != id {
				t.Fatal("competing receipt")
			}
			artifactID = id
		}
		replay, err := files.ReuseJobOperationArtifact(ctx, op.ID, a, req)
		if err != nil || !replay.Available || replay.Artifact.ID != artifactID {
			t.Fatal("lost acknowledgement", err)
		}
		got, _ := f.ops.OperationByID(ctx, f.account.ID, f.tenant.ID, op.ID)
		if len(got.Artifacts) != 0 || got.ReportCount != 1 || len(got.ArtifactStorageKeys) != 1 {
			t.Fatal("copy published or duplicated")
		}
		for _, mutate := range []func(*api.OperationArtifactRequest){func(r *api.OperationArtifactRequest) { r.SHA256 = "sha256:" + strings.Repeat("0", 64) }, func(r *api.OperationArtifactRequest) { r.URI += "changed" }, func(r *api.OperationArtifactRequest) { r.ReportID = "different" }} {
			changed := req
			mutate(&changed)
			if _, err := files.ReuseJobOperationArtifact(ctx, op.ID, a, changed); err == nil {
				t.Fatal("conflicting declaration accepted")
			}
		}
		if _, err := f.runtime.ReportOperationJob(ctx, op.ID, a, "result", api.OperationJobReportRequest{ReportID: req.ReportID, Result: []byte(`{"file":"export.csv"}`)}); !errors.Is(err, state.ErrOperationInputConflict) {
			t.Fatal("report namespace collision", err)
		}
		if _, err := f.runtime.ReportOperationJob(ctx, op.ID, a, "result", api.OperationJobReportRequest{ReportID: "result", Result: []byte(`{"file":"export.csv"}`)}); err != nil {
			t.Fatal(err)
		}
		if err := store.JobTaskCompleteClaimedWithLogs(ctx, op.JobRunID, 0, a.InstanceID, lease, "succeeded", 0, "", "", "", false, time.Now()); err != nil {
			t.Fatal(err)
		}
		final, err := f.ops.OperationByID(ctx, f.account.ID, f.tenant.ID, op.ID)
		if err != nil || final.State != api.OperationSucceeded || len(final.Artifacts) != 1 || final.Artifacts[0].ID != artifactID || !final.Artifacts[0].ExpiresAt.Equal(final.ExpiresAt) {
			t.Fatal("publication", err)
		}
		events, err := f.ops.OperationEvents(ctx, f.account.ID, f.tenant.ID, op.ID, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		prepared, attached := 0, 0
		for i, event := range events.Events {
			if event.Sequence != int64(i+1) {
				t.Fatal("sequence gap")
			}
			if event.Type == "artifact_prepared" {
				prepared++
			}
			if event.Type == "artifact_attached" {
				attached++
				var payload struct {
					Artifact api.OperationResultArtifact `json:"artifact"`
				}
				if err := json.Unmarshal(event.Data, &payload); err != nil || payload.Artifact.ExpiresAt == nil || !payload.Artifact.ExpiresAt.Equal(final.ExpiresAt) {
					t.Fatal("publication event used stale retention", err)
				}
			}
			if event.Type == "succeeded" && attached != 1 {
				t.Fatal("completion before publication")
			}
		}
		if prepared != 1 || attached != 1 {
			t.Fatal("duplicate file events")
		}
		deliveries, err := store.ClaimDueAppWebhookDeliveries(ctx, 1, time.Now().Add(time.Second))
		if err != nil || len(deliveries) != 1 {
			t.Fatal("completion outbox", err)
		}
		delivery := deliveries[0]
		if err := store.MarkAppWebhookDeliveryDead(ctx, delivery.ID, delivery.Attempt, delivery.NextAttemptAt, "receiver unavailable"); err != nil {
			t.Fatal(err)
		}
		final, _ = f.ops.OperationByID(ctx, f.account.ID, f.tenant.ID, op.ID)
		if final.State != api.OperationSucceeded || len(final.Artifacts) != 1 || final.CompletionDelivery.State != "dead" {
			t.Fatal("delivery changed business")
		}
		if _, err := files.ReuseJobOperationArtifact(ctx, op.ID, a, req); !errors.Is(err, state.ErrOperationStaleAttempt) {
			t.Fatal("closed authority", err)
		}
		// Seven losing staging intents are collectable; the winner is pinned.
		for i := 0; i < len(intents)-1; i++ {
			claim, err := blobs.ClaimOperationArtifactCleanup(ctx, uuid.NewString(), time.Now().Add(api.OperationArtifactStagingLifetime+time.Second))
			if err != nil || claim.StorageKey == final.ArtifactStorageKeys[artifactID] {
				t.Fatal("winner collected or orphan lost", err)
			}
			if err := blobs.CompleteOperationArtifactCleanup(ctx, claim.ID, claim.LeaseToken); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := blobs.ClaimOperationArtifactCleanup(ctx, "live-winner", time.Now().Add(api.OperationArtifactStagingLifetime+time.Second)); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("live copy collected", err)
		}
	})
}

func TestOperationJobArtifactRecovery(t *testing.T) {
	for _, source := range []string{"managed", "direct"} {
		t.Run(source, func(t *testing.T) { testOperationJobArtifactRecovery(t, source == "direct") })
	}
}

func testOperationJobArtifactRecovery(t *testing.T, direct bool) {
	for _, resolution := range []string{"succeeded", "failed", "cancelled", "safe_to_retry"} {
		t.Run(resolution, func(t *testing.T) {
			operationWorkflowStores(t, func(t *testing.T, store state.Store) {
				ctx := context.Background()
				f := jobOperationSetup(t, store)
				op := f.start(t)
				a, lease := f.claim(t, op, time.Now().Add(time.Minute))
				files := store.(state.OperationJobArtifactStore)
				req := jobArtifactDeclaration(op)
				if direct {
					req = operations.JobUploadArtifactDeclaration(op.ID, a.RunID, a.Attempt, api.OperationArtifactUploadRequest{ReportID: req.ReportID, Name: req.Name, SizeBytes: req.SizeBytes, SHA256: req.SHA256})
				}
				blob, _, err := files.ReserveJobOperationArtifact(ctx, op.ID, a, req)
				if err != nil {
					t.Fatal(err)
				}
				response, err := files.PrepareVerifiedJobOperationArtifact(ctx, op.ID, a, req, blob.ID)
				if err != nil || !response.Available {
					t.Fatal(err)
				}
				if err := store.JobTaskCompleteClaimedWithLogs(ctx, op.JobRunID, 0, a.InstanceID, lease, "failed", 1, "", "", "", false, time.Now()); err != nil {
					t.Fatal(err)
				}
				inspector := store.(state.OperationRecoveryInspectionStore)
				result := []byte(`{"file":"export.csv"}`)
				preview, err := inspector.PreviewOperationRecovery(ctx, f.account.ID, op.ID, api.OperationRecoveryPreviewRequest{ExpectedGeneration: 1, Resolution: resolution, Result: func() []byte {
					if resolution == "succeeded" {
						return result
					}
					return nil
				}()})
				if err != nil || !preview.Eligible || len(preview.Inspection.Artifacts) != 1 || !preview.Inspection.Artifacts[0].Retained || preview.Inspection.Artifacts[0].State != "prepared" {
					t.Fatal("recovery inspection", err)
				}
				raw, err := json.Marshal(preview.Inspection)
				if err != nil || strings.Contains(string(raw), a.Capability) || strings.Contains(string(raw), blob.StorageKey) || strings.Contains(string(raw), req.URI) {
					t.Fatal("private artifact authority leaked in recovery inspection", err)
				}
				if resolution == "succeeded" && len(preview.PublishArtifactIDs) != 1 {
					t.Fatal("publication omitted from preview")
				}
				if resolution == "safe_to_retry" && (!preview.ClearsArtifactReferences || len(preview.ReusableArtifactIDs) != 0) {
					t.Fatal("uncertain Job file reused")
				}
				recovery := api.OperationRecoveryRequest{RecoveryID: "confirmed-provider", ExpectedGeneration: 1, ExpectedInspectionRevision: preview.Inspection.InspectionRevision, Resolution: resolution, Evidence: "provider outcome reviewed"}
				if resolution == "succeeded" {
					recovery.Result = result
				}
				final, err := f.ops.RecoverOperation(ctx, f.account.ID, f.tenant.ID, op.ID, recovery)
				if err != nil {
					t.Fatal(err)
				}
				if resolution == "succeeded" {
					if len(final.Artifacts) != 1 || final.Artifacts[0].ID != response.Artifact.ID {
						t.Fatal("confirmed file missing")
					}
				} else if len(final.Artifacts) != 0 {
					t.Fatal("unconfirmed file published")
				}
				if resolution == "safe_to_retry" {
					if len(final.JobArtifactReceipts) != 0 || len(final.ArtifactStorageKeys) != 0 || final.Generation != 2 || final.JobRunID == op.JobRunID {
						t.Fatal("retry reused receipts")
					}
					if _, err := files.PrepareVerifiedJobOperationArtifact(ctx, op.ID, a, req, blob.ID); !errors.Is(err, state.ErrOperationStaleAttempt) {
						t.Fatal("stale generation", err)
					}
					fresh, _ := f.claim(t, final, time.Now().Add(time.Minute))
					nextReq := req
					if direct {
						nextReq = operations.JobUploadArtifactDeclaration(op.ID, fresh.RunID, fresh.Attempt, api.OperationArtifactUploadRequest{ReportID: req.ReportID, Name: req.Name, SizeBytes: req.SizeBytes, SHA256: req.SHA256})
					}
					if direct && nextReq.URI == req.URI {
						t.Fatal("direct receipt crossed generation")
					}
					if _, err := files.PrepareVerifiedJobOperationArtifact(ctx, op.ID, fresh, nextReq, blob.ID); !errors.Is(err, state.ErrConflict) {
						t.Fatal("old blob crossed generation", err)
					}
					absent, err := files.ReuseJobOperationArtifact(ctx, op.ID, fresh, nextReq)
					if err != nil || absent.Available {
						t.Fatal("old receipt crossed generation", err)
					}
				}
				blobs := store.(state.OperationResultBlobStore)
				claim, err := blobs.ClaimOperationArtifactCleanup(ctx, "retention-ended", final.ExpiresAt.Add(time.Second))
				if err != nil || claim.ID != blob.ID {
					t.Fatal("expired or unbound copy leaked", err)
				}
			})
		})
	}
}

func TestOperationJobArtifactCommitFences(t *testing.T) {
	for _, reason := range []string{"lease", "customer", "cancellation", "instance"} {
		t.Run(reason, func(t *testing.T) {
			operationWorkflowStores(t, func(t *testing.T, store state.Store) {
				ctx := context.Background()
				f := jobOperationSetup(t, store)
				op := f.start(t)
				expiry := time.Now().Add(time.Minute)
				if reason == "lease" {
					expiry = time.Now().Add(2 * time.Second)
				}
				a, _ := f.claim(t, op, expiry)
				files := store.(state.OperationJobArtifactStore)
				req := jobArtifactDeclaration(op)
				blob, _, err := files.ReserveJobOperationArtifact(ctx, op.ID, a, req)
				if err != nil {
					t.Fatal(err)
				}
				switch reason {
				case "lease":
					time.Sleep(time.Until(expiry) + time.Millisecond)
				case "customer":
					_, err = store.(state.PlatformTenantStore).SetPlatformTenantStatus(ctx, f.account.ID, f.tenant.ID, state.PlatformTenantSuspended)
				case "cancellation":
					_, err = f.ops.CancelOperation(ctx, f.account.ID, f.tenant.ID, op.ID, 1)
				case "instance":
					err = store.UpdateInstanceState(ctx, a.InstanceID, string(state.StateStopped))
				}
				if err != nil {
					t.Fatal(err)
				}
				if _, err := files.PrepareVerifiedJobOperationArtifact(ctx, op.ID, a, req, blob.ID); err == nil {
					t.Fatal("post-copy fence bypassed", reason)
				}
				got, _ := f.ops.OperationByID(ctx, f.account.ID, f.tenant.ID, op.ID)
				if len(got.ArtifactStorageKeys) != 0 || len(got.JobArtifactReceipts) != 0 {
					t.Fatal("rejected copy bound")
				}
			})
		})
	}
}

func TestOperationJobArtifactLimits(t *testing.T) {
	for _, limit := range []string{"total", "count", "account_staging"} {
		t.Run(limit, func(t *testing.T) {
			operationWorkflowStores(t, func(t *testing.T, store state.Store) {
				ctx := context.Background()
				f := jobOperationSetup(t, store)
				op := f.start(t)
				a, _ := f.claim(t, op, time.Now().Add(time.Minute))
				files := store.(state.OperationJobArtifactStore)
				req := jobArtifactDeclaration(op)
				oversized := req
				oversized.SizeBytes = op.PlanLimits.ArtifactMaxBytes + 1
				if _, _, err := files.ReserveJobOperationArtifact(ctx, op.ID, a, oversized); !errors.Is(err, state.ErrInvalidArgument) {
					t.Fatal("oversized file admitted", err)
				}
				count := op.PlanLimits.ArtifactsPerOperation
				switch limit {
				case "total":
					req.SizeBytes = op.PlanLimits.ArtifactMaxBytes
					count = int(op.PlanLimits.ArtifactTotalMaxBytes / req.SizeBytes)
				case "count":
					req.SizeBytes = 0
				case "account_staging":
					req.SizeBytes = op.PlanLimits.ArtifactMaxBytes
					count = int(op.PlanLimits.RetainedArtifactBytesPerAccount / req.SizeBytes)
				}
				for i := 0; i < count; i++ {
					req.ReportID = fmt.Sprintf("file-%d", i)
					req.Name = fmt.Sprintf("export-%d.csv", i)
					blob, _, err := files.ReserveJobOperationArtifact(ctx, op.ID, a, req)
					if err != nil {
						t.Fatal("within limit rejected", err)
					}
					if limit != "account_staging" {
						if _, err := files.PrepareVerifiedJobOperationArtifact(ctx, op.ID, a, req, blob.ID); err != nil {
							t.Fatal(err)
						}
					}
				}
				req.ReportID = "overflow"
				req.Name = "overflow.csv"
				if _, _, err := files.ReserveJobOperationArtifact(ctx, op.ID, a, req); !errors.Is(err, state.ErrOperationQuota) {
					t.Fatal("artifact quota bypassed", limit, err)
				}
			})
		})
	}
}
