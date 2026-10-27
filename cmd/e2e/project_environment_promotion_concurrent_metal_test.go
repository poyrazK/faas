//go:build promotionacceptance && metal

package e2e_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

const (
	promotionNativeLoadAccounts = 3
	promotionNativeLoadTargets  = 7
)

type promotionNativeLoadJob struct {
	p                                   *environmentPromotionNative
	environment, id, baseline, selected string
	deadline                            time.Time
	windows                             []time.Time
	checked                             bool
}

type promotionNativeLeaseSample struct {
	peak, accountPeak int
	err               error
}

// Three isolated Scale accounts/projects each own seven destination environments.
// Direct OCI baselines and one paired staging promotion per project avoid 42
// extra source builds without fabricating deployments, graphs or health proofs.
func TestProjectEnvironmentPromotionConcurrentMetal(t *testing.T) {
	var fixtures []*environmentPromotionNative
	first := newEnvironmentPromotionNativeConfigured(t, true, nil)
	fixtures = append(fixtures, first)
	for range promotionNativeLoadAccounts - 1 {
		fixtures = append(fixtures, newEnvironmentPromotionNativeConfigured(t, true, first.h))
	}
	// Subsequent fixture setup may restart APID. Install the network fault once.
	fault := newPromotionNativeProbeFault(t, first)
	var jobs []*promotionNativeLoadJob
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(registry.Close)
	image, _ := e2etest.HelloImage("promotion-load-baseline", "native baseline")
	imageRef := registry.AddImage("promotion-load-baseline", image)
	for _, p := range fixtures {
		// Give staging an actual paired snapshot through a normal APID promotion.
		// Its target graph already exists from the source build. Creating a new
		// environment via --sync-config would correctly be refused without a graph.
		snapshotPreview, err := p.owner.GetProjectEnvironmentPromotionPreviewWithMigration(t.Context(), p.project.Slug, "staging", "production", true, false, false, false)
		mustProjectWorkerNative(t, err)
		if !snapshotPreview.CanPromote {
			t.Fatalf("load staging snapshot refused: %v", snapshotPreview.BlockingReasons)
		}
		snapshot, err := p.owner.PromoteProjectEnvironmentWithIdempotencyKey(t.Context(), p.project.Slug, "staging", api.PromoteProjectEnvironmentRequest{FromEnvironment: "production", PromotionToken: snapshotPreview.PromotionToken}, uuid.NewString())
		mustProjectWorkerNative(t, err)
		if snapshot.Status != "succeeded" {
			t.Fatal("load staging snapshot did not complete")
		}
		for i := range promotionNativeLoadTargets {
			environment := fmt.Sprintf("target-%d", i)
			_, err := p.owner.CreateProjectEnvironment(t.Context(), p.project.Slug, api.CreateProjectEnvironmentRequest{Slug: environment})
			mustProjectWorkerNative(t, err)
			_, err = p.store.UpdateProjectEnvironmentProtection(t.Context(), p.accountID, p.project.ID, environment, false)
			mustProjectWorkerNative(t, err)
			deployed, err := p.owner.Deploy(t.Context(), p.apps["frontend"].Slug, api.CreateDeploymentRequest{Image: imageRef, Environment: environment})
			mustProjectWorkerNative(t, err)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
			_, err = e2etest.WaitForDeploymentLive(ctx, t, p.h.Pool, deployed.ID, 2*time.Minute)
			cancel()
			mustProjectWorkerNative(t, err)
			_, err = p.owner.PublishProjectReleaseSet(t.Context(), p.project.Slug, environment, api.PublishProjectReleaseSetRequest{TTLSeconds: 3600, Deployments: map[string]string{"frontend": deployed.ID}})
			mustProjectWorkerNative(t, err)
			// Refresh bounded graph retention before the simultaneous admissions.
			graph, err := p.owner.GetActiveProjectReleaseSet(t.Context(), p.project.Slug, environment)
			mustProjectWorkerNative(t, err)
			members := map[string]string{}
			for _, m := range graph.Members {
				members["frontend"] = m.DeploymentID
			}
			graph, err = p.owner.PublishProjectReleaseSet(t.Context(), p.project.Slug, environment, api.PublishProjectReleaseSetRequest{TTLSeconds: 3600, Deployments: members})
			mustProjectWorkerNative(t, err)
			jobs = append(jobs, &promotionNativeLoadJob{p: p, environment: environment, baseline: graph.ID})
		}
		var count int
		mustProjectWorkerNative(t, p.h.Pool.QueryRow(t.Context(), "SELECT count(*) FROM deployments WHERE app_id=$1", p.apps["frontend"].ID).Scan(&count))
		p.counts[p.apps["frontend"].ID] = count
		var builds int
		mustProjectWorkerNative(t, p.h.Pool.QueryRow(t.Context(), "SELECT count(*) FROM builds b JOIN deployments d ON d.id=b.deployment_id WHERE d.app_id=$1", p.apps["frontend"].ID).Scan(&builds))
		p.buildCounts[p.apps["frontend"].ID] = builds
	}
	fault.delayed.Store(true)
	// Block until the complete inventory exists; these are ordinary accepted jobs,
	// not synthetic health rows. The outage cannot produce attributed success.
	fault.blocked.Store(true)
	for _, job := range jobs {
		preview, err := job.p.owner.GetProjectEnvironmentPromotionPreviewWithMigration(t.Context(), job.p.project.Slug, job.environment, "staging", true, true, false, false)
		mustProjectWorkerNative(t, err)
		if !preview.CanPromote {
			t.Fatalf("load admission refused: %v", preview.BlockingReasons)
		}
		accepted, err := job.p.owner.PromoteProjectEnvironmentWithIdempotencyKey(t.Context(), job.p.project.Slug, job.environment, api.PromoteProjectEnvironmentRequest{FromEnvironment: "staging", PromotionToken: preview.PromotionToken}, uuid.NewString())
		mustProjectWorkerNative(t, err)
		job.id = accepted.PromotionID
		s := loadPromotionStatus(t, job)
		job.deadline = s.Health.Deadline
	}
	observerCtx, cancelObserver := context.WithCancel(t.Context())
	observed := make(chan promotionNativeLeaseSample, 1)
	go observePromotionNativeLeases(observerCtx, first, observed)
	// Always join the observer before fixture cleanup, including assertion failures.
	var observedOnce sync.Once
	var sample promotionNativeLeaseSample
	stopObserver := func() { observedOnce.Do(func() { cancelObserver(); sample = <-observed }) }
	t.Cleanup(stopObserver)
	fault.blocked.Store(false)
	select {
	case <-fault.started:
	case <-time.After(time.Minute):
		t.Fatal("slow gateway probe was not exercised")
	}
	// More than 20 accepted jobs must all coexist before any terminal result.
	for _, job := range jobs {
		if s := loadPromotionStatus(t, job); s.Status != "running" {
			t.Fatal("load inventory completed before fairness observation")
		}
	}
	waitProjectWorkerNative(t, 10*time.Second, "unrelated progress during delayed gateway probe", func() bool {
		if fault.active.Load() == 0 {
			return false
		}
		for _, job := range jobs[promotionNativeLoadTargets:] {
			if loadPromotionStatus(t, job).Health.CheckedAt != nil {
				return true
			}
		}
		return false
	})
	var leasedID, oldToken string
	var oldUntil time.Time
	mustProjectWorkerNative(t, first.h.Pool.QueryRow(t.Context(), "SELECT h.promotion_id::text,h.lease_token::text,h.lease_until FROM project_environment_promotion_health h JOIN project_environment_promotions p ON p.id=h.promotion_id WHERE p.account_id=$1 AND h.lease_token IS NOT NULL AND h.lease_until>clock_timestamp() ORDER BY h.lease_until DESC LIMIT 1", first.accountID).Scan(&leasedID, &oldToken, &oldUntil))
	var oldGeneration int64
	mustProjectWorkerNative(t, first.h.KillAPID())
	first.apidStopped = true
	var interruptedToken string
	mustProjectWorkerNative(t, first.h.Pool.QueryRow(t.Context(), "SELECT generation,COALESCE(lease_token::text,'') FROM project_environment_promotion_health WHERE promotion_id=$1", leasedID).Scan(&oldGeneration, &interruptedToken))
	if interruptedToken != oldToken {
		t.Fatal("owner restart did not interrupt a claimed native job")
	}
	fault.delayed.Store(false)
	mustProjectWorkerNative(t, first.h.RestartAPID())
	first.apidStopped = false
	// A dead owner cannot advance its unfinished health observation. The restarted
	// dispatcher must wait for the durable lease rather than steal it early.
	var reclaimAt time.Time
	waitProjectWorkerNative(t, 2*time.Minute, "durable native lease reclaim", func() bool {
		var generation int64
		var token string
		var lastClaimed time.Time
		mustProjectWorkerNative(t, first.h.Pool.QueryRow(t.Context(), "SELECT generation,COALESCE(lease_token::text,''),last_claimed_at FROM project_environment_promotion_health WHERE promotion_id=$1", leasedID).Scan(&generation, &token, &lastClaimed))
		if time.Now().Before(oldUntil) && generation != oldGeneration {
			t.Fatal("late observation advanced a retired owner's lease")
		}
		if token == oldToken {
			return false
		}
		if !lastClaimed.Before(oldUntil) {
			reclaimAt = lastClaimed
			return true
		}
		return false
	})
	// Observe late inventory progress while early jobs are still active.
	waitProjectWorkerNative(t, 3*time.Minute, "all native promotions receive a fair turn", func() bool {
		all := true
		for _, job := range jobs {
			s := loadPromotionStatus(t, job)
			if s.Health.CheckedAt != nil {
				job.checked = true
			}
			all = all && job.checked
			if s.Health.Phase == "preflight" || s.Health.Phase == "cutover" {
				if !s.Health.Deadline.Equal(job.deadline) {
					t.Fatal("load/restart reset health deadline")
				}
			}
			if s.Status != "running" {
				t.Fatal("fairness depended on earlier jobs terminating")
			}
		}
		return all
	})
	// Urgent operator restoration competes with ordinary due health work.
	urgent := jobs[promotionNativeLoadTargets]
	waitProjectWorkerNative(t, 4*time.Minute, "load cutover before abort", func() bool { return loadPromotionStatus(t, urgent).Health.Phase == "verifying" })
	receipt := urgent.p.controlPromotion(t, urgent.id, "abort", urgent.environment)
	controlAt := receipt.RequestedAt
	ordinaryProgress := false
	waitProjectWorkerNative(t, api.ProjectReleaseVerificationTimeout+time.Minute, "native load completion and restoration", func() bool {
		complete := true
		for _, job := range jobs {
			s := loadPromotionStatus(t, job)
			if s.Health.CheckedAt != nil && s.Health.Verdict == "healthy" && (len(job.windows) == 0 || s.Health.CheckedAt.After(job.windows[len(job.windows)-1])) {
				job.windows = append(job.windows, *s.Health.CheckedAt)
			}
			if job == urgent {
				if s.Health.Control == nil || !s.Health.Control.Deadline.Equal(receipt.Deadline) {
					t.Fatal("load changed control deadline")
				}
				if s.Health.Control.Status != "restored" {
					complete = false
				} else {
					job.selected = s.ReleaseGraph.RestoredTargetReleaseSetID
				}
			} else {
				if s.Health.CheckedAt != nil && s.Health.CheckedAt.After(controlAt) {
					ordinaryProgress = true
				}
				if s.Health.Phase == "failed" || s.Health.Phase == "rollback_failed" || s.Health.Phase == "conflicted" {
					t.Fatal("ordinary load promotion failed")
				}
				if s.Health.Phase != "succeeded" {
					complete = false
				} else {
					job.selected = s.ReleaseGraph.TargetReleaseSetID
				}
			}
		}
		return complete
	})
	if !ordinaryProgress {
		t.Fatal("urgent work displaced all ordinary progress")
	}
	stopObserver()
	mustProjectWorkerNative(t, sample.err)
	if sample.peak < 2 || sample.peak > api.ProjectPromotionHealthConcurrency || sample.accountPeak > api.ProjectPromotionHealthAccountConcurrency {
		t.Fatal("native fleet/account lease bounds were not exercised")
	}
	// A second restart may neither repeat publication nor allocate new candidates.
	mustProjectWorkerNative(t, first.h.RestartAPID())
	var evidence []map[string]any
	for _, job := range jobs {
		s := loadPromotionStatus(t, job)
		graph, err := job.p.owner.GetActiveProjectReleaseSet(t.Context(), job.p.project.Slug, job.environment)
		mustProjectWorkerNative(t, err)
		if job.selected == "" || graph.ID != job.selected {
			t.Fatal("restart duplicated or lost native publication")
		}
		for _, workload := range s.Workloads {
			source, err := job.p.store.DeploymentByID(t.Context(), workload.SourceDeploymentID)
			mustProjectWorkerNative(t, err)
			target, err := job.p.store.DeploymentByID(t.Context(), workload.TargetDeploymentID)
			mustProjectWorkerNative(t, err)
			if target.Scope != job.environment || target.RootfsKey == "" || target.RootfsKey != source.RootfsKey || target.SourceSHA256 != source.SourceSHA256 || target.ImageDigest != source.ImageDigest {
				t.Fatal("load promotion changed a tested artifact")
			}
		}
		evidence = append(evidence, map[string]any{"promotion_id": job.id, "account_id": job.p.accountID, "project_id": job.p.project.ID, "environment": job.environment, "healthy_windows": job.windows, "phase": s.Health.Phase, "selected_graph": graph.ID})
	}
	for _, p := range fixtures {
		var count int
		mustProjectWorkerNative(t, p.h.Pool.QueryRow(t.Context(), "SELECT count(*) FROM deployments WHERE app_id=$1", p.apps["frontend"].ID).Scan(&count))
		if count != p.counts[p.apps["frontend"].ID]+promotionNativeLoadTargets {
			t.Fatal("load/restart allocated duplicate promotion artifacts")
		}
		var builds int
		mustProjectWorkerNative(t, p.h.Pool.QueryRow(t.Context(), "SELECT count(*) FROM builds b JOIN deployments d ON d.id=b.deployment_id WHERE d.app_id=$1", p.apps["frontend"].ID).Scan(&builds))
		if builds != p.buildCounts[p.apps["frontend"].ID] {
			t.Fatal("load promotion submitted new source builds")
		}
	}
	for _, p := range fixtures[1:] {
		for _, app := range p.apps {
			mustProjectWorkerNative(t, p.owner.DeleteApp(t.Context(), app.Slug))
		}
		p.cleanupDone = true
	}
	first.cleanup(t)
	if t.Failed() {
		return
	}
	logPromotionNativeEvidence(t, map[string]any{"schema_version": 1, "scenario": "concurrent", "jobs": evidence, "fleet_peak": sample.peak, "account_peak": sample.accountPeak, "fleet_limit": api.ProjectPromotionHealthConcurrency, "account_limit": api.ProjectPromotionHealthAccountConcurrency, "slow_probe": true, "unrelated_progress_during_delay": true, "all_progressed_while_active": true, "owner_restarted": true, "lease_reclaimed": true, "reclaim_at": reclaimAt, "previous_lease_until": oldUntil, "late_observation_fenced": true, "ordinary_progress_after_control": ordinaryProgress, "control_restored": true, "control_requested_at": controlAt, "publication_unique": true, "artifact_reused": true, "cleanup": true})
}

func loadPromotionStatus(t *testing.T, job *promotionNativeLoadJob) api.ProjectEnvironmentPromotionStatusResponse {
	t.Helper()
	s, err := job.p.owner.GetProjectEnvironmentPromotionStatus(t.Context(), job.p.project.Slug, job.environment, job.id)
	mustProjectWorkerNative(t, err)
	if s.Health == nil {
		t.Fatal("load job lost health checkpoint")
	}
	if s.Health.Phase != "preflight" && s.Health.Phase != "cutover" && s.Health.CheckedAt != nil && s.Health.Verdict == "healthy" && (len(job.windows) == 0 || s.Health.CheckedAt.After(job.windows[len(job.windows)-1])) {
		job.windows = append(job.windows, *s.Health.CheckedAt)
	}
	return s
}

func observePromotionNativeLeases(ctx context.Context, p *environmentPromotionNative, out chan<- promotionNativeLeaseSample) {
	sample := promotionNativeLeaseSample{}
	defer func() { out <- sample }()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var fleet, account int
		err := p.h.Pool.QueryRow(ctx, `SELECT COALESCE(sum(n),0)::int,COALESCE(max(n),0)::int FROM (SELECT count(*) n FROM project_environment_promotion_health h JOIN project_environment_promotions p ON p.id=h.promotion_id WHERE h.lease_token IS NOT NULL AND h.lease_until>clock_timestamp() GROUP BY p.account_id) leases`).Scan(&fleet, &account)
		if err != nil {
			if ctx.Err() == nil {
				sample.err = err
			}
			return
		}
		sample.peak = max(sample.peak, fleet)
		sample.accountPeak = max(sample.accountPeak, account)
		if fleet > api.ProjectPromotionHealthConcurrency || account > api.ProjectPromotionHealthAccountConcurrency {
			sample.err = fmt.Errorf("native promotion concurrency exceeded: fleet=%d account=%d", fleet, account)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
