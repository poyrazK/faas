// adr: 644
package neon

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// Regression: accepted targets lost their identity when lineage verification
// failed, so cleanup retried the same failed correctness check.
func TestRestoreCreationReceiptSurvivesFailedVerification(t *testing.T) {
	for _, mode := range []string{"wrong_point", "missing_point", "cancel_after_checkpoint", "checkpoint_failure"} {
		t.Run(mode, func(t *testing.T) {
			point := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
			request := managedpostgres.RestoreRequest{ResourceID: "target-owner", SourceResourceID: "project-source/br-source", Spec: testDatabaseSpec(), PointInTime: point, IdempotencyKey: "restore-owner"}
			posts, reads, inventories, deletes, checkpoints := 0, 0, 0, 0, 0
			absent := false
			var provider *Provider
			var saved managedpostgres.CreationAcknowledgement
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			provider = testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				target := branch{ID: "br-target", ProjectID: "project-source", ParentID: "br-source", Name: provider.restoreBranchName(request.ResourceID), InitSource: "parent-data", CurrentState: "ready", CreatedAt: point.Add(time.Minute).Format(time.RFC3339Nano)}
				if mode != "missing_point" {
					target.ParentTimestamp = point.Add(time.Second).Format(time.RFC3339Nano)
				}
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v2/projects/project-source/branches":
					inventories++
					writeResponse(t, w, http.StatusOK, map[string]any{"branches": []branch{}})
				case r.Method == http.MethodPost && r.URL.Path == "/api/v2/projects/project-source/branches":
					posts++
					writeResponse(t, w, http.StatusCreated, map[string]any{"branch": target})
				case r.Method == http.MethodGet && r.URL.Path == "/api/v2/projects/project-source/branches/br-target":
					reads++
					if absent {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					writeResponse(t, w, http.StatusOK, map[string]any{"branch": target})
				case r.Method == http.MethodDelete && r.URL.Path == "/api/v2/projects/project-source/branches/br-target":
					deletes++
					absent = true
					writeResponse(t, w, http.StatusOK, map[string]any{"operations": []operation{{ID: "delete-op", Status: "finished"}}})
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			_, err := provider.RestoreWithCreationReceipt(ctx, request, nil, func(_ context.Context, a managedpostgres.CreationAcknowledgement) error {
				checkpoints++
				if reads != 0 {
					t.Fatal("creation checkpoint delayed until metadata polling")
				}
				if mode == "checkpoint_failure" {
					return managedpostgres.ErrUnavailable
				}
				saved = a
				if mode == "cancel_after_checkpoint" {
					cancel()
				}
				return nil
			})
			if err == nil || posts != 1 || checkpoints != 1 {
				t.Fatalf("restore err=%v posts=%d checkpoints=%d", err, posts, checkpoints)
			}
			if mode == "checkpoint_failure" {
				if reads != 0 || saved.ProviderResourceID != "" {
					t.Fatal("continued after failed checkpoint")
				}
				return
			}
			if saved.ProviderResourceID != "project-source/br-target" || saved.SourceResourceID != request.SourceResourceID {
				t.Fatalf("custody: %+v", saved)
			}
			retryCtx, retryCancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer retryCancel()
			_, err = provider.RestoreWithCreationReceipt(retryCtx, request, &saved, nil)
			if err == nil || posts != 1 || inventories != 1 {
				t.Fatalf("retry created or adopted unverified target: %v posts=%d inventories=%d", err, posts, inventories)
			}
			result, err := provider.DeleteRestoreCreation(t.Context(), request, saved, managedpostgres.CreationCleanup{Started: deletes > 0, RecordStarted: func(context.Context) error { return nil }})
			if err != nil || !result.Done || deletes != 1 {
				t.Fatalf("owned cleanup: %+v %v deletes=%d", result, err, deletes)
			}
			result, err = provider.DeleteRestoreCreation(t.Context(), request, saved, managedpostgres.CreationCleanup{Started: deletes > 0, RecordStarted: func(context.Context) error { return nil }})
			if err != nil || !result.Done || deletes != 1 {
				t.Fatalf("cleanup replay: %+v %v deletes=%d", result, err, deletes)
			}
		})
	}
}

func TestRestoreCreationCleanupRefusesChangedCustody(t *testing.T) {
	for _, fault := range []string{"parent", "project", "owner", "created", "identity", "init", "pending_delete", "lost_delete_ack"} {
		t.Run(fault, func(t *testing.T) {
			point := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
			r := managedpostgres.RestoreRequest{ResourceID: "owner", SourceResourceID: "project/br-source", PointInTime: point, IdempotencyKey: "cleanup"}
			a := managedpostgres.CreationAcknowledgement{ProviderResourceID: "project/br-target", SourceResourceID: r.SourceResourceID, CreatedAt: point.Add(time.Minute)}
			deletes, reads := 0, 0
			var p *Provider
			p = testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path != "/api/v2/projects/project/branches/br-target" {
					t.Fatalf("foreign mutation: %s", req.URL.Path)
				}
				if req.Method == http.MethodDelete {
					deletes++
					if fault == "lost_delete_ack" {
						w.WriteHeader(http.StatusBadGateway)
					} else {
						writeResponse(t, w, http.StatusOK, map[string]any{"operations": []operation{{ID: "done", Status: "finished"}}})
					}
					return
				}
				reads++
				if deletes > 0 && fault == "lost_delete_ack" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				target := branch{ID: "br-target", ProjectID: "project", ParentID: "br-source", Name: p.restoreBranchName(r.ResourceID), InitSource: "parent-data", CurrentState: "ready", CreatedAt: a.CreatedAt.Format(time.RFC3339Nano)}
				switch fault {
				case "parent":
					target.ParentID = "br-foreign"
				case "project":
					target.ProjectID = "foreign"
				case "owner":
					target.Name = "foreign-owner"
				case "created":
					target.CreatedAt = a.CreatedAt.Add(time.Second).Format(time.RFC3339Nano)
				case "identity":
					target.ID = "br-other"
				case "init":
					target.InitSource = "schema-only"
				}
				writeResponse(t, w, http.StatusOK, map[string]any{"branch": target})
			}))
			result, err := p.DeleteRestoreCreation(t.Context(), r, a, managedpostgres.CreationCleanup{RecordStarted: func(context.Context) error { return nil }})
			if fault == "pending_delete" {
				if err != nil || result.Done || deletes != 1 || reads < 2 {
					t.Fatalf("ack retired present target: %+v %v deletes=%d reads=%d", result, err, deletes, reads)
				}
			} else if fault == "lost_delete_ack" {
				if err != nil || !result.Done || deletes != 1 {
					t.Fatalf("lost delete recovery: %+v %v", result, err)
				}
			} else if !errors.Is(err, managedpostgres.ErrConflict) || deletes != 0 {
				t.Fatalf("unsafe cleanup: %+v %v deletes=%d", result, err, deletes)
			}
		})
	}
}

// Regression: optional capture/retention metadata must not erase the accepted
// checkpoint identity needed to clean it up after capture fails.
func TestSnapshotCreationReceiptCleansUnverifiedCheckpoint(t *testing.T) {
	point := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	r := managedpostgres.SnapshotCaptureRequest{ResourceID: "owner", SourceResourceID: "project/br-source", PointInTime: point, IdempotencyKey: "capture"}
	posts, deletes, patches := 0, 0, 0
	absent := true
	var saved managedpostgres.CreationAcknowledgement
	var p *Provider
	p = testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		actual := snapshot{ID: "snapshot", Name: p.snapshotName(r.ResourceID), SourceBranchID: "br-source", CreatedAt: point.Add(time.Minute).Format(time.RFC3339Nano), Manual: true}
		switch req.Method {
		case http.MethodGet:
			list := []snapshot{}
			if !absent {
				list = append(list, actual)
			}
			writeResponse(t, w, http.StatusOK, map[string]any{"snapshots": list})
		case http.MethodPost:
			posts++
			absent = false
			writeResponse(t, w, http.StatusOK, map[string]any{"snapshot": actual})
		case http.MethodDelete:
			if req.URL.Path != "/api/v2/projects/project/snapshots/snapshot" {
				t.Fatalf("wrong deletion: %s", req.URL.Path)
			}
			deletes++
			absent = true
			w.WriteHeader(http.StatusAccepted)
		case http.MethodPatch:
			patches++
			w.WriteHeader(http.StatusOK)
		}
	}))
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	_, err := p.CaptureSnapshotWithCreationReceipt(ctx, r, nil, func(_ context.Context, a managedpostgres.CreationAcknowledgement) error { saved = a; return nil })
	if !errors.Is(err, managedpostgres.ErrUnavailable) || saved.ProviderResourceID != "project/snapshots/snapshot" || posts != 1 || patches != 0 {
		t.Fatalf("capture: saved=%+v err=%v posts=%d patches=%d", saved, err, posts, patches)
	}
	ctx2, cancel2 := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel2()
	_, err = p.CaptureSnapshotWithCreationReceipt(ctx2, r, &saved, nil)
	if !errors.Is(err, managedpostgres.ErrUnavailable) || posts != 1 {
		t.Fatalf("retry: %v posts=%d", err, posts)
	}
	result, err := p.DeleteSnapshotCreation(t.Context(), r, saved, managedpostgres.CreationCleanup{Started: deletes > 0, RecordStarted: func(context.Context) error { return nil }})
	if err != nil || !result.Done || deletes != 1 || patches != 0 {
		t.Fatalf("cleanup: %+v %v", result, err)
	}
	result, err = p.DeleteSnapshotCreation(t.Context(), r, saved, managedpostgres.CreationCleanup{Started: deletes > 0, RecordStarted: func(context.Context) error { return nil }})
	if err != nil || !result.Done || deletes != 1 {
		t.Fatalf("cleanup replay: %+v %v", result, err)
	}
}

func TestSnapshotCreationCleanupRefusesChangedCustody(t *testing.T) {
	for _, fault := range []string{"owner", "source", "created", "duplicate", "automatic", "pending_delete", "lost_delete_ack"} {
		t.Run(fault, func(t *testing.T) {
			point := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
			r := managedpostgres.SnapshotCaptureRequest{ResourceID: "owner", SourceResourceID: "project/br-source", PointInTime: point, IdempotencyKey: "capture"}
			a := managedpostgres.CreationAcknowledgement{ProviderResourceID: "project/snapshots/snapshot", SourceResourceID: r.SourceResourceID, CreatedAt: point.Add(time.Minute)}
			deletes := 0
			var p *Provider
			p = testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method == http.MethodDelete {
					deletes++
					if fault == "lost_delete_ack" {
						w.WriteHeader(http.StatusBadGateway)
					} else {
						w.WriteHeader(http.StatusAccepted)
					}
					return
				}
				actual := snapshot{ID: "snapshot", Name: p.snapshotName(r.ResourceID), SourceBranchID: "br-source", CreatedAt: a.CreatedAt.Format(time.RFC3339Nano), Manual: true}
				switch fault {
				case "owner":
					actual.Name = "foreign"
				case "source":
					actual.SourceBranchID = "br-foreign"
				case "created":
					actual.CreatedAt = a.CreatedAt.Add(time.Second).Format(time.RFC3339Nano)
				case "automatic":
					actual.Manual = false
				}
				list := []snapshot{actual}
				if fault == "duplicate" {
					list = append(list, actual)
				}
				if deletes > 0 && fault == "lost_delete_ack" {
					list = []snapshot{}
				}
				writeResponse(t, w, http.StatusOK, map[string]any{"snapshots": list})
			}))
			result, err := p.DeleteSnapshotCreation(t.Context(), r, a, managedpostgres.CreationCleanup{RecordStarted: func(context.Context) error { return nil }})
			if fault == "pending_delete" {
				if err != nil || result.Done || deletes != 1 {
					t.Fatalf("pending: %+v %v deletes=%d", result, err, deletes)
				}
			} else if fault == "lost_delete_ack" {
				if err != nil || !result.Done || deletes != 1 {
					t.Fatalf("lost ack: %+v %v", result, err)
				}
			} else if err == nil || deletes != 0 {
				t.Fatalf("foreign cleanup: %+v %v deletes=%d", result, err, deletes)
			}
		})
	}
}

// Regression: a creation acknowledgement followed by early inventory absence
// cannot finish compensation. Cleanup must checkpoint prior independent
// visibility, and that checkpoint must survive a lost write or delete reply.
func TestCreationCleanupDistinguishesDelayedVisibilityFromDeletionReplay(t *testing.T) {
	for _, kind := range []string{"restore", "snapshot"} {
		t.Run(kind, func(t *testing.T) {
			point := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
			ack := managedpostgres.CreationAcknowledgement{ProviderResourceID: "project/br-target", SourceResourceID: "project/br-source", CreatedAt: point.Add(time.Minute)}
			if kind == "snapshot" {
				ack.ProviderResourceID = "project/snapshots/snapshot"
			}
			present, checkpointed := false, false
			deletes, posts := 0, 0
			var p *Provider
			p = testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts++
					t.Error("cleanup created a resource")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if r.Method == http.MethodDelete {
					deletes++
					present = false
					w.WriteHeader(http.StatusBadGateway)
					return
				}
				if kind == "restore" {
					if !present {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					target := branch{ID: "br-target", ProjectID: "project", ParentID: "br-source", Name: p.restoreBranchName("owner"), InitSource: "parent-data", CurrentState: "ready", CreatedAt: ack.CreatedAt.Format(time.RFC3339Nano)}
					writeResponse(t, w, http.StatusOK, map[string]any{"branch": target})
				} else {
					list := []snapshot{}
					if present {
						list = append(list, snapshot{ID: "snapshot", Name: p.snapshotName("owner"), SourceBranchID: "br-source", CreatedAt: ack.CreatedAt.Format(time.RFC3339Nano), Manual: true})
					}
					writeResponse(t, w, http.StatusOK, map[string]any{"snapshots": list})
				}
			}))
			cleanup := func(c managedpostgres.CreationCleanup) (managedpostgres.DeleteResult, error) {
				if kind == "restore" {
					return p.DeleteRestoreCreation(t.Context(), managedpostgres.RestoreRequest{ResourceID: "owner", SourceResourceID: ack.SourceResourceID, PointInTime: point, IdempotencyKey: "cleanup"}, ack, c)
				}
				return p.DeleteSnapshotCreation(t.Context(), managedpostgres.SnapshotCaptureRequest{ResourceID: "owner", SourceResourceID: ack.SourceResourceID, PointInTime: point, IdempotencyKey: "cleanup"}, ack, c)
			}
			result, err := cleanup(managedpostgres.CreationCleanup{})
			if !errors.Is(err, managedpostgres.ErrUnavailable) || result.Done || deletes != 0 {
				t.Fatalf("early absence retired creation: %+v %v", result, err)
			}
			present = true
			// Commit the checkpoint but lose its reply: no mutation in this call.
			result, err = cleanup(managedpostgres.CreationCleanup{RecordStarted: func(context.Context) error { checkpointed = true; return managedpostgres.ErrUnavailable }})
			if !errors.Is(err, managedpostgres.ErrUnavailable) || result.Done || deletes != 0 || !checkpointed {
				t.Fatalf("lost checkpoint: %+v %v", result, err)
			}
			result, err = cleanup(managedpostgres.CreationCleanup{Started: checkpointed})
			if err != nil || !result.Done || deletes != 1 || posts != 0 {
				t.Fatalf("lost delete reply: %+v %v deletes=%d posts=%d", result, err, deletes, posts)
			}
			result, err = cleanup(managedpostgres.CreationCleanup{Started: checkpointed})
			if err != nil || !result.Done || deletes != 1 {
				t.Fatalf("cleanup replay: %+v %v deletes=%d", result, err, deletes)
			}
		})
	}
}
