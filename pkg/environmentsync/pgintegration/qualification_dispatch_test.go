// adr: 521 — durable discovery is advisory, never a lease or VM receipt.
package pgintegration_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func dispatchIDs(t *testing.T, store state.EnvironmentGitOpsQualificationDiscoveryStore, nodeID, cursor string, limit int) []string {
	t.Helper()
	ids, err := store.ListEnvironmentWorkloadQualificationsForDispatch(t.Context(), nodeID, cursor, limit)
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

func TestEnvironmentQualificationDispatchDiscoveryPagesWithoutClaiming(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		_, _, requests := preparedQualificationFixture(t, basic)
		discovery := basic.(state.EnvironmentGitOpsQualificationDiscoveryStore)
		nodeID := qualificationPlacement(t, basic, 4096).NodeID
		want := []string{requests[0].ID, requests[1].ID}
		slices.Sort(want)
		first := dispatchIDs(t, discovery, nodeID, "", 1)
		if !reflect.DeepEqual(first, want[:1]) {
			t.Fatalf("first durable page: %v, want %v", first, want[:1])
		}
		// Alternate accepted UUID spelling has the same exclusive cursor.
		cursor := strings.ReplaceAll(first[0], "-", "")
		second := dispatchIDs(t, discovery, nodeID, cursor, 1)
		if !reflect.DeepEqual(second, want[1:]) || len(dispatchIDs(t, discovery, nodeID, second[0], 1)) != 0 {
			t.Fatal("pagination lost or repeated a durable request", second)
		}
		if !reflect.DeepEqual(dispatchIDs(t, discovery, nodeID, "", 2), want) {
			t.Fatal("new sweep could not rediscover work without notification delivery")
		}
		poller := basic.(state.EnvironmentGitSourcePollStore)
		now := time.Now()
		poll, err := poller.ClaimEnvironmentGitSourcePoll(t.Context(), uuid.NewString(), now, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if err := poller.FinishEnvironmentGitSourcePoll(t.Context(), poll,
			state.EnvironmentGitSourcePollResult{ErrorCode: "environment_git_source_unavailable"}, now, now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(dispatchIDs(t, discovery, nodeID, "", 2), want) {
			t.Fatal("Git outage discarded qualification of the last approved graph")
		}
		qualifier := basic.(state.EnvironmentGitOpsQualificationStore)
		claimed, err := qualifier.ClaimEnvironmentWorkloadQualification(t.Context(), first[0], "scheduler", time.Minute)
		if err != nil || claimed.Attempt != 1 {
			t.Fatal("discovery changed the attempt before claim", claimed.Attempt, err)
		}
		if !reflect.DeepEqual(dispatchIDs(t, discovery, nodeID, "", 2), want[1:]) {
			t.Fatal("active execution lease reentered dispatch discovery")
		}
		if err := qualifier.ValidateEnvironmentWorkloadQualification(t.Context(), claimed); err != nil {
			t.Fatal("reading the queue changed an active lease", err)
		}
		if _, err := basic.InstanceByID(t.Context(), claimed.ReservedInstanceID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("discovery or claim allocated a native instance", err)
		}
		for _, args := range []struct {
			node, cursor string
			limit        int
		}{
			{"", "", 1}, {uuid.Nil.String(), "", 1}, {"invalid-node", "", 1},
			{nodeID, "invalid-cursor", 1}, {nodeID, uuid.Nil.String(), 1},
			{nodeID, "", 0}, {nodeID, "", api.EnvironmentGitOpsQualificationDispatchBatchMax + 1},
		} {
			if _, err := discovery.ListEnvironmentWorkloadQualificationsForDispatch(t.Context(), args.node, args.cursor, args.limit); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatal("invalid or unbounded dispatch page accepted", err)
			}
		}
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := discovery.ListEnvironmentWorkloadQualificationsForDispatch(cancelled, nodeID, "", 1); !errors.Is(err, context.Canceled) {
			t.Fatal("cancelled discovery continued", err)
		}
	})
}

func TestEnvironmentQualificationDispatchDiscoveryRetainsUncertainReservations(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		_, _, requests := preparedQualificationFixture(t, basic)
		discovery := basic.(state.EnvironmentGitOpsQualificationDiscoveryStore)
		qualifier := basic.(state.EnvironmentGitOpsQualificationStore)
		placement := qualificationPlacement(t, basic, 4096)
		unadmitted, err := qualifier.ClaimEnvironmentWorkloadQualification(t.Context(), requests[0].ID, "crashed-before-admission", time.Second)
		if err != nil {
			t.Fatal(err)
		}
		held, err := qualifier.ClaimEnvironmentWorkloadQualification(t.Context(), requests[1].ID, "uncertain-native-owner", time.Second)
		if err != nil {
			t.Fatal(err)
		}
		admission, err := basic.(state.EnvironmentGitOpsQualificationInstanceStore).CreateEnvironmentWorkloadQualificationInstance(t.Context(), held, placement)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(max(0, time.Until(*held.LeaseUntil)+20*time.Millisecond))
		if got := dispatchIDs(t, discovery, placement.NodeID, "", 2); !reflect.DeepEqual(got, []string{unadmitted.ID}) {
			t.Fatal("expired reservation redispatched without retirement evidence", got)
		}
		// A generic terminal write cannot release the durable execution frame.
		// The exact original retirement is required.
		if err := basic.UpdateInstanceState(t.Context(), admission.Instance.ID, string(state.StateFailed)); err == nil {
			t.Fatal("generic terminal write released an uncertain reservation")
		}
		if got := dispatchIDs(t, discovery, placement.NodeID, "", 2); !reflect.DeepEqual(got, []string{unadmitted.ID}) {
			t.Fatal("terminal instance bypassed original retirement proof", got)
		}
		retireQualificationWithoutDispatch(t, basic, admission.Instance.ID)
		if got := dispatchIDs(t, discovery, placement.NodeID, "", 2); len(got) != 2 || !slices.Contains(got, held.ID) {
			t.Fatal("confirmed retired work could not resume", got)
		}
		for _, old := range []state.EnvironmentWorkloadQualificationRequest{unadmitted, held} {
			current, err := qualifier.ClaimEnvironmentWorkloadQualification(t.Context(), old.ID, "restarted-scheduler", time.Minute)
			if err != nil || current.ID != old.ID || current.GraphID != old.GraphID || current.Artifact != old.Artifact ||
				current.Attempt != old.Attempt+1 || current.LeaseToken == old.LeaseToken || current.ReservedInstanceID == old.ReservedInstanceID {
				t.Fatal("resume lost the durable work or reused attempt authority", err)
			}
		}
	})
}

func TestEnvironmentQualificationDispatchDiscoveryTracksOwnershipAndRevocation(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		lease, _, requests := preparedQualificationFixture(t, basic)
		discovery := basic.(state.EnvironmentGitOpsQualificationDiscoveryStore)
		nodeA := qualificationPlacement(t, basic, 4096).NodeID
		nodeB := qualificationPlacement(t, basic, 4096).NodeID
		for _, request := range requests {
			if err := basic.SetAppNodeID(t.Context(), request.AppID, nodeA); err != nil {
				t.Fatal(err)
			}
		}
		stale := dispatchIDs(t, discovery, nodeA, "", 2)
		if len(stale) != 2 || len(dispatchIDs(t, discovery, nodeB, "", 2)) != 0 {
			t.Fatal("foreign scheduler discovered owned work")
		}
		if err := basic.ReassignAppOwner(t.Context(), requests[0].AppID, nodeA, nodeB); err != nil {
			t.Fatal(err)
		}
		if got := dispatchIDs(t, discovery, nodeA, "", 2); !reflect.DeepEqual(got, []string{requests[1].ID}) {
			t.Fatal("previous scheduler retained moved work", got)
		}
		if got := dispatchIDs(t, discovery, nodeB, "", 2); !reflect.DeepEqual(got, []string{requests[0].ID}) {
			t.Fatal("new scheduler lost durable work", got)
		}
		holds := basic.(state.AccountAbuseHoldStore)
		if _, err := holds.SetAccountAbuseHold(t.Context(), lease.Source.AccountID, state.AccountAbuseHoldOperator, time.Now()); err != nil {
			t.Fatal(err)
		}
		if len(dispatchIDs(t, discovery, nodeA, "", 2)) != 0 || len(dispatchIDs(t, discovery, nodeB, "", 2)) != 0 {
			t.Fatal("held account entered qualification dispatch")
		}
		if _, err := holds.ReleaseAccountAbuseHold(t.Context(), lease.Source.AccountID); err != nil {
			t.Fatal(err)
		}
		control := basic.(state.EnvironmentGitOpsControlStore)
		if _, err := control.UpdateEnvironmentGitSource(t.Context(), lease.Source.AccountID, lease.Source.ID,
			state.EnvironmentGitSourceUpdate{ExpectedGeneration: lease.Source.Generation, Mode: "report"}); err != nil {
			t.Fatal(err)
		}
		if len(dispatchIDs(t, discovery, nodeA, "", 2)) != 0 || len(dispatchIDs(t, discovery, nodeB, "", 2)) != 0 {
			t.Fatal("revoked graph remained dispatchable")
		}
		for _, id := range stale {
			if _, err := basic.(state.EnvironmentGitOpsQualificationStore).ClaimEnvironmentWorkloadQualification(t.Context(), id, "stale-discovery", time.Minute); !errors.Is(err, state.ErrConflict) {
				t.Fatal("discovery granted authority after source revocation", err)
			}
		}
	})
}

func TestPgEnvironmentQualificationDispatchDiscoverySurvivesRestartAndRacingClaims(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	_, _, requests := preparedQualificationFixture(t, store)
	nodeID := qualificationPlacement(t, store, 4096).NodeID
	before := dispatchIDs(t, store, nodeID, "", 2)
	// A new store discovers committed work without a notification or an
	// in-memory queue, even after the publishing process has gone away.
	restarted := state.NewPgStore(pool)
	if !reflect.DeepEqual(before, dispatchIDs(t, restarted, nodeID, "", 2)) {
		t.Fatal("restart lost committed qualification work")
	}
	start, results := make(chan struct{}), make(chan error, 2)
	var wg sync.WaitGroup
	for _, worker := range []struct {
		store *state.PgStore
		name  string
	}{{store, "first-scheduler"}, {restarted, "second-scheduler"}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := worker.store.ClaimEnvironmentWorkloadQualificationForNode(t.Context(), requests[0].ID, nodeID, worker.name, time.Minute)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	claimed, conflicted := 0, 0
	for err := range results {
		switch {
		case err == nil:
			claimed++
		case errors.Is(err, state.ErrConflict):
			conflicted++
		default:
			t.Fatal("claim race returned unexpected error", err)
		}
	}
	if claimed != 1 || conflicted != 1 || !reflect.DeepEqual(dispatchIDs(t, restarted, nodeID, "", 2), []string{requests[1].ID}) {
		t.Fatal("stale discovery duplicated the execution lease", claimed, conflicted)
	}
}

func TestEnvironmentQualificationDispatchDiscoveryExcludesJobExecution(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		source, desired := seedMode(t, basic, "enforce")
		app, err := basic.CreateApp(t.Context(), state.App{AccountID: source.AccountID, ProjectID: source.ProjectID,
			Slug: "shop-job", Type: state.AppTypeApp, Status: state.AppActive, WorkloadClass: state.WorkloadClassJob,
			RAMMB: 512, MaxConcurrency: 1, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeJob}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := basic.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "production", Status: state.DeployLive,
			Kind: state.DeploymentKindImage, ImageDigest: "registry.example/job@sha256:" + strings.Repeat("c", 64)}); err != nil {
			t.Fatal(err)
		}
		desired.Definition.Workloads = map[string]api.EnvironmentWorkload{"job": {App: app.Slug,
			Source:  &api.EnvironmentWorkloadSource{Kind: "image", Image: "registry.example/job@sha256:" + strings.Repeat("d", 64)},
			Runtime: json.RawMessage(`{"execution_mode":"job"}`)}}
		desired, err = environmentsync.Compile(desired.Definition)
		if err != nil {
			t.Fatal(err)
		}
		source, _, err = basic.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("a", 40)))
		if err != nil {
			t.Fatal(err)
		}
		intent := basic.(intentTestStore)
		adoptWorkloadIntent(t, intent, source)
		lease, err := basic.ClaimEnvironmentGitOps(t.Context(), "job-preparer", time.Now(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := intent.ApplyEnvironmentGitOps(t.Context(), lease, claimedIntentPlan(t, intent, lease, desired)); err != nil {
			t.Fatal(err)
		}
		plan := claimedIntentPlan(t, intent, lease, desired)
		candidates, err := basic.(state.EnvironmentGitOpsPreparationStore).PrepareEnvironmentGitOpsImageCandidates(t.Context(), lease, plan)
		if err != nil || len(candidates) != 1 {
			t.Fatal("job candidate was not prepared", len(candidates), err)
		}
		if err := basic.SetDeploymentRootfs(t.Context(), candidates[0].DeploymentID, "/reviewed.ext4", "reviewed-job", 4096); err != nil {
			t.Fatal(err)
		}
		if err := basic.UpdateDeploymentStatus(t.Context(), candidates[0].DeploymentID, state.DeploySnapshotting, ""); err != nil {
			t.Fatal(err)
		}
		if graph, err := basic.(state.EnvironmentGitOpsGraphPreparationStore).ReconcileEnvironmentGitOpsPreparation(t.Context(), lease, plan); err != nil || graph.Phase != "prepared" {
			t.Fatal("job graph was not prepared", graph.Phase, err)
		}
		requests, err := basic.(state.EnvironmentGitOpsQualificationStore).QueueEnvironmentGitOpsQualification(t.Context(), lease, plan)
		if err != nil || len(requests) != 1 || requests[0].ExecutionMode != api.ExecutionModeJob {
			t.Fatal("job qualification was not queued", len(requests), err)
		}
		if _, err := basic.(state.EnvironmentGitOpsQualificationDispatchStore).ClaimEnvironmentWorkloadQualificationForNode(t.Context(), requests[0].ID,
			qualificationPlacement(t, basic, 4096).NodeID, "vm-dispatcher", time.Minute); !errors.Is(err, state.ErrConflict) {
			t.Fatal("VM dispatcher claimed unsupported job execution", err)
		}
		nodeID := qualificationPlacement(t, basic, 4096).NodeID
		if ids := dispatchIDs(t, basic.(state.EnvironmentGitOpsQualificationDiscoveryStore), nodeID, "", 1); len(ids) != 0 {
			t.Fatal("VM discovery dispatched a job without its qualification adapter", ids)
		}
	})
}

func TestEnvironmentQualificationNodeClaimFencesStaleOwnershipWithoutConsumingAttempt(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		_, _, requests := preparedQualificationFixture(t, basic)
		dispatch := basic.(state.EnvironmentGitOpsQualificationDispatchStore)
		nodeA, nodeB := qualificationPlacement(t, basic, 4096).NodeID, qualificationPlacement(t, basic, 4096).NodeID
		for _, request := range requests {
			if err := basic.SetAppNodeID(t.Context(), request.AppID, nodeA); err != nil {
				t.Fatal(err)
			}
		}
		if len(dispatchIDs(t, dispatch, nodeA, "", 2)) != 2 {
			t.Fatal("fixture has no advisory page for old owner")
		}
		if err := basic.ReassignAppOwner(t.Context(), requests[0].AppID, nodeA, nodeB); err != nil {
			t.Fatal(err)
		}
		if _, err := dispatch.ClaimEnvironmentWorkloadQualificationForNode(t.Context(), requests[0].ID, nodeA, "stale-owner", time.Minute); !errors.Is(err, state.ErrConflict) {
			t.Fatal("old owner reserved an attempt after transfer", err)
		}
		claimed, err := dispatch.ClaimEnvironmentWorkloadQualificationForNode(t.Context(), requests[0].ID, strings.ReplaceAll(nodeB, "-", ""), "new-owner", time.Minute)
		if err != nil || claimed.Attempt != 1 || claimed.ReservedInstanceID == "" {
			t.Fatal("refused stale claim consumed or lost fresh authority", claimed.Attempt, err)
		}
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := dispatch.ClaimEnvironmentWorkloadQualificationForNode(cancelled, requests[1].ID, nodeA, "cancelled", time.Minute); !errors.Is(err, context.Canceled) {
			t.Fatal("cancelled consumer reserved work", err)
		}
		for _, node := range []string{"", "bad-node", uuid.Nil.String()} {
			if _, err := dispatch.ClaimEnvironmentWorkloadQualificationForNode(t.Context(), requests[1].ID, node, "invalid-node", time.Minute); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatal("unbound scheduler claimed VM work", err)
			}
		}
		claimed, err = dispatch.ClaimEnvironmentWorkloadQualificationForNode(t.Context(), requests[1].ID, nodeA, "original-owner", time.Minute)
		if err != nil || claimed.Attempt != 1 {
			t.Fatal("invalid or cancelled claim changed the attempt", claimed.Attempt, err)
		}
	})
}
