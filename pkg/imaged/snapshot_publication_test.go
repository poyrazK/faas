package imaged

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

func publicationCount(t *testing.T, ops *wire.OpsMetrics, tier, outcome string) float64 {
	t.Helper()
	families, err := ops.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "imaged_snapshot_publication_total" {
			continue
		}
		for _, metric := range family.Metric {
			labels := map[string]string{}
			for _, label := range metric.Label {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["tier"] == tier && labels["outcome"] == outcome {
				return metric.GetCounter().GetValue()
			}
		}
	}
	t.Fatalf("missing publication metric for %s/%s", tier, outcome)
	return 0
}

func TestWarmSnapshotPromotionFollowsFirstDurablePublication(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(ctx, "warm-publication@example.com", "pro")
	app, _ := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "warm-publication", RAMMB: 256, MaxConcurrency: 3})
	dep, _ := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:abc", Kind: state.DeploymentKindImage})
	minRequests, minMs, count, readyMs := 5, 1500, int64(9), int64(2000)
	ops := wire.NewOpsMetrics("imaged")
	h := New(store, &fakeNotifier{}, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger()).
		WithOpsMetrics(ops).WithAudit(audit.New(store, silentLogger(), nil, "imaged"))
	p := snapshotWrittenPayload{
		DeploymentID: dep.ID, StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierWarm, "published"),
		FCVersion: "1.10.0", Tier: state.SnapshotTierWarm, MemBytes: 256 << 20,
		WarmMinRequests: &minRequests, WarmMinMs: &minMs, RequestCount: &count, ReadyToParkMs: &readyMs,
	}
	for i := 0; i < 2; i++ {
		if err := h.handleSnapshotWritten(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	snap, err := store.LatestSnapshotForTier(ctx, dep.ID, state.SnapshotTierWarm)
	if err != nil {
		t.Fatal(err)
	}
	events, err := store.ListEvents(ctx, acct.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	var promotions int
	for _, event := range events {
		if event.Kind != "app.warm_snapshot_promoted" {
			continue
		}
		promotions++
		if event.Actor != "imaged" {
			t.Fatalf("promotion actor = %q", event.Actor)
		}
		if event.Subject == nil || *event.Subject != uuid.MustParse(acct.ID) {
			t.Fatalf("promotion subject = %v, want %s", event.Subject, acct.ID)
		}
		var data map[string]any
		if err := json.Unmarshal(event.Data, &data); err != nil {
			t.Fatal(err)
		}
		if data["snapshot_id"] != snap.ID || data["request_count"] != float64(count) ||
			data["warm_min_requests"] != float64(minRequests) || data["warm_min_ms"] != float64(minMs) ||
			data["framework_ready_to_park_ms"] != float64(readyMs) || data["mem_bytes"] != float64(256<<20) {
			t.Fatalf("promotion evidence = %+v", data)
		}
	}
	if promotions != 1 {
		t.Fatalf("promotions = %d, want 1", promotions)
	}
	if got := publicationCount(t, ops, "warm", wire.SnapshotPublicationPublished); got != 1 {
		t.Fatalf("published = %v", got)
	}
	if got := publicationCount(t, ops, "warm", wire.SnapshotPublicationDuplicate); got != 1 {
		t.Fatalf("duplicate = %v", got)
	}
}

func TestWarmSnapshotStalePublicationHasNoPromotion(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(ctx, "warm-stale@example.com", "pro")
	app, _ := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "warm-stale", RAMMB: 256, MaxConcurrency: 3})
	dep, _ := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:abc", Kind: state.DeploymentKindImage})
	source, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateParked), 256, "node", "")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if err := store.MarkAppRuntimeConfigChanged(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	ops := wire.NewOpsMetrics("imaged")
	h := New(store, &fakeNotifier{}, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger()).WithOpsMetrics(ops).
		WithAudit(audit.New(store, silentLogger(), nil, "imaged"))
	if err := h.handleSnapshotWritten(ctx, snapshotWrittenPayload{
		DeploymentID: dep.ID, SourceInstanceID: source.ID, SourceStartedAt: source.StartedAt,
		StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierWarm, "stale"),
		FCVersion:  "1.10.0", Tier: state.SnapshotTierWarm,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LatestSnapshotForTier(ctx, dep.ID, state.SnapshotTierWarm); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stale row exists: %v", err)
	}
	events, err := store.ListEvents(ctx, acct.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Kind == "app.warm_snapshot_promoted" {
			t.Fatal("stale notification emitted promotion")
		}
	}
	if got := publicationCount(t, ops, "warm", wire.SnapshotPublicationStaleConfig); got != 1 {
		t.Fatalf("stale_config = %v", got)
	}
}

// Hides the cache listing capability: cleanup must use recorded keys even
// when none of the compute-node blobs are cached on the control plane.
type snapshotUnlistedBackend struct{ storage.StorageBackend }

type snapshotPolicyRaceStore struct {
	*state.MemStore
	accountID string
	appID     string
	inserted  bool
}

func (s *snapshotPolicyRaceStore) PublishSnapshotIfRuntimeFresh(ctx context.Context, snap state.Snapshot, sourceInstanceID string, sourceStartedAt time.Time) (state.Snapshot, error) {
	stored, err := s.MemStore.PublishSnapshotIfRuntimeFresh(ctx, snap, sourceInstanceID, sourceStartedAt)
	if err != nil {
		return stored, err
	}
	if !s.inserted {
		s.inserted = true
		if err := s.MemStore.UpsertAppSecretWithClassInScope(ctx, s.accountID, s.appID, api.DefaultEnvScope,
			"SESSION_TOKEN", "kid", "hash", state.SecretClassEphemeral, []byte("sealed")); err != nil {
			return state.Snapshot{}, err
		}
	}
	return stored, nil
}

func TestSnapshotPublicationRejectsRAMMismatchAndCleansCandidate(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(ctx, "snapshot-ram@example.com", "pro")
	app, _ := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "snapshot-ram", RAMMB: 256, MaxConcurrency: 3, IdleTimeoutS: 60})
	dep, _ := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:abc", Kind: state.DeploymentKindImage})
	be := mustLocalStorage(t, t.TempDir())
	h := New(store, &fakeNotifier{}, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger()).WithStorage(be)
	key := state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "wrong-ram")
	for _, part := range []string{key, state.SnapshotVMStateKey(state.Snapshot{StorageKey: key})} {
		if err := be.Put(ctx, part, strings.NewReader(part)); err != nil {
			t.Fatal(err)
		}
	}

	err := h.handleSnapshotWritten(ctx, snapshotWrittenPayload{
		DeploymentID: dep.ID,
		StorageKey:   key,
		FCVersion:    "1.10.0",
		Tier:         state.SnapshotTierInit,
		MemBytes:     128 << 20,
	})
	if err == nil || !strings.Contains(err.Error(), "snapshot RAM mismatch") {
		t.Fatalf("handleSnapshotWritten error = %v, want RAM mismatch", err)
	}
	if _, err := store.LatestSnapshot(ctx, dep.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("RAM-incompatible snapshot was published: %v", err)
	}
	for _, part := range []string{key, state.SnapshotVMStateKey(state.Snapshot{StorageKey: key})} {
		rc, err := be.Get(ctx, part)
		if err == nil {
			_ = rc.Close()
			t.Fatalf("RAM-incompatible snapshot artifact remains: %s", part)
		}
		if !storage.IsNotFound(err) {
			t.Fatal(err)
		}
	}
}

func TestSnapshotPublicationRejectsEphemeralSecretAndCleansCandidate(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "snapshot-ephemeral@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "snapshot-ephemeral", RAMMB: 256, MaxConcurrency: 3, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:abc", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppSecretWithClassInScope(ctx, acct.ID, app.ID, api.DefaultEnvScope,
		"SESSION_TOKEN", "kid", "hash", state.SecretClassEphemeral, []byte("sealed")); err != nil {
		t.Fatalf("UpsertAppSecretWithClassInScope: %v", err)
	}
	be := mustLocalStorage(t, t.TempDir())
	ops := wire.NewOpsMetrics("imaged")
	h := New(store, &fakeNotifier{}, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger()).WithStorage(be).WithOpsMetrics(ops)
	key := state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "ephemeral")
	for _, part := range []string{key, state.SnapshotVMStateKey(state.Snapshot{StorageKey: key})} {
		if err := be.Put(ctx, part, strings.NewReader(part)); err != nil {
			t.Fatal(err)
		}
	}

	err = h.handleSnapshotWritten(ctx, snapshotWrittenPayload{
		DeploymentID: dep.ID, StorageKey: key, FCVersion: "1.10.0", Tier: state.SnapshotTierInit,
	})
	if err == nil || !strings.Contains(err.Error(), "ephemeral secrets") {
		t.Fatalf("handleSnapshotWritten error = %v, want ephemeral-secret rejection", err)
	}
	if _, err := store.LatestSnapshot(ctx, dep.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("ephemeral-secret snapshot was published: %v", err)
	}
	for _, part := range []string{key, state.SnapshotVMStateKey(state.Snapshot{StorageKey: key})} {
		rc, err := be.Get(ctx, part)
		if err == nil {
			_ = rc.Close()
			t.Fatalf("ephemeral-secret snapshot artifact remains: %s", part)
		}
		if !storage.IsNotFound(err) {
			t.Fatal(err)
		}
	}
	if got := publicationCount(t, ops, "init", wire.SnapshotPublicationRejectedEphemeral); got != 1 {
		t.Fatalf("rejected_ephemeral = %v", got)
	}
}

func TestSnapshotPublicationRechecksEphemeralClassAfterRowInsert(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	acct, err := mem.CreateAccount(ctx, "snapshot-policy-race@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	app, err := mem.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "snapshot-policy-race", RAMMB: 256, MaxConcurrency: 3, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := mem.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:abc", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	store := &snapshotPolicyRaceStore{MemStore: mem, accountID: acct.ID, appID: app.ID}
	be := mustLocalStorage(t, t.TempDir())
	ops := wire.NewOpsMetrics("imaged")
	h := New(store, &fakeNotifier{}, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger()).WithStorage(be).WithOpsMetrics(ops)
	key := state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "policy-race")
	for _, part := range []string{key, state.SnapshotVMStateKey(state.Snapshot{StorageKey: key})} {
		if err := be.Put(ctx, part, strings.NewReader(part)); err != nil {
			t.Fatal(err)
		}
	}

	err = h.handleSnapshotWritten(ctx, snapshotWrittenPayload{
		DeploymentID: dep.ID, StorageKey: key, FCVersion: "1.10.0", Tier: state.SnapshotTierInit,
	})
	if err == nil || !strings.Contains(err.Error(), "ephemeral secrets") {
		t.Fatalf("handleSnapshotWritten error = %v, want reclassification rejection", err)
	}
	if _, err := mem.LatestSnapshot(ctx, dep.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("snapshot row survived retention-policy race: %v", err)
	}
	for _, part := range []string{key, state.SnapshotVMStateKey(state.Snapshot{StorageKey: key})} {
		rc, err := be.Get(ctx, part)
		if err == nil {
			_ = rc.Close()
			t.Fatalf("raced ephemeral snapshot artifact remains: %s", part)
		}
		if !storage.IsNotFound(err) {
			t.Fatal(err)
		}
	}
	if got := publicationCount(t, ops, "init", wire.SnapshotPublicationRejectedEphemeral); got != 1 {
		t.Fatalf("rejected_ephemeral = %v", got)
	}
	if got := publicationCount(t, ops, "init", wire.SnapshotPublicationPublished); got != 0 {
		t.Fatalf("published = %v, want 0 after policy rejection", got)
	}
}

func TestSnapshotPublicationDiscardsDelayedStaleNotification(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(ctx, "snapshot-delayed@example.com", "pro")
	app, _ := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "snapshot-delayed", RAMMB: 256, MaxConcurrency: 3})
	dep, _ := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:abc", Kind: state.DeploymentKindImage})
	source, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateParked), 256, "node", "")
	if err != nil {
		t.Fatal(err)
	}
	key := state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "delayed")
	be := mustLocalStorage(t, t.TempDir())
	for _, part := range []string{key, state.SnapshotVMStateKey(state.Snapshot{StorageKey: key})} {
		if err := be.Put(ctx, part, strings.NewReader(part)); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(time.Millisecond)
	if err := store.MarkAppRuntimeConfigChanged(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	h := New(store, &fakeNotifier{}, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger()).WithStorage(be)
	for _, sourceID := range []string{source.ID, ""} {
		if err := h.handleSnapshotWritten(ctx, snapshotWrittenPayload{
			DeploymentID: dep.ID, SourceInstanceID: sourceID, SourceStartedAt: source.StartedAt, StorageKey: key,
			FCVersion: "1.10.0", Tier: state.SnapshotTierInit,
		}); err != nil {
			t.Fatalf("stale notification %q: %v", sourceID, err)
		}
	}
	if _, err := store.LatestSnapshotForTier(ctx, dep.ID, state.SnapshotTierInit); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stale notification published a row: %v", err)
	}
	for _, part := range []string{key, state.SnapshotVMStateKey(state.Snapshot{StorageKey: key})} {
		rc, err := be.Get(ctx, part)
		if err == nil {
			_ = rc.Close()
			t.Fatalf("stale capture artifact remains: %s", part)
		}
		if !storage.IsNotFound(err) {
			t.Fatal(err)
		}
	}
	got, err := store.DeploymentByID(ctx, dep.ID)
	if err != nil || got.Status == state.DeployLive {
		t.Fatalf("stale notification activated deployment: (%+v, %v)", got, err)
	}
}

func TestSnapshotPublicationConflictPreservesWinnerAndCleansCandidate(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(ctx, "snapshot@example.com", "pro")
	app, _ := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "snapshot-publish", RAMMB: 256, MaxConcurrency: 3, IdleTimeoutS: 60})
	dep, _ := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:abc", Kind: state.DeploymentKindImage})
	be := mustLocalStorage(t, t.TempDir())
	h := New(store, &fakeNotifier{}, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger()).WithStorage(snapshotUnlistedBackend{be})
	first, second := state.SnapshotCaptureMemKey(dep.ID, "init", "first"), state.SnapshotCaptureMemKey(dep.ID, "init", "second")
	for _, key := range []string{first, second} {
		for _, part := range []string{key, state.SnapshotVMStateKey(state.Snapshot{StorageKey: key})} {
			if err := be.Put(ctx, part, strings.NewReader(part)); err != nil {
				t.Fatal(err)
			}
		}
		if err := h.handleSnapshotWritten(ctx, snapshotWrittenPayload{DeploymentID: dep.ID, StorageKey: key, FCVersion: "1.10.0", Tier: "init"}); err != nil {
			t.Fatal(err)
		}
	}
	latest, err := store.LatestSnapshot(ctx, dep.ID)
	if err != nil || latest.StorageKey != first {
		t.Fatalf("winner changed: %+v %v", latest, err)
	}
	for _, key := range []string{second, state.SnapshotVMStateKey(state.Snapshot{StorageKey: second})} {
		rc, err := be.Get(ctx, key)
		if err == nil {
			_ = rc.Close()
			t.Fatalf("unused candidate remains: %s", key)
		}
		if !storage.IsNotFound(err) {
			t.Fatal(err)
		}
	}
	// App deletion must also remove generation keys when the local cache is empty.
	h.cleanupSnapshotCaptures(ctx, snapshotUnlistedBackend{be}, dep.ID)
	for _, key := range []string{first, state.SnapshotVMStateKey(latest)} {
		rc, err := be.Get(ctx, key)
		if err == nil {
			_ = rc.Close()
			t.Fatalf("recorded object left behind: %s", key)
		}
		if !storage.IsNotFound(err) {
			t.Fatal(err)
		}
	}
}

func TestGitHubSnapshotCannotPromoteAfterNewerDeploymentAccepted(t *testing.T) {
	checkGitDrivenSnapshotCannotPromoteAfterNewerDeploymentAccepted(t, state.DeploymentKindGitHub)
}

func TestPreviewSnapshotCannotPromoteAfterNewerDeploymentAccepted(t *testing.T) {
	checkGitDrivenSnapshotCannotPromoteAfterNewerDeploymentAccepted(t, state.DeploymentKindPreview)
}

func checkGitDrivenSnapshotCannotPromoteAfterNewerDeploymentAccepted(t *testing.T, kind state.DeploymentKind) {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "github-snapshot-fence@example.test", "pro")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "github-snapshot-fence", RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	stable, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:stable"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, stable.ID); err != nil {
		t.Fatal(err)
	}
	older, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: kind,
		CommitSHA: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(ctx, older.ID, state.DeploySnapshotting, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: kind,
		CommitSHA: strings.Repeat("b", 40)}); err != nil {
		t.Fatal(err)
	}
	backend := mustLocalStorage(t, t.TempDir())
	notifier := &fakeNotifier{}
	h := New(store, notifier, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger()).WithStorage(backend)
	key := state.SnapshotCaptureMemKey(older.ID, state.SnapshotTierInit, "older")
	for _, part := range []string{key, state.SnapshotVMStateKey(state.Snapshot{StorageKey: key})} {
		if err := backend.Put(ctx, part, strings.NewReader(part)); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.handleSnapshotWritten(ctx, snapshotWrittenPayload{DeploymentID: older.ID, StorageKey: key,
		FCVersion: "1.10.0", Tier: state.SnapshotTierInit}); err != nil {
		t.Fatalf("stale snapshot publication: %v", err)
	}
	oldRow, err := store.DeploymentByID(ctx, older.ID)
	if err != nil || oldRow.Status != state.DeploySuperseded {
		t.Fatalf("stale GitHub row = (%+v, %v)", oldRow, err)
	}
	stableRow, err := store.DeploymentByID(ctx, stable.ID)
	if err != nil || stableRow.Status != state.DeployLive {
		t.Fatalf("serving predecessor = (%+v, %v)", stableRow, err)
	}
	var supersededNotice bool
	for _, call := range notifier.calls {
		if strings.Contains(call.payload, `"kind":"candidate_route"`) {
			t.Fatalf("stale deployment route was published: %+v", call)
		}
		if strings.Contains(call.payload, `"status":"superseded"`) && strings.Contains(call.payload, older.ID) {
			supersededNotice = true
		}
	}
	if !supersededNotice {
		t.Fatalf("stale deployment did not notify its terminal status: %+v", notifier.calls)
	}
}
