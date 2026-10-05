package main

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/environmentgitops"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

type gitOpsFleetNotifier struct {
	mu             sync.Mutex
	listeners      map[chan db.Notification]bool
	nodes          []string
	published      []db.EdgeRuleChangedPayload
	failApply      bool
	failPrepareApp string
}

type gitOpsCommitReplyLost struct {
	state.EnvironmentGitOpsEffectStore
}

func (s gitOpsCommitReplyLost) ApplyEnvironmentGitOpsWithEffects(ctx context.Context, lease state.EnvironmentGitOpsLease, plan environmentsync.Plan, effects []state.EnvironmentGitOpsEffectSpec) ([]state.EnvironmentGitOpsStep, error) {
	if _, err := s.EnvironmentGitOpsEffectStore.ApplyEnvironmentGitOpsWithEffects(ctx, lease, plan, effects); err != nil {
		return nil, err
	}
	return nil, context.DeadlineExceeded // the transaction committed; its reply was lost
}

func (n *gitOpsFleetNotifier) Notify(_ context.Context, channel, raw string) error {
	if channel != db.NotifyEdgeRuleChanged {
		return nil
	}
	payload, err := db.ParseEdgeRuleChangedPayload(raw)
	if err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.published = append(n.published, payload)
	if payload.Phase == "apply" && n.failApply || payload.Phase == "prepare" && payload.AppID == n.failPrepareApp {
		return errors.New("test transport unavailable")
	}
	for listener := range n.listeners {
		for _, node := range n.nodes {
			ack, _ := json.Marshal(db.EdgeRuleAckPayload{Generation: payload.Generation, Phase: payload.Phase, Node: node})
			listener <- db.Notification{Channel: db.NotifyEdgeRuleAck, Payload: string(ack)}
		}
	}
	return nil
}

func (n *gitOpsFleetNotifier) Subscribe(context.Context, []string) (<-chan db.Notification, func(), error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.listeners == nil {
		n.listeners = map[chan db.Notification]bool{}
	}
	listener := make(chan db.Notification, 128)
	n.listeners[listener] = true
	return listener, func() { n.mu.Lock(); delete(n.listeners, listener); n.mu.Unlock() }, nil
}

func (*gitOpsFleetNotifier) WaitFor(context.Context, string, func(string) bool, time.Duration) (string, error) {
	return "", db.ErrWaitTimeout
}

func gitOpsFleetNode(t *testing.T, store *state.MemStore, name string) {
	t.Helper()
	role, target := "compute-only", "http://127.0.0.1:8080"
	if _, err := store.CreateComputeNode(t.Context(), state.ComputeNode{Name: name, Active: true,
		TargetURL: "tcp://127.0.0.1:50051", Role: &role, GatewayTargetURL: &target}); err != nil {
		t.Fatal(err)
	}
}

func gitOpsBackendFixture(t *testing.T, count int, notifier *gitOpsFleetNotifier) (*server, *state.MemStore, state.EnvironmentGitSource, []state.App) {
	t.Helper()
	srv, store, account, project, app := newProjectLifecycleFixture(t)
	if _, err := store.UpdateProjectBinding(t.Context(), account.ID, project.ID, "example/shop", "main", 42); err != nil {
		t.Fatal(err)
	}
	apps := []state.App{app}
	if count == 2 {
		worker, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: project.ID,
			Slug: "shop-worker", WorkloadName: "worker", Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		apps = append(apps, worker)
	}
	source, err := store.CreateEnvironmentGitSource(t.Context(), account.ID, project.ID, "production", state.EnvironmentGitSourceSpec{
		RepositoryID: 123, InstallationID: 42, Repository: "example/shop", Ref: "refs/heads/main",
		ManifestPath: "environments/production.yaml", Mode: "enforce", ApprovalPolicy: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	definition := api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: "shop", Environment: "production", Workloads: map[string]api.EnvironmentWorkload{}}
	for _, app := range apps {
		if err := store.UpsertAppEnvInScope(t.Context(), account.ID, app.ID, "production", "MODE", "console"); err != nil {
			t.Fatal(err)
		}
		definition.Workloads[app.WorkloadName] = api.EnvironmentWorkload{App: app.Slug, Variables: map[string]string{"MODE": "production"},
			Policies: &[]api.EnvironmentPolicy{{Name: "headers", Kind: "headers", Action: json.RawMessage(`{"response_headers":[{"name":"X-Test","action":"set","value":"approved"}]}`)}}}
	}
	desired, err := environmentsync.Compile(definition)
	if err != nil {
		t.Fatal(err)
	}
	source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), state.ApproveEnvironmentRevision{AccountID: account.ID,
		SourceID: source.ID, CommitSHA: strings.Repeat("a", 40), Desired: desired, ApprovedBy: "test-owner"})
	if err != nil {
		t.Fatal(err)
	}
	adoption, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), account.ID, source.ID)
	if err != nil || !adoption.CanApply() {
		t.Fatalf("adoption preview: %+v %v", adoption, err)
	}
	if err := store.AdoptEnvironmentGitOps(t.Context(), account.ID, source.ID, adoption.Hash); err != nil {
		t.Fatal(err)
	}
	for _, node := range notifier.nodes {
		gitOpsFleetNode(t, store, node)
	}
	srv.notif = notifier
	srv.WithEdgeRuleFleetRequired(true)
	return srv, store, source, apps
}

func gitOpsBackendWorker(srv *server, store *state.MemStore) *environmentgitops.Worker {
	return &environmentgitops.Worker{Store: store, Backend: &environmentGitOpsBackend{server: srv, intent: store, effects: store},
		LeaseDuration: time.Minute, CheckInterval: time.Minute, RetryInterval: time.Second}
}

func TestEnvironmentGitOpsBackendRecoversCommittedEffectsBeforeEqualIntentCanConverge(t *testing.T) {
	for _, lostReply := range []bool{false, true} {
		t.Run(map[bool]string{false: "notification-outage", true: "commit-reply-lost"}[lostReply], func(t *testing.T) {
			testGitOpsBackendCommittedRecovery(t, lostReply)
		})
	}
}

func testGitOpsBackendCommittedRecovery(t *testing.T, lostReply bool) {
	notifier := &gitOpsFleetNotifier{nodes: []string{"node-a", "node-b"}, failApply: !lostReply}
	srv, store, source, _ := gitOpsBackendFixture(t, 1, notifier)
	worker := gitOpsBackendWorker(srv, store)
	if lostReply {
		worker.Backend.(*environmentGitOpsBackend).effects = gitOpsCommitReplyLost{store}
	}
	if worked, err := worker.RunOnce(t.Context()); err != nil || !worked {
		t.Fatalf("first attempt: %v %v", worked, err)
	}
	runs, err := store.ListEnvironmentGitOpsRuns(t.Context(), source.AccountID, source.ID, 1)
	if err != nil || len(runs) != 1 || runs[0].Status != "partial" {
		t.Fatalf("failed apply hid partial execution: %+v %v", runs, err)
	}
	var committedSteps []state.EnvironmentGitOpsStep
	if json.Unmarshal(runs[0].Steps, &committedSteps) != nil || len(committedSteps) == 0 {
		t.Fatal("lost reply discarded the committed step journal")
	}
	current, _ := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, "production")
	if current.AppliedRevisionID != "" {
		t.Fatal("saved intent was reported as fully applied")
	}
	notifier.mu.Lock()
	generation := notifier.published[0].Generation
	if lostReply {
		for _, event := range notifier.published {
			if event.Phase == "abort" {
				t.Fatal("ambiguous commit released the old policy fence")
			}
		}
	}
	notifier.mu.Unlock()
	// A fresh controller has no in-memory convergence or acknowledgement state.
	// A gateway joining after the commit must be included during recovery.
	freshNotifier := &gitOpsFleetNotifier{nodes: []string{"node-a", "node-b"}}
	gitOpsFleetNode(t, store, "node-c")
	fresh := newServer(store, srv.log, "gregale.dev", freshNotifier)
	fresh.WithEdgeRuleFleetRequired(true)
	worker = gitOpsBackendWorker(fresh, store)
	worker.Now = func() time.Time { return time.Now().Add(2 * time.Second) }
	if worked, err := worker.RunOnce(t.Context()); err != nil || !worked {
		t.Fatalf("recovered attempt: %v %v", worked, err)
	}
	current, _ = store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, "production")
	if current.AppliedRevisionID != "" {
		t.Fatal("recovery ignored the newly registered gateway's missing acknowledgement")
	}
	// The next recovery receives a fresh apply ACK from the complete fleet.
	freshNotifier = &gitOpsFleetNotifier{nodes: []string{"node-a", "node-b", "node-c"}}
	fresh = newServer(store, srv.log, "gregale.dev", freshNotifier)
	fresh.WithEdgeRuleFleetRequired(true)
	worker = gitOpsBackendWorker(fresh, store)
	worker.Now = func() time.Time { return time.Now().Add(6 * time.Second) }
	if worked, err := worker.RunOnce(t.Context()); err != nil || !worked {
		t.Fatalf("complete recovered fleet: %v %v", worked, err)
	}
	current, _ = store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, "production")
	if current.AppliedRevisionID != source.ApprovedRevisionID {
		t.Fatal("acknowledged recovery did not publish the applied revision")
	}
	freshNotifier.mu.Lock()
	defer freshNotifier.mu.Unlock()
	if len(freshNotifier.published) != 1 || freshNotifier.published[0].Phase != "apply" || freshNotifier.published[0].Generation != generation {
		t.Fatalf("recovery did not replay the persisted generation: %+v", freshNotifier.published)
	}
}

func TestEnvironmentGitOpsBackendPreparesWholePolicyBatchBeforeAnyIntentWrite(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "two-workloads", true: "second-prepare-fails"}[fail], func(t *testing.T) {
			notifier := &gitOpsFleetNotifier{nodes: []string{"node-a"}}
			srv, store, source, apps := gitOpsBackendFixture(t, 2, notifier)
			slices.SortFunc(apps, func(a, b state.App) int { return strings.Compare(a.ID, b.ID) })
			if fail {
				notifier.failPrepareApp = apps[1].ID
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if worked, err := gitOpsBackendWorker(srv, store).RunOnce(ctx); err != nil || !worked {
				t.Fatalf("batch attempt: %v %v", worked, err)
			}
			for _, app := range apps {
				variables, err := store.ListAppEnvInScope(t.Context(), source.AccountID, app.ID, "production")
				expected := "production"
				if fail {
					expected = "console"
				}
				if err != nil || len(variables) != 1 || variables[0].Value != expected {
					t.Fatalf("whole batch preflight failed: %+v %v", variables, err)
				}
			}
			notifier.mu.Lock()
			defer notifier.mu.Unlock()
			applyCount, prepareCount := 0, 0
			for _, event := range notifier.published {
				if event.Phase == "prepare" {
					prepareCount++
				}
				if event.Phase == "apply" {
					if prepareCount != 2 {
						t.Fatal("applied before preparing the complete graph")
					}
					applyCount++
				}
			}
			if !fail && applyCount != 2 || fail && applyCount != 0 {
				t.Fatalf("apply count = %d", applyCount)
			}
		})
	}
}
