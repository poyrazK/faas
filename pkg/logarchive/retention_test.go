package logarchive

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/conformance"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/prometheus/client_golang/prometheus"
)

func retentionFixture(t *testing.T, s *state.MemStore, id, day string) state.RuntimeLogArchive {
	t.Helper()
	fx := conformance.Seed(t, s)
	if _, err := s.UpsertComputeRegion(t.Context(), state.ComputeRegion{ID: id, DisplayName: id, AcceptingNewApps: true}); err != nil {
		t.Fatal(err)
	}
	app := fx.App
	app.ID, app.Slug, app.NodeID, app.ComputeRegion = "", "logs-"+uuid.NewString(), "", id
	app, err := s.CreateApp(t.Context(), app)
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	node := fx.Node
	node.ID, node.Name, node.Region = "", "logs-"+uuid.NewString(), &id
	node, err = s.CreateComputeNode(t.Context(), node)
	if err != nil {
		t.Fatal(err)
	}
	i, err := s.CreateInstance(t.Context(), app.ID, dep.ID, string(state.StateRunning), 128, node.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	binding, err := s.CaptureRuntimeLogArchiveBinding(t.Context(), i.ID, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	ref := state.RuntimeLogArchive{RuntimeLogArchiveBinding: binding, Day: day, StorageKey: region.RuntimeLogArchiveKey(id, i.ID, day)}
	if err := s.ReserveRuntimeLogArchive(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	return ref
}

type retentionMetadataFault struct {
	*state.MemStore
	key  string
	fail bool
}

func (s *retentionMetadataFault) DeleteCollectedRuntimeLogArchive(ctx context.Context, ref state.RuntimeLogArchive) error {
	if s.fail && ref.StorageKey == s.key {
		s.fail = false
		return errors.New("metadata unavailable")
	}
	return s.MemStore.DeleteCollectedRuntimeLogArchive(ctx, ref)
}

func TestArchiveRetentionPaginationFailuresAndPendingOrphans(t *testing.T) {
	s := state.NewMemStore()
	now := time.Date(2026, 10, 3, 2, 0, 0, 0, time.FixedZone("east", 3*60*60))
	cutoff := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -RemoteRetentionDays()+1).Format("2006-01-02")
	pub := retentionFixture(t, s, "eu-retention", "2026-06-01")
	pending := retentionFixture(t, s, "eu-retention", "2026-06-02")
	missingRoute := retentionFixture(t, s, "us-retention", "2026-06-03")
	current := retentionFixture(t, s, "eu-retention", cutoff)
	fragment := strings.Repeat("b", 64)
	if err := s.PublishRuntimeLogArchiveFragment(t.Context(), pub.InstanceID, pub.Day, 5, strings.Repeat("a", 64), fragment); err != nil {
		t.Fatal(err)
	}
	eu, _ := storage.NewLocalStorageBackend(t.TempDir())
	us, _ := storage.NewLocalStorageBackend(t.TempDir())
	legacy, _ := storage.NewLocalStorageBackend(t.TempDir())
	routes, err := storage.NewRegionRouter(map[string]storage.StorageBackend{"eu-retention": eu, "us-retention": us}, legacy)
	if err != nil {
		t.Fatal(err)
	}
	client := &RoutedClient{Regional: routes}
	unknown := region.RuntimeLogArchiveKey("eu-retention", uuid.NewString(), pub.Day)
	for _, key := range []string{pub.StorageKey, pending.StorageKey, missingRoute.StorageKey, current.StorageKey, unknown} {
		if err := client.PutObject(t.Context(), key, "application/gzip", bytes.NewBufferString("bytes"), 5); err != nil {
			t.Fatal(err)
		}
	}
	client.Regional, err = storage.NewRegionRouter(map[string]storage.StorageBackend{"eu-retention": eu}, legacy)
	if err != nil {
		t.Fatal(err)
	}
	fault := &retentionMetadataFault{MemStore: s, key: pub.StorageKey, fail: true}
	registry := prometheus.NewRegistry()
	c, err := NewRetentionCollector(RetentionParams{Store: fault, Archives: client, BatchSize: 1, Now: func() time.Time { return now }, Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	for step := 0; step < 3; step++ {
		result, err := c.RunOnce(t.Context())
		if result.Scanned != 1 || (step == 1 && (result.Collected != 1 || err != nil)) || (step != 1 && err == nil) {
			t.Fatalf("step %d: %+v %v", step, result, err)
		}
	}
	if accepted, err := s.RuntimeLogArchiveFragmentPublished(t.Context(), pub.InstanceID, pub.Day, fragment); err != nil || !accepted {
		t.Fatalf("metadata failure lost receipts: %v %v", accepted, err)
	}
	for _, ref := range []state.RuntimeLogArchive{pub, missingRoute} {
		refs, err := s.ListAppRuntimeLogArchives(t.Context(), ref.AppID)
		if err != nil || len(refs) != 1 {
			t.Fatalf("retry descriptor lost: %+v %v", refs, err)
		}
		if err := s.ReserveRuntimeLogArchive(t.Context(), ref); !errors.Is(err, state.ErrRuntimeLogArchiveExpired) {
			t.Fatalf("failed delete permits resurrection: %v", err)
		}
	}
	client.Regional = routes
	for step := 0; step < 4; step++ {
		result, err := c.RunOnce(t.Context())
		if err != nil || result.Scanned > 1 {
			t.Fatalf("retry page: %+v %v", result, err)
		}
	}
	for _, ref := range []state.RuntimeLogArchive{pub, pending, missingRoute} {
		refs, err := s.ListAppRuntimeLogArchives(t.Context(), ref.AppID)
		if err != nil || len(refs) != 0 {
			t.Fatalf("not collected: %+v %v", refs, err)
		}
		if r, err := client.OpenObject(t.Context(), ref.StorageKey); !storage.IsNotFound(err) {
			if r != nil {
				_ = r.Close()
			}
			t.Fatalf("object survived: %v", err)
		}
		if err := s.ReserveRuntimeLogArchive(t.Context(), ref); !errors.Is(err, state.ErrRuntimeLogArchiveExpired) {
			t.Fatalf("collected day revived: %v", err)
		}
	}
	for _, key := range []string{current.StorageKey, unknown} {
		r, err := client.OpenObject(t.Context(), key)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(r)
		_ = r.Close()
		if err != nil || string(raw) != "bytes" {
			t.Fatalf("protected object changed: %q %v", raw, err)
		}
	}
	if _, err := s.RuntimeLogArchiveByInstanceDay(t.Context(), current.InstanceID, current.Day); err != nil {
		t.Fatalf("inclusive retention boundary expired: %v", err)
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == "apid_log_archive_retention_collected_total" && family.Metric[0].Counter.GetValue() != 3 {
			t.Fatalf("collected metric: %v", family)
		}
	}
}

func TestArchiveRetentionWaitsForPublisherAndBlocksLateSpool(t *testing.T) {
	s := state.NewMemStore()
	ref := retentionFixture(t, s, "eu-retention-race", "2026-06-01")
	backend, _ := storage.NewLocalStorageBackend(t.TempDir())
	legacy, _ := storage.NewLocalStorageBackend(t.TempDir())
	routes, err := storage.NewRegionRouter(map[string]storage.StorageBackend{ref.ComputeRegion: backend}, legacy)
	if err != nil {
		t.Fatal(err)
	}
	client := &RoutedClient{Regional: routes}
	blocking := &blockingArchiveUpload{client: client, entered: make(chan struct{}), release: make(chan struct{})}
	var release sync.Once
	defer release.Do(func() { close(blocking.release) })
	spool := NewSpool(t.TempDir(), 1<<20)
	at, _ := time.Parse("2006-01-02", ref.Day)
	if _, err := spool.Write(ref.InstanceID, 1, "stdout", at, "old pending line"); err != nil {
		t.Fatal(err)
	}
	sh, err := NewShipper(Config{Regional: true}, spool, blocking, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	sh.WithCatalog(s, s)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, _, err := sh.RunOnce(ctx); done <- err }()
	select {
	case <-blocking.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	c, err := NewRetentionCollector(RetentionParams{Store: s, Archives: client, Timeout: 50 * time.Millisecond, Now: func() time.Time { return time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	result, err := c.RunOnce(ctx)
	if err == nil || result.Collected != 0 {
		t.Fatalf("collector passed active publisher: %+v %v", result, err)
	}
	if expired, err := s.RuntimeLogArchiveDayExpired(ctx, ref.InstanceID, ref.Day); err != nil || expired {
		t.Fatalf("expiry raced PUT: %v %v", expired, err)
	}
	release.Do(func() { close(blocking.release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	result, err = c.RunOnce(ctx)
	if err != nil || result.Collected != 1 {
		t.Fatalf("retry collection: %+v %v", result, err)
	}
	if _, err := spool.Write(ref.InstanceID, 2, "stdout", at, "late pending line"); err != nil {
		t.Fatal(err)
	}
	if count, _, err := sh.RunOnce(ctx); err != nil || count != 0 {
		t.Fatalf("late worker republished expired day: %d %v", count, err)
	}
	if r, err := client.OpenObject(ctx, ref.StorageKey); !storage.IsNotFound(err) {
		if r != nil {
			_ = r.Close()
		}
		t.Fatalf("late PUT resurrected object: %v", err)
	}
	if len(spool.FilesSnapshot()) != 0 || spool.LocalBytes() != 0 {
		t.Fatal("confirmed collection retained expired local backlog")
	}
	if _, err := spool.Write(ref.InstanceID, 3, "stdout", at, "later pending line"); !errors.Is(err, ErrSpoolDayCollected) {
		t.Fatalf("late writer recreated collected day: %v", err)
	}
}
