package imaged

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func postCaptureCandidate(t *testing.T, s state.Store, account, slug string) (state.App, state.Deployment) {
	t.Helper()
	a, err := s.CreateApp(t.Context(), state.App{AccountID: account, Slug: slug, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	return a, postCaptureDeployment(t, s, a)
}

func postCaptureDeployment(t *testing.T, s state.Store, a state.App) state.Deployment {
	t.Helper()
	at := time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond)
	raw, err := json.Marshal(state.StageState{Current: state.StageSnapshotPrepare, CurrentStartedAt: &at, History: []state.StageStateItem{}})
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: a.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:test", StageState: raw})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(t.Context(), d.ID, "/layers/test.ext4", "layers/test.ext4", 1024); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateDeploymentStatus(t.Context(), d.ID, state.DeploySnapshotting, ""); err != nil {
		t.Fatal(err)
	}
	d, err = s.DeploymentByID(t.Context(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func postCapturePublish(t *testing.T, s state.Store, d state.Deployment, tier string) state.Snapshot {
	t.Helper()
	snap, err := s.CreateSnapshot(t.Context(), state.Snapshot{DeploymentID: d.ID, Tier: tier, FCVersion: "1.10.0", StorageKey: "snap/" + uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func TestPostCaptureObservationStoreContract(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var s state.Store = state.NewMemStore()
			if backend == "postgres" {
				s = state.NewPgStore(pgtest.OpenMigrated(t))
			}
			ctx := t.Context()
			reader := s.(state.PostCaptureObservationStore)
			if got, err := reader.PostCaptureSnapshot(ctx); err != nil || got != (state.PostCaptureSnapshot{}) {
				t.Fatalf("empty=%+v %v", got, err)
			}
			account, err := s.CreateAccount(ctx, "post-capture@example.test", api.PlanScale)
			if err != nil {
				t.Fatal(err)
			}
			for i := range 257 {
				_, d := postCaptureCandidate(t, s, account.ID, fmt.Sprintf("published-%d", i))
				postCapturePublish(t, s, d, state.SnapshotTierInit)
			}
			// Uncaptured work and warm-only rows cannot prove init publication.
			_, warm := postCaptureCandidate(t, s, account.ID, "warm-only")
			postCapturePublish(t, s, warm, state.SnapshotTierWarm)
			postCaptureCandidate(t, s, account.ID, "uncaptured")
			app, parked := postCaptureCandidate(t, s, account.ID, "parked")
			node, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
			if err != nil {
				t.Fatal(err)
			}
			wake, err := uuid.NewV7()
			if err != nil {
				t.Fatal(err)
			}
			instance, err := s.CreateInstance(ctx, app.ID, parked.ID, string(state.StateParked), app.RAMMB, node.ID, wake.String())
			if err != nil {
				t.Fatal(err)
			}
			parkedAt := time.Now().UTC().Truncate(time.Microsecond)
			if err := s.UpdateInstanceStateWithTimestamp(ctx, instance.ID, string(state.StateParked), parkedAt); err != nil {
				t.Fatal(err)
			}
			_, verify := postCaptureCandidate(t, s, account.ID, "verifying")
			postCapturePublish(t, s, verify, state.SnapshotTierInit)
			verification := s.(state.DeploymentHostingVerificationStore)
			p, err := verification.UpdateDeploymentHostingVerification(ctx, verify.ID, state.HostingVerificationUpdate{Action: state.HostingVerificationBegin, At: time.Now().UTC()})
			if err != nil {
				t.Fatal(err)
			}
			_, retry := postCaptureCandidate(t, s, account.ID, "retry")
			postCapturePublish(t, s, retry, state.SnapshotTierInit)
			r, err := verification.UpdateDeploymentHostingVerification(ctx, retry.ID, state.HostingVerificationUpdate{Action: state.HostingVerificationBegin, At: time.Now().UTC()})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := verification.UpdateDeploymentHostingVerification(ctx, retry.ID, state.HostingVerificationUpdate{Action: state.HostingVerificationRetry, Attempt: r.Attempts, At: time.Now().UTC(), ErrorCode: apihostingreceipt.SmokeErrorGatewayUnavailable, RetryNotBefore: time.Now().Add(time.Second)}); err != nil {
				t.Fatal(err)
			}
			_, compat := postCaptureCandidate(t, s, account.ID, "compatibility")
			postCapturePublish(t, s, compat, state.SnapshotTierInit)
			if err := s.RecordMCPTaskPromotionHold(ctx, compat.ID, "retained gate", state.MCPTaskPromotionRecovery{Channel: "snapshot_written", Payload: json.RawMessage(`{"deployment_id":"` + compat.ID + `"}`)}); err != nil {
				t.Fatal(err)
			}
			got, err := reader.PostCaptureSnapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for class, n := range map[state.PostCaptureClass]int64{state.PostCaptureActivationPending: 257, state.PostCapturePublicationPending: 1, state.PostCaptureHostingVerifying: 1, state.PostCaptureHostingRetry: 1, state.PostCaptureCompatibilityHeld: 1, state.PostCaptureDependencyHeld: 0, state.PostCaptureGateOverdue: 0, state.PostCaptureUnknown: 0} {
				if got[class].Pending != n || got[class].UnknownAge != 0 {
					t.Fatalf("%s=%+v want=%d; all=%+v", class, got[class], n, got)
				}
			}
			if !got[state.PostCapturePublicationPending].OldestAt.Equal(parkedAt) {
				t.Fatalf("publication age used stage instead of parked time: %+v", got)
			}
			if _, err := verification.UpdateDeploymentHostingVerification(ctx, verify.ID, state.HostingVerificationUpdate{Action: state.HostingVerificationComplete, Attempt: p.Attempts, At: time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}
			if err := s.MarkAppRuntimeConfigChanged(ctx, app.ID); err != nil {
				t.Fatal(err)
			}
			got, err = reader.PostCaptureSnapshot(ctx)
			if err != nil || got[state.PostCapturePublicationPending].Pending != 0 || got[state.PostCaptureActivationPending].Pending != 258 {
				t.Fatalf("stale capture or completed verification=%+v %v", got, err)
			}
			if err := s.MarkDeploymentLive(ctx, verify.ID); err != nil {
				t.Fatal(err)
			}
			got, err = reader.PostCaptureSnapshot(ctx)
			if err != nil || got[state.PostCaptureActivationPending].Pending != 257 {
				t.Fatalf("live row retained: %+v %v", got, err)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := reader.PostCaptureSnapshot(canceled); err == nil {
				t.Fatal("canceled read succeeded")
			}
		})
	}
}

func TestPostCaptureDependencyGateObservation(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var s state.Store = state.NewMemStore()
			if backend == "postgres" {
				s = state.NewPgStore(pgtest.OpenMigrated(t))
			}
			ctx := t.Context()
			account, err := s.CreateAccount(ctx, "post-dependency@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			project, err := s.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "post-dependency"})
			if err != nil {
				t.Fatal(err)
			}
			target, err := s.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "backend", WorkloadName: "backend", Type: state.AppTypeApp, RAMMB: 256})
			if err != nil {
				t.Fatal(err)
			}
			postCaptureDeployment(t, s, target)
			owner, err := s.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "frontend", WorkloadName: "frontend", Type: state.AppTypeApp, RAMMB: 256, Manifest: state.AppManifest{ServiceBindings: []api.AppServiceBinding{{Service: "backend", Binding: api.ServiceBindingEnvKey("backend")}}, ProjectDependencyConditions: map[string]string{"backend": api.ComposeDependencyHealthy}}})
			if err != nil {
				t.Fatal(err)
			}
			candidate := postCaptureDeployment(t, s, owner)
			postCapturePublish(t, s, candidate, state.SnapshotTierInit)
			gates := s.(state.DeploymentDependencyGateStore)
			gate, err := gates.CheckDeploymentDependencies(ctx, candidate.ID, time.Now().UTC())
			if err == nil || gate == nil || gate.DeadlineAt == nil {
				t.Fatalf("expected durable gate: %+v %v", gate, err)
			}
			reader := s.(state.PostCaptureObservationStore)
			got, err := reader.PostCaptureSnapshot(ctx)
			if err != nil || got[state.PostCaptureDependencyHeld].Pending != 1 || got[state.PostCaptureActivationPending].Pending != 0 {
				t.Fatalf("held=%+v %v", got, err)
			}
			// A retained failure is actionable even when terminalization has not yet committed.
			if _, err := gates.CheckDeploymentDependencies(ctx, candidate.ID, *gate.DeadlineAt); err == nil {
				t.Fatal("expected expired gate")
			}
			got, err = reader.PostCaptureSnapshot(ctx)
			if err != nil || got[state.PostCaptureGateOverdue].Pending != 1 || got[state.PostCaptureDependencyHeld].Pending != 0 {
				t.Fatalf("expired=%+v %v", got, err)
			}
			if _, err := s.SetDeploymentFailed(ctx, candidate.ID, state.CodeDependencyTimeout, "expired"); err != nil {
				t.Fatal(err)
			}
			got, err = reader.PostCaptureSnapshot(ctx)
			if err != nil || got != (state.PostCaptureSnapshot{}) {
				t.Fatalf("terminal gate counted: %+v %v", got, err)
			}
		})
	}
}

func TestPostCaptureRollbackAndEphemeralExclusion(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var s state.Store = state.NewMemStore()
			if backend == "postgres" {
				s = state.NewPgStore(pgtest.OpenMigrated(t))
			}
			ctx := t.Context()
			account, err := s.CreateAccount(ctx, "post-rollback@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, d := postCaptureCandidate(t, s, account.ID, "rollback")
			old := postCapturePublish(t, s, d, state.SnapshotTierInit)
			verification := s.(state.DeploymentHostingVerificationStore)
			if _, err := verification.UpdateDeploymentHostingVerification(ctx, d.ID, state.HostingVerificationUpdate{Action: state.HostingVerificationBegin, At: time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}
			if err := s.RecordMCPTaskPromotionHold(ctx, d.ID, "old attempt", state.MCPTaskPromotionRecovery{Channel: "snapshot_written", Payload: json.RawMessage(`{"deployment_id":"` + d.ID + `"}`)}); err != nil {
				t.Fatal(err)
			}
			if err := s.MarkDeploymentSuperseded(ctx, d.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.PrepareDeploymentRollback(ctx, app.ID, d.ID); err != nil {
				t.Fatal(err)
			}
			postCapturePublish(t, s, d, state.SnapshotTierWarm)
			reader := s.(state.PostCaptureObservationStore)
			got, err := reader.PostCaptureSnapshot(ctx)
			if err != nil || got != (state.PostCaptureSnapshot{}) {
				t.Fatalf("old captures or warm publication proved new attempt: %+v %v", got, err)
			}
			if err := s.MarkSnapshotStale(ctx, old.ID); err != nil {
				t.Fatal(err)
			}
			current := postCapturePublish(t, s, d, state.SnapshotTierInit)
			got, err = reader.PostCaptureSnapshot(ctx)
			if err != nil || got[state.PostCaptureActivationPending].Pending != 1 || !got[state.PostCaptureActivationPending].OldestAt.Equal(current.CreatedAt) {
				t.Fatalf("publication boundary or stale gates: %+v %v", got, err)
			}
			if err := s.RecordMCPTaskPromotionHold(ctx, d.ID, "renewed after rollback", state.MCPTaskPromotionRecovery{Channel: "snapshot_written", Payload: json.RawMessage(`{"deployment_id":"` + d.ID + `"}`)}); err != nil {
				t.Fatal(err)
			}
			got, err = reader.PostCaptureSnapshot(ctx)
			if err != nil || got[state.PostCaptureCompatibilityHeld].Pending != 1 || got[state.PostCaptureCompatibilityHeld].UnknownAge != 1 || got[state.PostCaptureActivationPending].Pending != 0 {
				t.Fatalf("renewed hold was ignored or invented a new first-held time: %+v %v", got, err)
			}
			if err := s.UpsertAppSecretWithClassInScope(ctx, account.ID, app.ID, "default", "TOKEN", "kid", "hash", state.SecretClassEphemeral, []byte("sealed")); err != nil {
				t.Fatal(err)
			}
			got, err = reader.PostCaptureSnapshot(ctx)
			if err != nil || got != (state.PostCaptureSnapshot{}) {
				t.Fatalf("intentional no-snapshot flow included: %+v %v", got, err)
			}
		})
	}
}

func TestPostCaptureExpiredHostingRecovery(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var s state.Store = state.NewMemStore()
			if backend == "postgres" {
				s = state.NewPgStore(pgtest.OpenMigrated(t))
			}
			ctx := t.Context()
			account, err := s.CreateAccount(ctx, "post-expired@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			_, d := postCaptureCandidate(t, s, account.ID, "expired")
			published := postCapturePublish(t, s, d, state.SnapshotTierInit)
			verification := s.(state.DeploymentHostingVerificationStore)
			at := time.Now().Add(-10 * time.Minute).UTC()
			p, err := verification.UpdateDeploymentHostingVerification(ctx, d.ID, state.HostingVerificationUpdate{Action: state.HostingVerificationBegin, At: at})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := verification.UpdateDeploymentHostingVerification(ctx, d.ID, state.HostingVerificationUpdate{Action: state.HostingVerificationRetry, Attempt: p.Attempts, At: at.Add(time.Second), ErrorCode: apihostingreceipt.SmokeErrorTransportUnavailable, RetryNotBefore: at.Add(time.Minute)}); err != nil {
				t.Fatal(err)
			}
			got, err := s.(state.PostCaptureObservationStore).PostCaptureSnapshot(ctx)
			if err != nil || got[state.PostCaptureGateOverdue].Pending != 1 || !got[state.PostCaptureGateOverdue].OldestAt.Equal(published.CreatedAt) {
				t.Fatalf("expired hosting boundary=%+v %v", got, err)
			}
		})
	}
}
