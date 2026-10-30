// adr: 20261030102813 — a newer canary supersedes the one in flight.
package state_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestPg_RetiredInFlightCanaryEndsAborted reproduces Hunt 14 H14-112: a
// second deploy during a canary left two canaries beside the stable that
// meterd never promoted, and every path that retired the older canary left it
// superseded but still rolling_out.
func TestPg_RetiredInFlightCanaryEndsAborted(t *testing.T) {
	for _, tc := range []struct {
		name string
		// retire makes the deployment that retires the in-flight canary live
		// and returns its ID.
		retire     func(t *testing.T, s *state.PgStore, pool *pgxpool.Pool, ctx context.Context, appID, stableID string) string
		wantStable int
	}{
		{
			name: "newer canary goes live",
			retire: func(t *testing.T, s *state.PgStore, _ *pgxpool.Pool, ctx context.Context, appID, _ string) string {
				newer := createPgCanary(t, s, ctx, appID, "sha256:newer-canary", 4)
				if err := s.MarkDeploymentLive(ctx, newer); err != nil {
					t.Fatalf("MarkDeploymentLive(newer canary): %v", err)
				}
				return newer
			},
			wantStable: 99,
		},
		{
			name: "stable deploy goes live",
			retire: func(t *testing.T, s *state.PgStore, _ *pgxpool.Pool, ctx context.Context, appID, _ string) string {
				stable, err := s.CreateDeployment(ctx, state.Deployment{
					AppID: appID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:stable-deploy",
					Status: state.DeployPending, Scope: "default",
				})
				if err != nil {
					t.Fatal(err)
				}
				if err := s.MarkDeploymentLive(ctx, stable.ID); err != nil {
					t.Fatalf("MarkDeploymentLive(stable): %v", err)
				}
				return stable.ID
			},
		},
		{
			name: "stacked canary completes",
			retire: func(t *testing.T, s *state.PgStore, pool *pgxpool.Pool, ctx context.Context, appID, stableID string) string {
				// The pre-fix stacked state: a second canary already serves
				// beside the first, and an operator advances it to the end.
				newer := createPgCanary(t, s, ctx, appID, "sha256:stacked-canary", 1)
				activatePgCanary(t, pool, ctx, newer)
				if _, err := pool.Exec(ctx, `update deployments set traffic_percent = 98 where id = $1`, stableID); err != nil {
					t.Fatal(err)
				}
				if _, _, err := s.AdvanceCanary(ctx, newer, state.CanaryAdvanceParams{
					ExpectedStep: 0, TrafficPercent: 100,
					Audit: state.DeploymentAudit{Kind: state.DeployTrafficChanged, Actor: "test"},
				}); err != nil {
					t.Fatalf("AdvanceCanary(terminal): %v", err)
				}
				return newer
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, pool, ctx := pgStoreWithPool(t)
			_, appID, stableID := seedLiveDeploy(t, s, ctx, "canary-supersede", "canary-supersede")
			older := createPgCanary(t, s, ctx, appID, "sha256:older-canary", 4)
			activatePgCanary(t, pool, ctx, older)
			if _, err := pool.Exec(ctx, `update deployments set traffic_percent = 99 where id = $1`, stableID); err != nil {
				t.Fatal(err)
			}

			newer := tc.retire(t, s, pool, ctx, appID, stableID)

			got, err := s.DeploymentByID(ctx, older)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != state.DeploySuperseded || got.TrafficPercent != 0 || got.RolloutState != "aborted" ||
				got.RolloutAbortedAt == nil || got.RolloutAbortedReason != "superseded by deployment "+newer {
				t.Fatalf("older canary = status %s traffic %d rollout %s aborted_at %v reason %q; want superseded, 0, aborted by %s",
					got.Status, got.TrafficPercent, got.RolloutState, got.RolloutAbortedAt, got.RolloutAbortedReason, newer)
			}
			stable, err := s.DeploymentByID(ctx, stableID)
			if err != nil {
				t.Fatal(err)
			}
			if stable.TrafficPercent != tc.wantStable || stable.RolloutState != "complete" {
				t.Fatalf("stable = traffic %d rollout %s, want %d complete", stable.TrafficPercent, stable.RolloutState, tc.wantStable)
			}
			var serving, inFlight int
			if err := pool.QueryRow(ctx, `select coalesce(sum(traffic_percent), 0),
				count(*) filter (where canary_total_steps > 0 and rollout_state in ('pending', 'rolling_out'))
				from deployments where app_id = $1 and status = 'live'`, appID).Scan(&serving, &inFlight); err != nil {
				t.Fatal(err)
			}
			if serving != 100 || inFlight > 1 {
				t.Fatalf("live set serves %d%% with %d canaries in flight, want 100%% and at most one", serving, inFlight)
			}
		})
	}
}

// TestPg_SoleServingCanaryStaysBesideNewerCanary keeps the older canary when
// nothing else carries traffic: it is the newer canary's only predecessor.
func TestPg_SoleServingCanaryStaysBesideNewerCanary(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	_, appID, stableID := seedLiveDeploy(t, s, ctx, "canary-sole", "canary-sole")
	older := createPgCanary(t, s, ctx, appID, "sha256:sole-canary", 4)
	activatePgCanary(t, pool, ctx, older)
	if _, err := pool.Exec(ctx, `update deployments set traffic_percent = case when id = $1 then 100 else 0 end where id in ($1, $2)`, older, stableID); err != nil {
		t.Fatal(err)
	}
	newer := createPgCanary(t, s, ctx, appID, "sha256:sole-newer", 4)
	if err := s.MarkDeploymentLive(ctx, newer); err != nil {
		t.Fatalf("MarkDeploymentLive(newer canary): %v", err)
	}
	got, err := s.DeploymentByID(ctx, older)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.DeployLive || got.TrafficPercent != 99 || got.RolloutState != "rolling_out" {
		t.Fatalf("sole serving canary = status %s traffic %d rollout %s, want live 99 rolling_out", got.Status, got.TrafficPercent, got.RolloutState)
	}
}

func createPgCanary(t *testing.T, s *state.PgStore, ctx context.Context, appID, digest string, steps int) string {
	t.Helper()
	dep, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: appID, Kind: state.DeploymentKindImage, ImageDigest: digest,
		Status: state.DeployPending, Scope: "default", CanaryPreset: "balanced",
		CanaryTotalSteps: steps, TrafficPercent: 1,
	})
	if err != nil {
		t.Fatalf("CreateDeployment(canary %s): %v", digest, err)
	}
	return dep.ID
}

func activatePgCanary(t *testing.T, pool *pgxpool.Pool, ctx context.Context, id string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `update deployments
		set status = 'live', rollout_state = 'rolling_out', canary_step = 0, traffic_percent = 1,
		    rollout_started_at = now(), canary_step_started_at = now()
		where id = $1`, id); err != nil {
		t.Fatalf("activate canary fixture: %v", err)
	}
}
