package logarchive

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/onebox-faas/faas/pkg/grace"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/conformance"
	"github.com/onebox-faas/faas/pkg/storage"
)

type retryLogCatalog struct {
	*state.MemStore
	fail bool
}

func (c *retryLogCatalog) PublishRuntimeLogArchiveFragment(ctx context.Context, i, d string, n int64, h, fragment string) error {
	if c.fail {
		c.fail = false
		return errors.New("metadata unavailable")
	}
	return c.MemStore.PublishRuntimeLogArchiveFragment(ctx, i, d, n, h, fragment)
}

func TestRegionalArchivePublicationRetryAndRoutes(t *testing.T) {
	ctx := t.Context()
	s := state.NewMemStore()
	fx := conformance.Seed(t, s)
	id := "eu-logs"
	if _, err := s.UpsertComputeRegion(ctx, state.ComputeRegion{ID: id, DisplayName: id, AcceptingNewApps: true}); err != nil {
		t.Fatal(err)
	}
	app := fx.App
	app.ID, app.Slug, app.NodeID, app.ComputeRegion = "", "logs-"+uuid.NewString(), "", id
	app, err := s.CreateApp(ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	node := fx.Node
	node.ID, node.Name, node.Region = "", "logs-"+uuid.NewString(), &id
	node, err = s.CreateComputeNode(ctx, node)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := s.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 128, node.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CaptureRuntimeLogArchiveBinding(ctx, instance.ID, node.ID); err != nil {
		t.Fatal(err)
	}
	legacy, _ := storage.NewLocalStorageBackend(t.TempDir())
	eu, _ := storage.NewLocalStorageBackend(t.TempDir())
	other, _ := storage.NewLocalStorageBackend(t.TempDir())
	router, err := storage.NewRegionRouter(map[string]storage.StorageBackend{id: eu, "us-logs": other}, legacy)
	if err != nil {
		t.Fatal(err)
	}
	client, err := ClientForConfig(Config{Regional: true}, router)
	if err != nil {
		t.Fatal(err)
	}
	spool := NewSpool(t.TempDir(), 1<<20)
	at := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	day := at.Format("2006-01-02")
	if _, err := spool.WriteWithLevel(instance.ID, 1, "stdout", at, "first", "info"); err != nil {
		t.Fatal(err)
	}
	catalog := &retryLogCatalog{MemStore: s, fail: true}
	sh, err := NewShipper(Config{Regional: true}, spool, client, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	sh.WithCatalog(catalog, s)
	n, _, err := sh.RunOnce(ctx)
	if err != nil || n != 0 {
		t.Fatalf("metadata failure committed: %d %v", n, err)
	}
	ref, err := s.RuntimeLogArchiveByInstanceDay(ctx, instance.ID, day)
	if err != nil || ref.Published() {
		t.Fatalf("pending reservation lost: %+v %v", ref, err)
	}
	if _, err := spool.WriteWithLevel(instance.ID, 2, "stderr", at, "second", "error"); err != nil {
		t.Fatal(err)
	}
	n, _, err = sh.RunOnce(ctx)
	if err != nil || n != 1 {
		t.Fatalf("retry failed: %d %v", n, err)
	}
	ref, err = s.RuntimeLogArchiveByInstanceDay(ctx, instance.ID, day)
	if err != nil || !ref.Published() {
		t.Fatalf("publication lost: %+v %v", ref, err)
	}
	body, err := client.OpenObject(ctx, ref.StorageKey)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(body)
	_ = body.Close()
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	if ref.Bytes != int64(len(raw)) || ref.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("metadata mismatch: %+v", ref)
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	text, err := io.ReadAll(gz)
	_ = gz.Close()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(text), `"msg":"first"`) != 1 || strings.Count(string(text), `"msg":"second"`) != 1 {
		t.Fatalf("retry duplicated/lost lines: %s", text)
	}
	// Stop after metadata publication but before the local commit, then let a
	// fresh worker append history before the original spool recovers its receipt.
	marker := filepath.Join(spool.root, instance.ID, day[:4], day[5:7], spoolFilePrefix+day+spoolCommittedSuffix)
	fault := &archiveCommitFaultClient{RoutedClient: client, marker: marker, fail: true}
	sh.s3 = fault
	if _, err := spool.WriteWithLevel(instance.ID, 3, "stdout", at, "third", "info"); err != nil {
		t.Fatal(err)
	}
	n, _, err = sh.RunOnce(ctx)
	if err != nil || n != 0 {
		t.Fatalf("commit fault not retained: %d %v", n, err)
	}
	fresh := NewSpool(t.TempDir(), 1<<20)
	if _, err := fresh.WriteWithLevel(instance.ID, 4, "stdout", at, "fourth", "info"); err != nil {
		t.Fatal(err)
	}
	peer, err := NewShipper(Config{Regional: true}, fresh, client, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	peer.WithCatalog(s, s)
	n, _, err = peer.RunOnce(ctx)
	if err != nil || n != 1 {
		t.Fatalf("peer lost remote history: %d %v", n, err)
	}
	if err := os.Remove(filepath.Join(marker, "fault")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	n, _, err = sh.RunOnce(ctx)
	if err != nil || n != 0 {
		t.Fatalf("receipt recovery reuploaded: %d %v", n, err)
	}
	if _, err := spool.WriteWithLevel(instance.ID, 5, "stdout", at, "fifth", "info"); err != nil {
		t.Fatal(err)
	}
	n, _, err = sh.RunOnce(ctx)
	if err != nil || n != 1 {
		t.Fatalf("resumed publisher failed: %d %v", n, err)
	}
	body, err = client.OpenObject(ctx, ref.StorageKey)
	if err != nil {
		t.Fatal(err)
	}
	gz, err = gzip.NewReader(body)
	if err != nil {
		t.Fatal(err)
	}
	text, err = io.ReadAll(gz)
	_ = gz.Close()
	_ = body.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, word := range []string{"first", "second", "third", "fourth", "fifth"} {
		if strings.Count(string(text), `"msg":"`+word+`"`) != 1 {
			t.Fatalf("interleaved receipt lost/duplicated %s: %s", word, text)
		}
	}
	// A copied partial can contain an already accepted fragment before it was
	// ever sealed on this worker. Its receipt prevents a duplicate publication.
	copied := NewSpool(t.TempDir(), 1<<20)
	if _, err := copied.WriteWithLevel(instance.ID, 5, "stdout", at, "fifth", "info"); err != nil {
		t.Fatal(err)
	}
	duplicate, err := NewShipper(Config{Regional: true}, copied, client, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	duplicate.WithCatalog(s, s)
	n, _, err = duplicate.RunOnce(ctx)
	if err != nil || n != 0 {
		t.Fatalf("copied partial duplicated receipt: %d %v", n, err)
	}
	relative := region.RuntimeLogArchiveKey("", instance.ID, day)
	for _, backend := range []storage.StorageBackend{legacy, other} {
		r, err := backend.Get(ctx, relative)
		if r != nil {
			_ = r.Close()
		}
		if !storage.IsNotFound(err) {
			t.Fatalf("cross-region write: %v", err)
		}
	}
	revoked, _ := storage.NewRegionRouter(map[string]storage.StorageBackend{}, legacy)
	absent := &RoutedClient{Regional: revoked}
	if _, err := absent.OpenObject(ctx, ref.StorageKey); !errors.Is(err, storage.ErrRegionNotConfigured) {
		t.Fatalf("revoked read fell back: %v", err)
	}
	if err := absent.Delete(ctx, ref.StorageKey); !errors.Is(err, storage.ErrRegionNotConfigured) {
		t.Fatalf("revoked delete fell back: %v", err)
	}
	if err := client.Delete(ctx, ref.StorageKey); err != nil {
		t.Fatal(err)
	}
	if err := client.Delete(ctx, ref.StorageKey); err != nil && !storage.IsNotFound(err) {
		t.Fatalf("idempotent delete: %v", err)
	}
}

func TestRegionalArchiveRequiresCatalog(t *testing.T) {
	legacy, _ := storage.NewLocalStorageBackend(t.TempDir())
	router, _ := storage.NewRegionRouter(map[string]storage.StorageBackend{}, legacy)
	spool := NewSpool(t.TempDir(), 1<<20)
	if _, err := spool.WriteWithLevel("unknown", 1, "stdout", time.Now(), "unowned", "info"); err != nil {
		t.Fatal(err)
	}
	sh, err := NewShipper(Config{Regional: true}, spool, &RoutedClient{Regional: router}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	n, _, err := sh.RunOnce(t.Context())
	if err != nil || n != 0 {
		t.Fatalf("unowned upload: %d %v", n, err)
	}
	if len(spool.FilesSnapshot()) != 1 {
		t.Fatal("failed ownership lost spool")
	}
}

type blockingArchiveUpload struct {
	client           *RoutedClient
	entered, release chan struct{}
}

func (b *blockingArchiveUpload) PutObject(ctx context.Context, key, typ string, r io.Reader, n int64) error {
	close(b.entered)
	select {
	case <-b.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return b.client.PutObject(ctx, key, typ, r, n)
}

type observedArchiveLockStore struct {
	*state.MemStore
	entered chan struct{}
}

func (s *observedArchiveLockStore) AcquireRuntimeLogArchivePublicationLock(ctx context.Context, id string) (func(context.Context), error) {
	close(s.entered)
	return s.MemStore.AcquireRuntimeLogArchivePublicationLock(ctx, id)
}

func TestArchiveDeletionWaitsForInFlightPut(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	s := state.NewMemStore()
	fx := conformance.Seed(t, s)
	i, err := s.CreateInstance(ctx, fx.App.ID, fx.Deployment.ID, string(state.StateRunning), 128, fx.Node.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CaptureRuntimeLogArchiveBinding(ctx, i.ID, fx.Node.ID); err != nil {
		t.Fatal(err)
	}
	// The S3 fake records remote PUT/DELETE ordering for the legacy route.
	var mu sync.Mutex
	objects := map[string][]byte{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			objects[r.URL.Path] = body
		case http.MethodDelete:
			delete(objects, r.URL.Path)
		default:
			t.Errorf("unexpected %s", r.Method)
		}
	}))
	defer server.Close()
	legacy, err := NewS3Client(server.URL, "us-east-1", "logs", "key", "secret")
	if err != nil {
		t.Fatal(err)
	}
	client := &RoutedClient{Legacy: legacy}
	blocked := &blockingArchiveUpload{client: client, entered: make(chan struct{}), release: make(chan struct{})}
	spool := NewSpool(t.TempDir(), 1<<20)
	at := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	if _, err := spool.WriteWithLevel(i.ID, 1, "stdout", at, "racing publication", "info"); err != nil {
		t.Fatal(err)
	}
	sh, err := NewShipper(Config{Bucket: "logs"}, spool, blocked, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	sh.WithCatalog(s, s)
	published := make(chan struct{})
	go func() { defer close(published); _, _, _ = sh.RunOnce(ctx) }()
	select {
	case <-blocked.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err := s.ScheduleAppDeletion(ctx, fx.App.ID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	observed := &observedArchiveLockStore{MemStore: s, entered: make(chan struct{})}
	g := grace.New(grace.Params{Store: observed, LogArchives: client})
	deleted := make(chan error, 1)
	go func() { deleted <- g.RunAppsOnce(ctx) }()
	select {
	case <-observed.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case err := <-deleted:
		t.Fatalf("purge bypassed publication lock: %v", err)
	default:
	}
	close(blocked.release)
	select {
	case <-published:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case err := <-deleted:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err := s.AppByID(ctx, fx.App.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("purge did not finish: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(objects) != 0 {
		t.Fatalf("in-flight PUT outlived purge: %v", objects)
	}
}

type archiveCommitFaultClient struct {
	*RoutedClient
	marker string
	fail   bool
}

func (c *archiveCommitFaultClient) PutObject(ctx context.Context, key, typ string, r io.Reader, n int64) error {
	if err := c.RoutedClient.PutObject(ctx, key, typ, r, n); err != nil {
		return err
	}
	if c.fail {
		c.fail = false
		if err := os.Mkdir(c.marker, 0700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(c.marker, "fault"), []byte("fault"), 0600); err != nil {
			return err
		}
	}
	return nil
}
