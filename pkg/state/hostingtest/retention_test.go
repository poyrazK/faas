package hostingtest

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
	"github.com/onebox-faas/faas/pkg/state"
)

func hostingRetentionLifecycle(t *testing.T, store hostingTestStore) {
	// adr: 202610171001 — dry runs are read-only; retirement is atomic and keeps CI identities.
	account, site := seedDurable(t, store)
	ctx := t.Context()
	now := time.Now().UTC().Add(26 * time.Hour)
	create := func(ready bool) api.HostingRelease {
		r, err := store.CreateHostingRelease(ctx, account.ID, site.ID, manifest())
		if err != nil {
			t.Fatal(err)
		}
		if ready {
			r, err = store.ReadyHostingRelease(ctx, account.ID, site.ID, r.ID)
			if err != nil {
				t.Fatal(err)
			}
		}
		return r
	}
	published := create(true)
	var err error
	site, err = store.PromoteHostingRelease(ctx, account.ID, site.ID, published.ID, site.Revision)
	if err != nil {
		t.Fatal(err)
	}
	manual := create(true)
	abandoned := create(false)
	request := api.HostingCIRunRequest{Manifest: manifest(), PreviewTTLSeconds: 3600}
	run, err := store.CreateHostingCIRun(ctx, account.ID, site.ID, uuid.NewString(), request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReadyHostingRelease(ctx, account.ID, site.ID, run.ReleaseID); err != nil {
		t.Fatal(err)
	}
	protectedReq := request
	protectedReq.PreviewTTLSeconds = int64(api.HostingPreviewTTLMax / time.Second)
	protectedRun, err := store.CreateHostingCIRun(ctx, account.ID, site.ID, uuid.NewString(), protectedReq)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := store.GetHostingCleanupPlan(ctx, account.ID, site.ID, nil, now)
	if err != nil || plan.Policy.Enabled || plan.Policy.Revision != 0 || plan.ReclaimableReservedBytes != 2*(api.HostingStorageCopies*18+api.HostingManifestMaxBytes) {
		t.Fatal(plan, err)
	}
	if _, err = store.GetHostingCleanupPlan(ctx, uuid.NewString(), site.ID, nil, now); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign cleanup plan", err)
	}
	if ids, err := store.ApplyHostingRetention(ctx, account.ID, site.ID, site.OriginID, now); err != nil || len(ids) != 0 {
		t.Fatal("default policy performed work", ids, err)
	}
	req := api.SetHostingRetentionRequest{Enabled: true, HostingRetentionRules: hosting.DefaultRetentionRules()}
	var wg sync.WaitGroup
	var accepted atomic.Int64
	for range 10 {
		wg.Go(func() {
			_, err := store.SetHostingRetentionPolicy(ctx, account.ID, site.ID, req)
			if err == nil {
				accepted.Add(1)
			} else if !errors.Is(err, state.ErrConflict) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatal("policy CAS raced", accepted.Load())
	}
	if _, err = store.ApplyHostingRetention(ctx, account.ID, site.ID, "wrong-origin", now); !errors.Is(err, state.ErrConflict) {
		t.Fatal("origin mismatch", err)
	}
	before, err := store.GetHostingUsage(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	var retired atomic.Int64
	for range 10 {
		wg.Go(func() {
			ids, err := store.ApplyHostingRetention(ctx, account.ID, site.ID, site.OriginID, now)
			if err != nil {
				t.Error(err)
			}
			retired.Add(int64(len(ids)))
		})
	}
	wg.Wait()
	if retired.Load() != 2 {
		t.Fatal("duplicate/missing retirement", retired.Load())
	}
	for _, id := range []string{abandoned.ID, run.ReleaseID} {
		r, err := store.GetHostingRelease(ctx, account.ID, site.ID, id)
		if err != nil || r.State != "retiring" || r.RetirementGeneration == 0 {
			t.Fatal(r, err)
		}
	}
	for _, id := range []string{published.ID, manual.ID, protectedRun.ReleaseID} {
		r, err := store.GetHostingRelease(ctx, account.ID, site.ID, id)
		if err != nil || r.State == "retiring" {
			t.Fatal("protected release retired", r, err)
		}
	}
	after, err := store.GetHostingUsage(ctx, account.ID)
	if err != nil || before.ReservedBytes != after.ReservedBytes {
		t.Fatal("freed reservation before bytes removed", after, err)
	}
	retry, err := store.CreateHostingCIRun(ctx, account.ID, site.ID, run.ID, request)
	if err != nil || retry.ReleaseID != run.ReleaseID || retry.PreviewID != run.PreviewID {
		t.Fatal("retirement lost intent", retry, err)
	}
	req.ExpectedRevision = 1
	req.Enabled = false
	if _, err = store.SetHostingRetentionPolicy(ctx, account.ID, site.ID, req); err != nil {
		t.Fatal(err)
	}
	if ids, err := store.ApplyHostingRetention(ctx, account.ID, site.ID, site.OriginID, now); err != nil || len(ids) != 0 {
		t.Fatal("opt-out ignored", ids, err)
	}
}

func hostingRetentionRaces(t *testing.T, store hostingTestStore) {
	// adr: 202610171001 — promotion/preview creation and cleanup serialize on the same site lock.
	for _, operation := range []string{"promotion", "preview"} {
		t.Run(operation, func(t *testing.T) {
			for range 4 {
				account, site := seedDurable(t, store)
				ctx := t.Context()
				run, err := store.CreateHostingCIRun(ctx, account.ID, site.ID, uuid.NewString(), api.HostingCIRunRequest{Manifest: manifest(), PreviewTTLSeconds: 3600})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = store.ReadyHostingRelease(ctx, account.ID, site.ID, run.ReleaseID); err != nil {
					t.Fatal(err)
				}
				if _, err = store.SetHostingRetentionPolicy(ctx, account.ID, site.ID, api.SetHostingRetentionRequest{Enabled: true, HostingRetentionRules: hosting.DefaultRetentionRules()}); err != nil {
					t.Fatal(err)
				}
				var wg sync.WaitGroup
				start := make(chan struct{})
				var dependencyErr, cleanupErr error
				wg.Go(func() {
					<-start
					if operation == "promotion" {
						_, dependencyErr = store.PromoteHostingRelease(ctx, account.ID, site.ID, run.ReleaseID, site.Revision)
					} else {
						_, dependencyErr = store.CreateHostingCIPreview(ctx, account.ID, site.ID, run.ID)
					}
				})
				wg.Go(func() {
					<-start
					_, cleanupErr = store.ApplyHostingRetention(ctx, account.ID, site.ID, site.OriginID, time.Now().Add(26*time.Hour))
				})
				close(start)
				wg.Wait()
				if cleanupErr != nil || dependencyErr != nil && !errors.Is(dependencyErr, state.ErrConflict) {
					t.Fatal(dependencyErr, cleanupErr)
				}
				r, err := store.GetHostingRelease(ctx, account.ID, site.ID, run.ReleaseID)
				if err != nil {
					t.Fatal(err)
				}
				if dependencyErr == nil && r.State != "ready" || dependencyErr != nil && r.State != "retiring" {
					t.Fatal("dependency lost to stale cleanup", r, dependencyErr)
				}
			}
		})
	}
}
