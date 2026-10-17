package state

import (
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func promotionHealthEvidence(policy api.ProjectPromotionHealthPolicy, candidateErrors, stableErrors int) []api.ProjectReleaseProbeEvidence {
	var evidence []api.ProjectReleaseProbeEvidence
	for _, p := range policy.Probes {
		evidence = append(evidence, api.ProjectReleaseProbeEvidence{Probe: p, CandidateRequests: api.RouteHealthProbeRequestsPerMinute, StableRequests: api.RouteHealthProbeRequestsPerMinute, CandidateErrors: candidateErrors, StableErrors: stableErrors})
	}
	return evidence
}

func TestPromotionHealthDecision(t *testing.T) {
	now := time.Now().UTC()
	policy := api.ProjectPromotionHealthPolicy{Probes: []api.ProjectReleaseProbe{{AppID: "app", Slug: "api", Method: "GET", Path: "/database-health"}}}
	for _, tc := range []struct {
		name, phase, action, want             string
		candidate, stable, healthy, regressed int
		expired, unauth, missing, oversized   bool
	}{
		{name: "preflight healthy", phase: "preflight", healthy: 1, want: "cutover"},
		{name: "post-cutover healthy", phase: "verifying", healthy: 1, want: "finalizing"},
		{name: "first sample cannot cut over", phase: "preflight", want: "preflight"},
		{name: "candidate database fails before cutover", phase: "preflight", candidate: 1, regressed: 1, want: "failed"},
		{name: "candidate database fails after cutover", phase: "verifying", candidate: 1, regressed: 1, want: "rolling_back"},
		{name: "both versions fail", phase: "verifying", candidate: 1, stable: 1, regressed: 1, want: "verifying"},
		{name: "baseline fails while candidate healthy", phase: "preflight", stable: 1, healthy: 1, want: "preflight"},
		{name: "auth unknown", phase: "preflight", unauth: true, healthy: 1, want: "preflight"},
		{name: "missing probes unknown", phase: "preflight", missing: true, healthy: 1, want: "preflight"},
		{name: "oversized counts unknown", phase: "preflight", oversized: true, healthy: 1, want: "preflight"},
		{name: "deadline preserves preflight baseline", phase: "preflight", expired: true, want: "failed"},
		{name: "deadline with healthy baseline restores", phase: "verifying", expired: true, want: "rolling_back"},
		{name: "deadline cannot restore failing baseline", phase: "verifying", expired: true, stable: 1, want: "rollback_failed"},
		{name: "record completion requires finalizing", phase: "finalizing", action: "complete", want: "succeeded"},
		{name: "operator edit stops monitoring", phase: "verifying", action: "conflict", want: "conflicted"},
		{name: "cutover deadline cannot publish", phase: "cutover", expired: true, want: "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checked := now.Add(-api.ProjectReleaseVerificationInterval)
			h := &api.ProjectPromotionHealth{Policy: policy, Phase: tc.phase, Generation: 5, StartedAt: checked, Deadline: now.Add(time.Hour), CheckedAt: &checked, HealthyRounds: tc.healthy, RegressedRounds: tc.regressed, Evidence: promotionHealthEvidence(policy, tc.candidate, tc.stable)}
			if tc.expired {
				h.Deadline = now
			}
			if tc.action == "complete" {
				h.Verdict = "healthy"
				h.HealthyRounds = api.ProjectReleaseVerificationRounds
			}
			p := ProjectEnvironmentPromotion{}
			if tc.phase == "verifying" || tc.phase == "finalizing" {
				p.TargetReleaseSetID = "candidate"
			}
			evidence := promotionHealthEvidence(policy, tc.candidate, tc.stable)
			if tc.unauth {
				evidence[0].Unauthenticated = 1
			}
			if tc.missing {
				evidence = nil
			}
			if tc.oversized {
				evidence[0].CandidateRequests++
			}
			next, err := decidePromotionHealth(h, p, PromotionHealthObservation{Generation: h.Generation, Action: tc.action, Evidence: evidence}, now)
			if err != nil || next.Phase != tc.want || next.Generation != 6 {
				t.Fatalf("next=%+v err=%v", next, err)
			}
			if h.Generation != 5 || h.Phase != tc.phase {
				t.Fatal("decision mutated previous checkpoint")
			}
			if _, err = decidePromotionHealth(next, p, PromotionHealthObservation{Generation: 5, Evidence: evidence}, now.Add(time.Minute)); !errors.Is(err, ErrConflict) {
				t.Fatal("stale sample counted")
			}
		})
	}
}

func TestPromotionHealthMemory(t *testing.T) {
	testPromotionHealthStore(t, func(*testing.T) Store { return NewMemStore() })
}

func setPromotionHealthTestCheckpoint(t *testing.T, store Store, id string, h *api.ProjectPromotionHealth) {
	t.Helper()
	switch s := store.(type) {
	case *MemStore:
		s.mu.Lock()
		s.promotionHealth[id] = clonePromotionHealth(h)
		s.mu.Unlock()
	case *PgStore:
		requirePromotionSnapshot(t, savePromotionHealthDB(t.Context(), s.pool, id, h.Generation, h))
	default:
		t.Fatal("unsupported health fixture store")
	}
}

func testPromotionHealthStore(t *testing.T, factory func(*testing.T) Store) {
	for _, scenario := range []string{"publish", "preflight_failure", "stale_sample", "new_target", "finalize_target_edit", "lease", "policy_copy", "lease_expiry"} {
		t.Run(scenario, func(t *testing.T) {
			f := newPromotionSnapshotFixture(t, factory(t), false)
			policy := &api.ProjectPromotionHealthPolicy{}
			for _, app := range f.apps {
				policy.Probes = append(policy.Probes, api.ProjectReleaseProbe{AppID: app.ID, Slug: app.Slug, Method: "GET", Path: "/health"})
			}
			sort.Slice(policy.Probes, func(i, j int) bool { return policy.Probes[i].AppID < policy.Probes[j].AppID })
			f.promotion.HealthPolicy = policy
			f.create(t)
			f.prepare(t)
			hs := f.store.(ProjectPromotionHealthStore)
			h, err := hs.ProjectEnvironmentPromotionHealth(t.Context(), f.account.ID, f.promotion.ID)
			requirePromotionSnapshot(t, err)
			if h == nil || h.Phase != "preflight" {
				t.Fatal("health not captured at admission")
			}
			if scenario == "policy_copy" {
				h.Policy.Probes[0].Path = "/changed-policy"
				stored, err := hs.ProjectEnvironmentPromotionHealth(t.Context(), f.account.ID, f.promotion.ID)
				requirePromotionSnapshot(t, err)
				if stored.Policy.Probes[0].Path != "/health" {
					t.Fatal("public health read aliased the saved policy")
				}
				if s, ok := f.store.(*PgStore); ok {
					if err := savePromotionHealthDB(t.Context(), s.pool, f.promotion.ID, h.Generation, h); err == nil {
						t.Fatal("saved policy was mutable")
					}
				}
				return
			}
			publisher := f.store.(ProjectEnvironmentPromotionReleaseSetStore)
			if _, err = f.store.UpdateProjectEnvironmentPromotion(t.Context(), f.account.ID, f.promotion.ID, "succeeded", "", nil); err == nil {
				t.Fatal("legacy writer bypassed destination health")
			}
			if _, err = publisher.PublishProjectEnvironmentPromotionReleaseSet(t.Context(), f.account.ID, f.promotion.ID, 1800, f.candidates); !errors.Is(err, ErrConflict) {
				t.Fatal("cutover bypassed health gate", err)
			}
			if scenario == "lease" || scenario == "lease_expiry" {
				first, second := uuid.NewString(), uuid.NewString()
				claimed, err := hs.ClaimProjectEnvironmentPromotionHealth(t.Context(), f.account.ID, f.promotion.ID, first)
				requirePromotionSnapshot(t, err)
				if !claimed {
					t.Fatal("first lease denied")
				}
				if scenario == "lease_expiry" {
					switch s := f.store.(type) {
					case *MemStore:
						s.SetClockForTest(func() time.Time { return time.Now().Add(2 * api.ProjectPromotionHealthLeaseDuration) })
					case *PgStore:
						_, err = s.pool.Exec(t.Context(), "UPDATE project_environment_promotion_health SET lease_until=clock_timestamp()-interval '1 second' WHERE promotion_id=$1", f.promotion.ID)
						requirePromotionSnapshot(t, err)
					}
					claimed, err = hs.ClaimProjectEnvironmentPromotionHealth(t.Context(), f.account.ID, f.promotion.ID, second)
					requirePromotionSnapshot(t, err)
					if !claimed {
						t.Fatal("expired worker lease stranded the promotion")
					}
					return
				}
				claimed, err = hs.ClaimProjectEnvironmentPromotionHealth(t.Context(), f.account.ID, f.promotion.ID, second)
				requirePromotionSnapshot(t, err)
				if claimed {
					t.Fatal("concurrent lease admitted")
				}
				requirePromotionSnapshot(t, hs.ReleaseProjectEnvironmentPromotionHealth(t.Context(), f.promotion.ID, second))
				claimed, err = hs.ClaimProjectEnvironmentPromotionHealth(t.Context(), f.account.ID, f.promotion.ID, second)
				requirePromotionSnapshot(t, err)
				if claimed {
					t.Fatal("foreign release removed lease")
				}
				requirePromotionSnapshot(t, hs.ReleaseProjectEnvironmentPromotionHealth(t.Context(), f.promotion.ID, first))
				claimed, err = hs.ClaimProjectEnvironmentPromotionHealth(t.Context(), f.account.ID, f.promotion.ID, second)
				requirePromotionSnapshot(t, err)
				if !claimed {
					t.Fatal("released lease was not recoverable")
				}
				return
			}
			if scenario == "new_target" {
				_, err = f.store.(ProjectReleaseSetStore).PublishProjectReleaseSet(t.Context(), f.account.ID, f.project.ID, "production", 1800, f.targetGraph.Members)
				requirePromotionSnapshot(t, err)
			}
			candidateErrors := 0
			if scenario == "preflight_failure" {
				candidateErrors = 1
			}
			evidence := promotionHealthEvidence(*policy, candidateErrors, 0)
			h, err = hs.AdvanceProjectEnvironmentPromotionHealth(t.Context(), f.account.ID, f.promotion.ID, PromotionHealthObservation{Generation: h.Generation, Evidence: evidence})
			requirePromotionSnapshot(t, err)
			if scenario == "new_target" {
				if h.Phase != "conflicted" {
					t.Fatal("new intent overwritten")
				}
				return
			}
			if scenario == "stale_sample" {
				_, err = hs.AdvanceProjectEnvironmentPromotionHealth(t.Context(), f.account.ID, f.promotion.ID, PromotionHealthObservation{Generation: h.Generation - 1, Evidence: evidence})
				if !errors.Is(err, ErrConflict) {
					t.Fatal("stale sample counted")
				}
				return
			}
			// Persist a real previous sample time, avoiding a minute of wall-clock
			// waiting while exercising the production store's interval checks.
			checked := time.Now().Add(-api.ProjectReleaseVerificationInterval)
			h.CheckedAt = &checked
			setPromotionHealthTestCheckpoint(t, f.store, f.promotion.ID, h)
			h, err = hs.AdvanceProjectEnvironmentPromotionHealth(t.Context(), f.account.ID, f.promotion.ID, PromotionHealthObservation{Generation: h.Generation, Evidence: evidence})
			requirePromotionSnapshot(t, err)
			if scenario == "preflight_failure" {
				if h.Phase != "failed" {
					t.Fatal("failed candidates cut over")
				}
				if _, err = f.store.UpdateProjectEnvironmentPromotion(t.Context(), f.account.ID, f.promotion.ID, "running", "", nil); err == nil {
					t.Fatal("stale worker reopened a failed promotion")
				}
				return
			}
			if h.Phase != "cutover" {
				t.Fatal("healthy gate did not open", h.Phase)
			}
			_, err = publisher.PublishProjectEnvironmentPromotionReleaseSet(t.Context(), f.account.ID, f.promotion.ID, 1800, f.candidates)
			requirePromotionSnapshot(t, err)
			h, err = hs.ProjectEnvironmentPromotionHealth(t.Context(), f.account.ID, f.promotion.ID)
			requirePromotionSnapshot(t, err)
			if h.Phase != "verifying" || h.CheckedAt != nil || h.HealthyRounds != 0 {
				t.Fatal("publication did not atomically reset health", h)
			}
			for range api.ProjectReleaseVerificationRounds {
				checked = time.Now().Add(-api.ProjectReleaseVerificationInterval)
				h.CheckedAt = &checked
				setPromotionHealthTestCheckpoint(t, f.store, f.promotion.ID, h)
				h, err = hs.AdvanceProjectEnvironmentPromotionHealth(t.Context(), f.account.ID, f.promotion.ID, PromotionHealthObservation{Generation: h.Generation, Evidence: promotionHealthEvidence(*policy, 0, 0)})
				requirePromotionSnapshot(t, err)
			}
			if scenario == "finalize_target_edit" {
				requirePromotionSnapshot(t, f.store.UpsertAppEnvInScope(t.Context(), f.account.ID, f.apps[0].ID, "production", "MODE", "new-intent"))
			}
			h, err = hs.AdvanceProjectEnvironmentPromotionHealth(t.Context(), f.account.ID, f.promotion.ID, PromotionHealthObservation{Generation: h.Generation, Action: "complete"})
			requirePromotionSnapshot(t, err)
			want := "succeeded"
			if scenario == "finalize_target_edit" {
				want = "conflicted"
			}
			if h.Phase != want {
				t.Fatal("completion ignored destination ownership", h.Phase)
			}
		})
	}
}
