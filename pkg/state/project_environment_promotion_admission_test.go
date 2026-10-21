package state

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestPromotionAdmissionMemory(t *testing.T) {
	testPromotionAdmission(t, func(*testing.T) Store { return NewMemStore() })
}

func admissionPolicy(f *promotionSnapshotFixture) *api.ProjectPromotionHealthPolicy {
	p := &api.ProjectPromotionHealthPolicy{}
	for _, a := range f.apps {
		p.Probes = append(p.Probes, api.ProjectReleaseProbe{AppID: a.ID, Slug: a.Slug, Method: "GET", Path: "/health"})
	}
	sort.Slice(p.Probes, func(i, j int) bool { return p.Probes[i].AppID < p.Probes[j].AppID })
	return p
}
func admissionActive(t *testing.T, store Store, account string, n int) []PromotionHealthJob {
	t.Helper()
	jobs := make([]PromotionHealthJob, 0, n)
	for i := range n {
		jobs = append(jobs, seedPromotionSchedule(t, store, account, 100+i, "preflight"))
	}
	return jobs
}
func freeAdmission(t *testing.T, store Store, jobs []PromotionHealthJob) {
	t.Helper()
	for _, j := range jobs {
		h, err := store.(ProjectPromotionHealthStore).ProjectEnvironmentPromotionHealth(t.Context(), j.AccountID, j.PromotionID)
		requirePromotionSnapshot(t, err)
		h.Phase = "failed"
		setPromotionHealthTestCheckpoint(t, store, j.PromotionID, h)
	}
}
func dispatchAdmission(t *testing.T, store Store, p ProjectEnvironmentPromotion, generation int64) *api.ProjectPromotionHealth {
	t.Helper()
	token := uuid.NewString()
	ok, err := store.(ProjectPromotionHealthStore).ClaimProjectEnvironmentPromotionHealth(t.Context(), p.AccountID, p.ID, token)
	requirePromotionSnapshot(t, err)
	if !ok {
		t.Fatal("queue claim denied")
	}
	defer func() {
		requirePromotionSnapshot(t, store.(ProjectPromotionHealthStore).ReleaseProjectEnvironmentPromotionHealth(t.Context(), p.ID, token))
	}()
	next, err := store.(ProjectPromotionAdmissionStore).DispatchProjectEnvironmentPromotion(t.Context(), p.AccountID, p.ID, token, generation)
	requirePromotionSnapshot(t, err)
	return next
}
func testPromotionAdmission(t *testing.T, factory func(*testing.T) Store) {
	for _, scenario := range []string{"low_load", "dispatch", "source_env", "destination_env", "source_graph", "destination_graph", "retention", "destination_secret", "config_version", "source_flags", "target_flags", "migration_abort", "migration_dispatch", "abort_changed_intent", "queue_full_approval", "fifo", "stale_owner", "expired"} {
		t.Run(scenario, func(t *testing.T) {
			store := factory(t)
			ctx := t.Context()
			if scenario == "expired" {
				testExpiredAdmission(t, store)
				return
			}
			f := newPromotionSnapshotFixture(t, store, false, false, strings.HasPrefix(scenario, "migration_"))
			f.promotion.HealthPolicy = admissionPolicy(f)
			if strings.HasPrefix(scenario, "migration_") {
				f.promotion.HealthPolicy.Migration = f.view.Migration
			}
			var jobs []PromotionHealthJob
			if scenario != "low_load" {
				jobs = admissionActive(t, store, f.account.ID, api.ProjectPromotionAdmissionAccountActive)
			}
			f.create(t)
			hs := store.(ProjectPromotionHealthStore)
			h, err := hs.ProjectEnvironmentPromotionHealth(ctx, f.account.ID, f.promotion.ID)
			requirePromotionSnapshot(t, err)
			if scenario == "low_load" {
				if h.Phase != "preflight" || h.Admission != nil {
					t.Fatal("low load unexpectedly queued", h)
				}
				return
			}
			if h.Phase != "queued" || h.Admission == nil || h.Admission.StartedAt != nil || h.Admission.Reason != "account_capacity" || h.Admission.WaitDeadline.Sub(h.Admission.QueuedAt) != api.ProjectPromotionAdmissionWaitTimeout {
				t.Fatalf("missing queue receipt: %+v", h)
			}
			// Queueing captures intent without creating candidates, tasks or holds.
			input := f.inputs[0]
			input.PromotionID = f.promotion.ID
			candidate := f.sources[0]
			candidate.ID = ""
			candidate.BuildID = ""
			candidate.Revision = 0
			candidate.Scope = "production"
			candidate.Status = DeployPending
			candidate.TrafficPercent = 0
			candidate.TrafficPercentExplicit = true
			if _, err = store.(ProjectEnvironmentPromotionWorkloadSpecStore).CreateDeploymentForEnvironmentPromotion(ctx, candidate, input); !errors.Is(err, ErrConflict) {
				t.Fatal("queued candidate was prepared", err)
			}
			next := dispatchAdmission(t, store, f.promotion, h.Generation)
			if next.Generation != h.Generation || next.Phase != "queued" || !next.Admission.WaitDeadline.Equal(h.Admission.WaitDeadline) {
				t.Fatal("waiting extended or advanced", next)
			}
			if scenario == "queue_full_approval" {
				testFullAdmissionApproval(t, f, h)
				return
			}
			if scenario == "fifo" {
				p := f.promotion
				p.ID = ""
				p.CreatedAt = time.Time{}
				p.IdempotencyKey = uuid.NewString()
				p.HealthPolicy = admissionPolicy(f)
				p.ReleaseConfigurationHash = f.view.Hash
				later, _, e := store.CreateProjectEnvironmentPromotion(ctx, p, f.workloads)
				requirePromotionSnapshot(t, e)
				freeAdmission(t, store, jobs)
				if n := dispatchAdmission(t, store, later, 1); n.Phase != "queued" {
					t.Fatal("later request jumped queue")
				}
				if n := dispatchAdmission(t, store, f.promotion, 1); n.Phase != "preflight" {
					t.Fatal("head could not start", n)
				}
				if n := dispatchAdmission(t, store, later, 1); n.Phase != "preflight" {
					t.Fatal("next could not start", n)
				}
				return
			}
			if scenario == "stale_owner" {
				freeAdmission(t, store, jobs)
				token := uuid.NewString()
				ok, e := hs.ClaimProjectEnvironmentPromotionHealth(ctx, f.account.ID, f.promotion.ID, token)
				requirePromotionSnapshot(t, e)
				if !ok {
					t.Fatal("claim")
				}
				admission := store.(ProjectPromotionAdmissionStore)
				if _, e = admission.DispatchProjectEnvironmentPromotion(ctx, f.account.ID, f.promotion.ID, uuid.NewString(), h.Generation); !errors.Is(e, ErrConflict) {
					t.Fatal("stale token dispatched", e)
				}
				if _, e = admission.DispatchProjectEnvironmentPromotion(ctx, f.account.ID, f.promotion.ID, token, h.Generation+1); !errors.Is(e, ErrConflict) {
					t.Fatal("stale generation dispatched", e)
				}
				requirePromotionSnapshot(t, hs.ReleaseProjectEnvironmentPromotionHealth(ctx, f.promotion.ID, token))
				return
			}
			switch scenario {
			case "destination_secret":
				changePromotionSecret(t, f)
			case "config_version":
				_, e := store.CreateProjectEnvironmentConfigVersion(ctx, ProjectEnvironmentConfig{AccountID: f.account.ID, ProjectID: f.project.ID, EnvironmentSlug: "production", Values: json.RawMessage(`{}`), ConfigHash: api.EmptyProjectEnvironmentConfigHash()})
				requirePromotionSnapshot(t, e)
			case "source_flags", "target_flags":
				slug := "staging"
				if scenario == "target_flags" {
					slug = "production"
				}
				env, e := store.ProjectEnvironmentBySlug(ctx, f.account.ID, f.project.ID, slug)
				requirePromotionSnapshot(t, e)
				flags := store.(FeatureFlagStore)
				scope := FeatureFlagScope{AccountID: f.account.ID, ProjectID: f.project.ID, EnvironmentID: env.ID}
				current, e := flags.GetFeatureFlags(ctx, scope, 0)
				requirePromotionSnapshot(t, e)
				_, e = flags.UpdateFeatureFlags(ctx, FeatureFlagUpdate{Scope: scope, ExpectedVersion: current.Version, Config: current.Config, Actor: "operator"})
				requirePromotionSnapshot(t, e)
			case "source_env", "destination_env":
				scope := "staging"
				if scenario == "destination_env" {
					scope = "production"
				}
				requirePromotionSnapshot(t, store.UpsertAppEnvInScope(ctx, f.account.ID, f.apps[0].ID, scope, "MODE", "changed while queued"))
			case "source_graph", "destination_graph", "abort_changed_intent":
				scope, members := "staging", f.sourceGraph.Members
				if scenario != "source_graph" {
					scope, members = "production", f.targetGraph.Members
				}
				_, e := store.(ProjectReleaseSetStore).PublishProjectReleaseSet(ctx, f.account.ID, f.project.ID, scope, 1800, members)
				requirePromotionSnapshot(t, e)
			case "retention":
				switch s := store.(type) {
				case *MemStore:
					s.mu.Lock()
					r := s.projectReleaseSets[f.sourceGraph.ID]
					r.TTLSeconds = 60
					s.projectReleaseSets[r.ID] = r
					s.mu.Unlock()
				case *PgStore:
					_, e := s.pool.Exec(ctx, "UPDATE project_release_sets SET ttl_seconds=60 WHERE id=$1", f.sourceGraph.ID)
					requirePromotionSnapshot(t, e)
				}
			}
			if scenario == "abort_changed_intent" || scenario == "migration_abort" {
				c, e := store.(ProjectPromotionControlStore).RequestProjectPromotionControl(ctx, f.account.ID, f.promotion.ID, "abort", api.ProjectPromotionControlRequest{RequestID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ExpectedGeneration: h.Generation})
				requirePromotionSnapshot(t, e)
				if c.Status != "aborted" {
					t.Fatal("queue cancellation did not finish", c)
				}
				return
			}
			freeAdmission(t, store, jobs)
			// A fresh PgStore represents a restarted process; persisted queue timing is authoritative.
			if pg, ok := store.(*PgStore); ok {
				store = NewPgStore(pg.pool.Pool)
			}
			before := time.Now().UTC()
			next = dispatchAdmission(t, store, f.promotion, h.Generation)
			if scenario == "migration_dispatch" {
				if next.Phase != "migrating" || next.Admission.StartedAt == nil || next.Deadline.Sub(next.StartedAt) != api.ProjectPromotionMigrationTimeout || next.Migration.TaskID != "" {
					t.Fatalf("queued migration started incorrectly: %+v", next)
				}
			} else if scenario == "dispatch" {
				if next.Phase != "preflight" || next.Admission.StartedAt == nil || next.StartedAt.Before(before) || next.Deadline.Sub(next.StartedAt) != api.ProjectReleaseVerificationTimeout || !next.Admission.WaitDeadline.Equal(h.Admission.WaitDeadline) {
					t.Fatalf("execution did not get a fresh window: %+v", next)
				}
				f.prepare(t)
			} else if next.Phase != "conflicted" || next.Admission.Reason != "intent_changed" || next.Admission.StartedAt != nil {
				t.Fatalf("changed intent started: %+v", next)
			}
		})
	}
}
func testFullAdmissionApproval(t *testing.T, f *promotionSnapshotFixture, h *api.ProjectPromotionHealth) {
	t.Helper()
	ctx := t.Context()
	p := f.promotion
	p.ID = ""
	p.HealthPolicy = admissionPolicy(f)
	p.ReleaseConfigurationHash = f.view.Hash
	for range api.ProjectPromotionAdmissionAccountQueued - 1 {
		p.IdempotencyKey = uuid.NewString()
		_, _, err := f.store.CreateProjectEnvironmentPromotion(ctx, p, f.workloads)
		requirePromotionSnapshot(t, err)
	}
	a, err := f.store.CreateProjectEnvironmentApproval(ctx, ProjectEnvironmentApproval{AccountID: f.account.ID, ProjectSlug: f.project.Slug, EnvironmentSlug: "production", PlanTokenHash: strings.Repeat("a", 64), ApprovalTokenHash: strings.Repeat("b", 64), ExpiresAt: time.Now().Add(time.Hour)})
	requirePromotionSnapshot(t, err)
	p.IdempotencyKey = uuid.NewString()
	p.Approval = &PromotionApprovalIntent{PlanTokenHash: a.PlanTokenHash, ApprovalTokenHash: a.ApprovalTokenHash}
	_, _, err = f.store.CreateProjectEnvironmentPromotion(ctx, p, f.workloads)
	var full *PromotionAdmissionFullError
	if !errors.As(err, &full) || full.Scope != "account" || full.Observed != api.ProjectPromotionAdmissionAccountQueued {
		t.Fatal("full queue not rejected", err)
	}
	if _, err = f.store.ProjectEnvironmentApprovalByToken(ctx, f.account.ID, f.project.Slug, "production", a.PlanTokenHash, a.ApprovalTokenHash); err != nil {
		t.Fatal("rejected admission consumed approval", err)
	}
	if _, _, err = f.store.ProjectEnvironmentPromotionByIdempotencyKey(ctx, f.account.ID, f.project.Slug, p.IdempotencyKey); !errors.Is(err, ErrNotFound) {
		t.Fatal("rejected operation persisted", err)
	}
	_, err = f.store.(ProjectPromotionControlStore).RequestProjectPromotionControl(ctx, f.account.ID, f.promotion.ID, "abort", api.ProjectPromotionControlRequest{RequestID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", ExpectedGeneration: h.Generation})
	requirePromotionSnapshot(t, err)
	accepted, _, err := f.store.CreateProjectEnvironmentPromotion(ctx, p, f.workloads)
	requirePromotionSnapshot(t, err)
	if accepted.ConsumedApproval == nil {
		t.Fatal("accepted approval lacks receipt")
	}
	if _, err = f.store.ProjectEnvironmentApprovalByToken(ctx, f.account.ID, f.project.Slug, "production", a.PlanTokenHash, a.ApprovalTokenHash); !errors.Is(err, ErrNotFound) {
		t.Fatal("accepted approval not consumed", err)
	}
	if _, _, err = f.store.CreateProjectEnvironmentPromotion(ctx, p, f.workloads); !errors.Is(err, ErrConflict) {
		t.Fatal("idempotency duplicate accepted", err)
	}
}
func testExpiredAdmission(t *testing.T, store Store) {
	t.Helper()
	job := seedPromotionSchedule(t, store, "", 1, "queued_expired")
	h, err := store.(ProjectPromotionHealthStore).ProjectEnvironmentPromotionHealth(t.Context(), job.AccountID, job.PromotionID)
	requirePromotionSnapshot(t, err)
	p := ProjectEnvironmentPromotion{ID: job.PromotionID, AccountID: job.AccountID}
	next := dispatchAdmission(t, store, p, h.Generation)
	p, _, err = store.ProjectEnvironmentPromotionByID(t.Context(), job.AccountID, job.ProjectSlug, job.ToEnvironment, job.PromotionID)
	requirePromotionSnapshot(t, err)
	if p.VerificationStartedAt != nil {
		t.Fatal("expired wait claimed verification started")
	}
	if next.Phase != "failed" || next.Admission.Reason != "queue_timeout" || next.Admission.StartedAt != nil {
		t.Fatal("expired queue started", next)
	}
}

func testConcurrentAdmission(t *testing.T, store Store) {
	t.Helper()
	// Independent accounts/projects contend for the final fleet operation slot.
	jobs := admissionActive(t, store, "", api.ProjectPromotionAdmissionFleetActive-1)
	_ = jobs
	fixtures := make([]*promotionSnapshotFixture, 2)
	for i := range fixtures {
		fixtures[i] = newPromotionSnapshotFixtureNamed(t, store, false, "admission-"+uuid.NewString()[:12])
		fixtures[i].promotion.HealthPolicy = admissionPolicy(fixtures[i])
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, f := range fixtures {
		wg.Go(func() {
			s := f.store
			if pg, ok := s.(*PgStore); ok {
				s = NewPgStore(pg.pool.Pool)
			}
			p, _, err := s.CreateProjectEnvironmentPromotion(t.Context(), f.promotion, f.workloads)
			f.promotion = p
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		requirePromotionSnapshot(t, err)
	}
	active, queued := 0, 0
	for _, f := range fixtures {
		h, err := store.(ProjectPromotionHealthStore).ProjectEnvironmentPromotionHealth(t.Context(), f.account.ID, f.promotion.ID)
		requirePromotionSnapshot(t, err)
		switch h.Phase {
		case "preflight":
			active++
		case "queued":
			queued++
		}
	}
	if active != 1 || queued != 1 {
		t.Fatalf("fleet capacity oversubscribed: active=%d queued=%d", active, queued)
	}
}
func TestPromotionAdmissionConcurrentMemory(t *testing.T) { testConcurrentAdmission(t, NewMemStore()) }

func testFleetQueueAdmission(t *testing.T, store Store) {
	t.Helper()
	f := newPromotionSnapshotFixture(t, store, false)
	f.promotion.HealthPolicy = admissionPolicy(f)
	admissionActive(t, store, "", api.ProjectPromotionAdmissionFleetActive)
	for i := range api.ProjectPromotionAdmissionFleetQueued {
		seedPromotionSchedule(t, store, "", 200+i, "queued")
	}
	_, _, err := store.CreateProjectEnvironmentPromotion(t.Context(), f.promotion, f.workloads)
	var full *PromotionAdmissionFullError
	if !errors.As(err, &full) || full.Scope != "fleet" || full.Limit != api.ProjectPromotionAdmissionFleetQueued || full.Observed != api.ProjectPromotionAdmissionFleetQueued {
		t.Fatal("fleet waiting bound bypassed", err)
	}
}
func TestPromotionAdmissionFleetQueueMemory(t *testing.T) { testFleetQueueAdmission(t, NewMemStore()) }

func testConcurrentAdmissionDispatch(t *testing.T, store Store) {
	t.Helper()
	ctx := t.Context()
	active := admissionActive(t, store, "", api.ProjectPromotionAdmissionFleetActive)
	fixtures := make([]*promotionSnapshotFixture, 2)
	tokens := make([]string, 2)
	for i := range fixtures {
		f := newPromotionSnapshotFixtureNamed(t, store, false, "dispatch-"+uuid.NewString()[:12])
		f.promotion.HealthPolicy = admissionPolicy(f)
		f.create(t)
		fixtures[i] = f
		tokens[i] = uuid.NewString()
		ok, err := store.(ProjectPromotionHealthStore).ClaimProjectEnvironmentPromotionHealth(ctx, f.account.ID, f.promotion.ID, tokens[i])
		requirePromotionSnapshot(t, err)
		if !ok {
			t.Fatal("queue lease denied")
		}
	}
	freeAdmission(t, store, active[:1])
	var wg sync.WaitGroup
	results := make(chan *api.ProjectPromotionHealth, 2)
	errs := make(chan error, 2)
	for i, f := range fixtures {
		wg.Go(func() {
			s := store
			if pg, ok := s.(*PgStore); ok {
				s = NewPgStore(pg.pool.Pool)
			}
			h, err := s.(ProjectPromotionAdmissionStore).DispatchProjectEnvironmentPromotion(ctx, f.account.ID, f.promotion.ID, tokens[i], 1)
			results <- h
			errs <- err
		})
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		requirePromotionSnapshot(t, err)
	}
	started, waiting := 0, 0
	for h := range results {
		switch h.Phase {
		case "preflight":
			started++
		case "queued":
			waiting++
		}
	}
	if started != 1 || waiting != 1 {
		t.Fatalf("competing dispatch exceeded fleet limit: started=%d queued=%d", started, waiting)
	}
}
func TestPromotionAdmissionDispatchConcurrentMemory(t *testing.T) {
	testConcurrentAdmissionDispatch(t, NewMemStore())
}
