package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func seedPromotionSchedule(t *testing.T, store Store, account string, index int, phase string) PromotionHealthJob {
	t.Helper()
	ctx := t.Context()
	if account == "" {
		a, err := store.CreateAccount(ctx, fmt.Sprintf("schedule-%s@example.test", uuid.NewString()), api.PlanScale)
		requirePromotionSnapshot(t, err)
		account = a.ID
	}
	project, err := store.CreateProject(ctx, Project{AccountID: account, Slug: fmt.Sprintf("schedule-%d", index)})
	requirePromotionSnapshot(t, err)
	p, _, err := store.CreateProjectEnvironmentPromotion(ctx, ProjectEnvironmentPromotion{AccountID: account, ProjectID: project.ID, ProjectSlug: project.Slug, FromEnvironment: "staging", ToEnvironment: "production", PromotionHash: strings.Repeat("a", 64), IdempotencyKey: uuid.NewString(), Status: "running", VerificationStatus: "pending"}, nil)
	requirePromotionSnapshot(t, err)
	now := time.Now().UTC()
	h := &api.ProjectPromotionHealth{Generation: 1, Phase: phase, StartedAt: now, Deadline: now.Add(api.ProjectReleaseVerificationTimeout), Policy: api.ProjectPromotionHealthPolicy{Probes: []api.ProjectReleaseProbe{{AppID: uuid.NewString(), Slug: "api", Method: "GET", Path: "/health"}}}, Verdict: "unknown"}
	if phase == "queued_expired" || phase == "queued" {
		h.Phase = "queued"
		h.StartedAt = now.Add(-time.Hour)
		h.Deadline = now.Add(-time.Minute)
		if phase == "queued" {
			h.StartedAt = now
			h.Deadline = now.Add(api.ProjectPromotionAdmissionWaitTimeout)
		}
		h.Admission = &api.ProjectPromotionAdmission{QueuedAt: h.StartedAt, WaitDeadline: h.Deadline, Reason: "fleet_capacity"}
	}

	switch s := store.(type) {
	case *MemStore:
		s.mu.Lock()
		if s.promotionHealth == nil {
			s.promotionHealth = map[string]*api.ProjectPromotionHealth{}
		}
		s.promotionHealth[p.ID] = h
		s.mu.Unlock()
	case *PgStore:
		raw, e := encodePromotionHealth(h)
		requirePromotionSnapshot(t, e)
		requirePromotionSnapshot(t, sqlc.New().InsertPromotionHealth(ctx, s.pool, sqlc.InsertPromotionHealthParams{PromotionID: p.ID, Generation: h.Generation, Phase: h.Phase, Payload: raw}))
	}
	return PromotionHealthJob{p.ID, account, project.Slug, "production"}
}

func TestPromotionScheduleMemory(t *testing.T) {
	testPromotionSchedule(t, func(*testing.T) Store { return NewMemStore() })
}

func testPromotionSchedule(t *testing.T, factory func(*testing.T) Store) {
	for _, scenario := range []string{"batch_fairness", "priority", "account_limit", "fleet_limit", "restart", "stale_finish", "due_sample", "observation_fence", "wake", "recovery", "skip_locked"} {
		t.Run(scenario, func(t *testing.T) {
			store := factory(t)
			scheduler := store.(ProjectPromotionHealthScheduler)
			health := store.(ProjectPromotionHealthStore)
			ctx := t.Context()
			count := 8
			if scenario == "batch_fairness" {
				count = 3*api.ProjectPromotionHealthBatchSize + 1
			}
			jobs := make([]PromotionHealthJob, 0, count)
			for i := range count {
				account := ""
				if scenario == "account_limit" && i > 0 && i < 4 {
					account = jobs[0].AccountID
				}
				phase := "preflight"
				if scenario == "priority" && i == count-1 {
					phase = "rolling_back"
				}
				jobs = append(jobs, seedPromotionSchedule(t, store, account, i, phase))
			}
			claim := func(prefer bool) (*PromotionHealthJob, string) {
				t.Helper()
				token := uuid.NewString()
				job, e := scheduler.ClaimNextProjectEnvironmentPromotionHealth(ctx, token, prefer)
				requirePromotionSnapshot(t, e)
				return job, token
			}
			if scenario == "batch_fairness" {
				seen := map[string]bool{}
				for range count {
					job, token := claim(false)
					if job == nil || seen[job.PromotionID] {
						t.Fatalf("later work starved after %d jobs", len(seen))
					}
					seen[job.PromotionID] = true
					requirePromotionSnapshot(t, health.ReleaseProjectEnvironmentPromotionHealth(ctx, job.PromotionID, token))
				}
				for _, job := range jobs {
					p, _, e := store.ProjectEnvironmentPromotionByID(ctx, job.AccountID, job.ProjectSlug, job.ToEnvironment, job.PromotionID)
					requirePromotionSnapshot(t, e)
					if p.Status != "running" {
						t.Fatal("fairness depended on jobs completing")
					}
				}
				return
			}
			if scenario == "priority" {
				ordinary, _ := claim(false)
				if ordinary == nil || ordinary.PromotionID != jobs[0].PromotionID {
					t.Fatal("fair lane displaced oldest ordinary work", ordinary)
				}
				job, token := claim(true)
				if job == nil || job.PromotionID != jobs[count-1].PromotionID {
					t.Fatal("recovery lane did not prefer rollback", job)
				}
				requirePromotionSnapshot(t, health.ReleaseProjectEnvironmentPromotionHealth(ctx, job.PromotionID, token))
				return
			}
			if scenario == "fleet_limit" || scenario == "account_limit" {
				schedulers := []ProjectPromotionHealthScheduler{scheduler}
				if pg, ok := store.(*PgStore); ok {
					schedulers = append(schedulers, NewPgStore(pg.pool.Pool))
				}
				var wg sync.WaitGroup
				var mu sync.Mutex
				var claimed []PromotionHealthJob
				var errors []error
				for index := range 3 * api.ProjectPromotionHealthConcurrency {
					wg.Go(func() {
						job, err := schedulers[index%len(schedulers)].ClaimNextProjectEnvironmentPromotionHealth(ctx, uuid.NewString(), false)
						mu.Lock()
						defer mu.Unlock()
						if err != nil {
							errors = append(errors, err)
						}
						if job != nil {
							claimed = append(claimed, *job)
						}
					})
				}
				wg.Wait()
				if len(errors) != 0 || len(claimed) != api.ProjectPromotionHealthConcurrency {
					t.Fatal("fleet bound", len(claimed), errors)
				}
				counts := map[string]int{}
				seen := map[string]bool{}
				for _, job := range claimed {
					counts[job.AccountID]++
					if seen[job.PromotionID] {
						t.Fatal("duplicate claim")
					}
					seen[job.PromotionID] = true
				}
				for _, n := range counts {
					if n > api.ProjectPromotionHealthAccountConcurrency {
						t.Fatal("account consumed all slots")
					}
				}
				if scenario == "account_limit" && counts[jobs[0].AccountID] != api.ProjectPromotionHealthAccountConcurrency {
					t.Fatal("account quota not exercised", counts)
				}
				return
			}
			if scenario == "skip_locked" {
				s, ok := store.(*PgStore)
				if !ok {
					return
				}
				tx, e := s.pool.Begin(ctx)
				requirePromotionSnapshot(t, e)
				defer func() { _ = tx.Rollback(ctx) }()
				_, e = tx.Exec(ctx, "SELECT 1 FROM project_environment_promotion_health WHERE promotion_id=$1 FOR UPDATE", jobs[0].PromotionID)
				requirePromotionSnapshot(t, e)
				claimCtx, cancel := context.WithTimeout(ctx, time.Second)
				defer cancel()
				job, e := scheduler.ClaimNextProjectEnvironmentPromotionHealth(claimCtx, uuid.NewString(), false)
				requirePromotionSnapshot(t, e)
				if job == nil || job.PromotionID == jobs[0].PromotionID {
					t.Fatal("locked job blocked unrelated work")
				}
				return
			}
			job, token := claim(false)
			if job == nil {
				t.Fatal("no claim")
			}
			if s, ok := store.(*PgStore); ok && scenario == "restart" {
				store = NewPgStore(s.pool.Pool)
				scheduler = store.(ProjectPromotionHealthScheduler)
				health = store.(ProjectPromotionHealthStore)
			}
			current, e := scheduler.ProjectEnvironmentPromotionHealthLeaseCurrent(ctx, job.AccountID, job.PromotionID, token)
			requirePromotionSnapshot(t, e)
			if !current {
				t.Fatal("restart lost lease")
			}
			h, e := health.ProjectEnvironmentPromotionHealth(ctx, job.AccountID, job.PromotionID)
			requirePromotionSnapshot(t, e)
			if scenario == "due_sample" {
				now := time.Now().UTC()
				h.CheckedAt = &now
				setPromotionHealthTestCheckpoint(t, store, job.PromotionID, h)
			}
			if scenario == "observation_fence" {
				_, e = health.AdvanceProjectEnvironmentPromotionHealth(ctx, job.AccountID, job.PromotionID, PromotionHealthObservation{Generation: h.Generation, LeaseToken: uuid.NewString()})
				if !errors.Is(e, ErrConflict) {
					t.Fatal("foreign observation accepted", e)
				}
				return
			}
			if scenario == "stale_finish" || scenario == "restart" {
				switch s := store.(type) {
				case *MemStore:
					s.mu.Lock()
					lease := s.promotionHealthLeases[job.PromotionID]
					lease.until = clockPastForScheduleTest()
					s.promotionHealthLeases[job.PromotionID] = lease
					schedule := s.promotionHealthSchedules[job.PromotionID]
					schedule.nextCheckAt = lease.until
					s.promotionHealthSchedules[job.PromotionID] = schedule
					s.mu.Unlock()
				case *PgStore:
					_, e = s.pool.Exec(ctx, "UPDATE project_environment_promotion_health SET lease_until=clock_timestamp()-interval '1 second',next_check_at=clock_timestamp()-interval '1 second' WHERE promotion_id=$1", job.PromotionID)
					requirePromotionSnapshot(t, e)
				}
				nextToken := uuid.NewString()
				claimed, e := health.ClaimProjectEnvironmentPromotionHealth(ctx, job.AccountID, job.PromotionID, nextToken)
				requirePromotionSnapshot(t, e)
				if !claimed {
					t.Fatal("expired claim stranded work")
				}
				requirePromotionSnapshot(t, health.ReleaseProjectEnvironmentPromotionHealth(ctx, job.PromotionID, token))
				current, e = scheduler.ProjectEnvironmentPromotionHealthLeaseCurrent(ctx, job.AccountID, job.PromotionID, nextToken)
				requirePromotionSnapshot(t, e)
				if !current {
					t.Fatal("stale finish cleared newer lease")
				}
				_, e = health.AdvanceProjectEnvironmentPromotionHealth(ctx, job.AccountID, job.PromotionID, PromotionHealthObservation{Generation: h.Generation, LeaseToken: token})
				if !errors.Is(e, ErrConflict) {
					t.Fatal("expired owner observed", e)
				}
				return
			}
			requirePromotionSnapshot(t, health.ReleaseProjectEnvironmentPromotionHealth(ctx, job.PromotionID, token))
			if scenario == "due_sample" {
				var due time.Time
				switch st := store.(type) {
				case *MemStore:
					st.mu.Lock()
					due = st.promotionHealthSchedules[job.PromotionID].nextCheckAt
					st.mu.Unlock()
				case *PgStore:
					requirePromotionSnapshot(t, st.pool.QueryRow(ctx, "SELECT next_check_at FROM project_environment_promotion_health WHERE promotion_id=$1", job.PromotionID).Scan(&due))
				}
				if delta := due.Sub(h.CheckedAt.Add(api.ProjectReleaseVerificationInterval)); delta < -time.Millisecond || delta > time.Millisecond {
					t.Fatal("sample interval was not persisted", due, h.CheckedAt)
				}
			}
			if scenario == "recovery" {
				h.Generation++
				h.Phase = "failed"
				setPromotionScheduleHealth(t, store, job.PromotionID, h)
				_, e = store.UpdateProjectEnvironmentPromotion(ctx, job.AccountID, job.PromotionID, "failed", "", nil)
				requirePromotionSnapshot(t, e)
				req := api.ProjectPromotionMigrationRecoveryRequest{RequestID: strings.Repeat("a", 32), ExpectedTaskID: uuid.NewString(), ExpectedGeneration: h.Generation, Action: "resume", DatabaseStateChecked: true, SchemaCompatible: true, EvidenceHash: strings.Repeat("b", 64)}
				now := time.Now().UTC()
				r := promotionRecoveryRecord{Receipt: api.ProjectPromotionMigrationRecovery{Request: req, Status: "quiescing", OperatorAccountID: job.AccountID, RequestedAt: now, Deadline: now.Add(api.ProjectReleaseVerificationTimeout)}}
				switch st := store.(type) {
				case *MemStore:
					st.mu.Lock()
					if st.promotionRecoveries == nil {
						st.promotionRecoveries = map[string]map[string]promotionRecoveryRecord{}
					}
					st.promotionRecoveries[job.PromotionID] = map[string]promotionRecoveryRecord{req.RequestID: r}
					st.mu.Unlock()
				case *PgStore:
					raw, err := json.Marshal(r)
					requirePromotionSnapshot(t, err)
					requirePromotionSnapshot(t, sqlc.New().InsertPromotionRecovery(ctx, st.pool, sqlc.InsertPromotionRecoveryParams{PromotionID: job.PromotionID, RequestID: req.RequestID, RequestHash: req.Fingerprint(), Payload: raw}))
				}
				next, newToken := claim(true)
				if next == nil || next.PromotionID != job.PromotionID {
					t.Fatal("failed promotion recovery was not scheduled", next)
				}
				requirePromotionSnapshot(t, health.ReleaseProjectEnvironmentPromotionHealth(ctx, job.PromotionID, newToken))
				r.Receipt.Status = "verifying"
				switch st := store.(type) {
				case *MemStore:
					st.mu.Lock()
					st.promotionRecoveries[job.PromotionID][req.RequestID] = r
					st.mu.Unlock()
				case *PgStore:
					requirePromotionSnapshot(t, savePromotionRecoveryDB(ctx, st.pool, job.PromotionID, r, "quiescing"))
				}
				next, _ = claim(true)
				if next == nil || next.PromotionID != job.PromotionID {
					t.Fatal("recovery status change did not wake work", next)
				}
				return
			}
			pending, e := health.PendingProjectEnvironmentPromotionHealth(ctx)
			requirePromotionSnapshot(t, e)
			for _, p := range pending {
				if p.PromotionID == job.PromotionID {
					t.Fatal("finished job immediately requeued")
				}
			}
			if scenario == "wake" {
				h.Generation++
				h.Phase = "rolling_back"
				setPromotionScheduleHealth(t, store, job.PromotionID, h)
				next, _ := claim(true)
				if next == nil || next.PromotionID != job.PromotionID {
					t.Fatal("new rollback did not wake sleeping job", next)
				}
			}
		})
	}
}

func clockPastForScheduleTest() time.Time { return time.Now().Add(-time.Second) }
func setPromotionScheduleHealth(t *testing.T, store Store, id string, h *api.ProjectPromotionHealth) {
	t.Helper()
	switch s := store.(type) {
	case *MemStore:
		s.mu.Lock()
		s.promotionHealth[id] = clonePromotionHealth(h)
		s.mu.Unlock()
	case *PgStore:
		requirePromotionSnapshot(t, savePromotionHealthDB(t.Context(), s.pool, id, h.Generation-1, h))
	}
}

func TestPromotionScheduleDeadlines(t *testing.T) {
	now := time.Now().UTC()
	checked := now
	for _, phase := range []string{"preflight", "verifying", "restoring"} {
		h := &api.ProjectPromotionHealth{Phase: phase, CheckedAt: &checked, Deadline: now.Add(10 * time.Second)}
		if next := nextPromotionHealthCheck(h, nil, now); !next.Equal(h.Deadline) {
			t.Fatal("sample interval hid deadline", phase, next)
		}
		h.Deadline = now.Add(-time.Second)
		if next := nextPromotionHealthCheck(h, nil, now); !next.After(now) {
			t.Fatal("expired deadline caused a busy loop", phase, next)
		}
	}
}
