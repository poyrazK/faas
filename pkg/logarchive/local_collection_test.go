package logarchive

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

type noCollectionUpload struct{ calls int }

func (c *noCollectionUpload) PutObject(context.Context, string, string, io.Reader, int64) error {
	c.calls++
	return errors.New("unexpected upload")
}

type unavailableCollectionCatalog struct {
	*state.MemStore
	calls int
}

func (c *unavailableCollectionCatalog) RuntimeLogArchiveBindingByInstance(context.Context, string) (state.RuntimeLogArchiveBinding, error) {
	c.calls++
	return state.RuntimeLogArchiveBinding{}, errors.New("catalog unavailable")
}

func TestLocalCollectionRequiresExactReceiptAndReclaimsLiveBuffers(t *testing.T) {
	store := state.NewMemStore()
	ref := retentionFixture(t, store, "eu-local-collection", "2026-06-01")
	spool := NewSpool(t.TempDir(), 1<<20)
	at, _ := time.Parse("2006-01-02", ref.Day)
	sealed, err := spool.Write(ref.InstanceID, 1, "stdout", at, "sealed line")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := spool.PrepareUpload(ref.InstanceID, ref.Day); err != nil {
		t.Fatal(err)
	}
	buffered, err := spool.Write(ref.InstanceID, 2, "stdout", at, "still buffered")
	if err != nil {
		t.Fatal(err)
	}
	base, err := spool.collectionBase(ref.InstanceID, ref.Day)
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{spoolCommittedSuffix, archiveGzipSuffix + ".pending", archiveGzipSuffix} {
		if err := os.WriteFile(base+suffix, []byte("retained history"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	fresh := at.AddDate(0, 0, 1)
	if _, err := spool.Write(ref.InstanceID, 3, "stdout", fresh, "fresh day"); err != nil {
		t.Fatal(err)
	}
	if _, err := spool.Write("other-instance", 4, "stdout", at, "other owner"); err != nil {
		t.Fatal(err)
	}
	before := spool.LocalBytes()
	spool.maxBytes = before
	client := &noCollectionUpload{}
	registry := prometheus.NewRegistry()
	sh, err := NewShipper(Config{Regional: true}, spool, client, nil, NewMetricsWithPrefix(registry, "vmmd"))
	if err != nil {
		t.Fatal(err)
	}
	sh.WithCatalog(store, store)
	if err := store.RetireRuntimeLogArchive(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	file := FileInfo{Instance: ref.InstanceID, Day: ref.Day}
	if _, err := sh.uploadFile(t.Context(), file); !errors.Is(err, state.ErrRuntimeLogArchiveExpired) {
		t.Fatalf("retirement granted cleanup: %v", err)
	}
	if marked, err := collectedMarker(base); err != nil || marked {
		t.Fatalf("unconfirmed marker: %v %v", marked, err)
	}
	if spool.LocalBytes() != before {
		t.Fatal("retirement discarded local backlog")
	}
	if _, err := spool.Write(ref.InstanceID, 9, "stdout", fresh, "full before collection"); !errors.Is(err, ErrSpoolFull) {
		t.Fatalf("capacity ceiling bypassed: %v", err)
	}
	if err := store.DeleteCollectedRuntimeLogArchive(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	if n, err := sh.uploadFile(t.Context(), file); err != nil || n != 0 {
		t.Fatalf("local collection: %d %v", n, err)
	}
	reclaimed := int64(sealed + buffered)
	if got := spool.LocalBytes(); got != before-reclaimed {
		t.Fatalf("remaining bytes=%d want %d", got, before-reclaimed)
	}
	for _, suffix := range []string{spoolPartialSuffix, spoolUploadSuffix, spoolCommittedSuffix, archiveGzipSuffix + ".pending", archiveGzipSuffix} {
		if _, err := os.Lstat(base + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("collected payload %s remains: %v", suffix, err)
		}
	}
	if len(spool.FilesSnapshot()) != 2 {
		t.Fatal("other day/owner changed")
	}
	if _, err := spool.Write(ref.InstanceID, 10, "stdout", fresh, "capacity reclaimed"); err != nil {
		t.Fatalf("collection did not free capacity: %v", err)
	}
	if _, err := spool.Write(ref.InstanceID, 5, "stdout", at, "late"); !errors.Is(err, ErrSpoolDayCollected) {
		t.Fatalf("live writer fence: %v", err)
	}
	if err := spool.CloseAll(); err != nil {
		t.Fatal(err)
	}
	restarted := NewSpool(spool.root, 1<<20)
	if _, err := restarted.Write(ref.InstanceID, 6, "stdout", at, "after restart"); !errors.Is(err, ErrSpoolDayCollected) {
		t.Fatalf("restart writer fence: %v", err)
	}
	if _, err := restarted.Write(ref.InstanceID, 7, "stdout", fresh, "new day remains writable"); err != nil {
		t.Fatal(err)
	}
	if err := restarted.CloseAll(); err != nil {
		t.Fatal(err)
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{"vmmd_log_archive_local_collected_files_total": 5, "vmmd_log_archive_local_collected_pending_bytes_total": float64(reclaimed)}
	for _, family := range families {
		if expected, ok := want[family.GetName()]; ok {
			if family.Metric[0].Counter.GetValue() != expected {
				t.Fatalf("metric %s=%v want %v", family.GetName(), family.Metric[0].Counter.GetValue(), expected)
			}
			delete(want, family.GetName())
		}
	}
	if len(want) != 0 || client.calls != 0 {
		t.Fatalf("metrics missing=%v uploads=%d", want, client.calls)
	}
}

func TestLocalCollectionDoesNotInferOlderUnreservedDay(t *testing.T) {
	store := state.NewMemStore()
	newer := retentionFixture(t, store, "eu-local-proof", "2026-06-02")
	if err := store.RetireRuntimeLogArchive(t.Context(), newer); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteCollectedRuntimeLogArchive(t.Context(), newer); err != nil {
		t.Fatal(err)
	}
	spool := NewSpool(t.TempDir(), 1<<20)
	at := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if _, err := spool.Write(newer.InstanceID, 1, "stdout", at, "never reserved"); err != nil {
		t.Fatal(err)
	}
	client := &noCollectionUpload{}
	sh, err := NewShipper(Config{Regional: true}, spool, client, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	sh.WithCatalog(store, store)
	file := FileInfo{Instance: newer.InstanceID, Day: "2026-06-01"}
	if _, err := sh.uploadFile(t.Context(), file); !errors.Is(err, state.ErrRuntimeLogArchiveExpired) {
		t.Fatalf("floor inferred collection: %v", err)
	}
	if len(spool.FilesSnapshot()) != 1 || spool.LocalBytes() == 0 || client.calls != 0 {
		t.Fatal("unreserved backlog discarded or uploaded")
	}
	if err := spool.CloseAll(); err != nil {
		t.Fatal(err)
	}
}

func TestLocalCollectionResumesEveryPayloadStateWithoutCatalog(t *testing.T) {
	for _, suffix := range []string{spoolPartialSuffix, spoolUploadSuffix, spoolCommittedSuffix, archiveGzipSuffix + ".pending", archiveGzipSuffix} {
		t.Run(suffix, func(t *testing.T) {
			spool := NewSpool(t.TempDir(), 1<<20)
			instance, day := "instance-recovered", "2026-06-01"
			base, err := spool.collectionBase(instance, day)
			if err != nil {
				t.Fatal(err)
			}
			if err := spool.collectionDirectory(instance, day); err != nil {
				t.Fatal(err)
			}
			// Model a crash after the durable witness but before payload removal.
			if err := installCollectedMarker(base); err != nil {
				t.Fatal(err)
			}
			if err := syncArchiveDir(base); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(base+suffix, []byte("collected bytes"), 0o640); err != nil {
				t.Fatal(err)
			}
			restarted := NewSpool(spool.root, 1<<20)
			unavailable := &unavailableCollectionCatalog{MemStore: state.NewMemStore()}
			client := &noCollectionUpload{}
			sh, err := NewShipper(Config{Regional: true}, restarted, client, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			sh.WithCatalog(unavailable, unavailable)
			if files := restarted.FilesSnapshot(); len(files) != 1 {
				t.Fatalf("interrupted payload omitted: %+v", files)
			}
			if n, bytes, err := sh.RunOnce(t.Context()); err != nil || n != 0 || bytes != 0 {
				t.Fatalf("resume: %d %d %v", n, bytes, err)
			}
			if len(restarted.FilesSnapshot()) != 0 || restarted.LocalBytes() != 0 || unavailable.calls != 0 || client.calls != 0 {
				t.Fatal("resume required catalog/upload or retained bytes")
			}
			if marked, err := collectedMarker(base); err != nil || !marked {
				t.Fatalf("durable fence removed: %v %v", marked, err)
			}
		})
	}
}

func TestLocalCollectionRejectsUnsafeFilesAndDamagedWitness(t *testing.T) {
	t.Run("symlink-directory", func(t *testing.T) {
		root, outside := t.TempDir(), t.TempDir()
		instance, day := "instance-symlink", "2026-06-01"
		if err := os.MkdirAll(filepath.Join(outside, "2026", "06"), 0o750); err != nil {
			t.Fatal(err)
		}
		payload := filepath.Join(outside, "2026", "06", spoolFilePrefix+day+spoolPartialSuffix)
		if err := os.WriteFile(payload, []byte("protected"), 0o640); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(root, instance)); err != nil {
			t.Fatal(err)
		}
		spool := NewSpool(root, 1<<20)
		if _, err := spool.discardCollectedDay(instance, day); err == nil {
			t.Fatal("cleanup followed symlink")
		}
		if raw, err := os.ReadFile(payload); err != nil || string(raw) != "protected" {
			t.Fatalf("outside bytes changed: %q %v", raw, err)
		}
		if _, err := os.Lstat(filepath.Join(outside, day[:4], day[5:7], spoolFilePrefix+day+spoolCollectedSuffix)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("outside marker installed: %v", err)
		}
	})
	t.Run("symlink-payload", func(t *testing.T) {
		spool := NewSpool(t.TempDir(), 1<<20)
		instance, day := "instance-link", "2026-06-01"
		if err := spool.collectionDirectory(instance, day); err != nil {
			t.Fatal(err)
		}
		base, err := spool.collectionBase(instance, day)
		if err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(t.TempDir(), "protected")
		if err := os.WriteFile(outside, []byte("protected"), 0o640); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, base+spoolPartialSuffix); err != nil {
			t.Fatal(err)
		}
		if _, err := spool.discardCollectedDay(instance, day); err == nil {
			t.Fatal("nonregular payload accepted")
		}
		if raw, err := os.ReadFile(outside); err != nil || string(raw) != "protected" {
			t.Fatalf("outside bytes changed: %q %v", raw, err)
		}
	})
	t.Run("damaged-marker", func(t *testing.T) {
		spool := NewSpool(t.TempDir(), 1<<20)
		at := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
		if _, err := spool.Write("instance-marker", 1, "stdout", at, "retain"); err != nil {
			t.Fatal(err)
		}
		if err := spool.CloseAll(); err != nil {
			t.Fatal(err)
		}
		base, err := spool.collectionBase("instance-marker", "2026-06-01")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(base+spoolCollectedSuffix, []byte("damaged"), 0o640); err != nil {
			t.Fatal(err)
		}
		client := &noCollectionUpload{}
		sh, err := NewShipper(Config{Bucket: "test"}, spool, client, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		file := spool.FilesSnapshot()[0]
		if _, err := sh.uploadFile(t.Context(), file); err == nil {
			t.Fatal("damaged witness authorized cleanup")
		}
		if len(spool.FilesSnapshot()) != 1 || spool.LocalBytes() == 0 || client.calls != 0 {
			t.Fatal("damaged witness lost payload")
		}
		if _, err := spool.Write(file.Instance, 2, "stdout", at, "late"); err == nil {
			t.Fatal("writer ignored damaged witness")
		}
	})
}

func TestLocalCollectionSerializesActiveWriter(t *testing.T) {
	spool := NewSpool(t.TempDir(), 1<<30)
	at := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	started, done := make(chan struct{}), make(chan error, 1)
	var once sync.Once
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	go func() {
		for seq := int64(1); ; seq++ {
			_, err := spool.Write("instance-race", seq, "stdout", at, "concurrent")
			once.Do(func() { close(started) })
			if err != nil {
				done <- err
				return
			}
			if ctx.Err() != nil {
				done <- ctx.Err()
				return
			}
		}
	}()
	<-started
	if _, err := spool.discardCollectedDay("instance-race", "2026-06-01"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrSpoolDayCollected) {
			t.Fatalf("writer not fenced: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := spool.CloseAll(); err != nil {
		t.Fatal(err)
	}
	if spool.LocalBytes() != 0 || len(spool.FilesSnapshot()) != 0 {
		t.Fatal("writer retained expired fd/buffer")
	}
}

type faultCollectionDelete struct{ fail bool }

func (d *faultCollectionDelete) Delete(context.Context, string) error {
	if d.fail {
		return errors.New("remote delete unavailable")
	}
	return nil
}

func TestLocalCollectionRetainsBacklogUntilRemoteAndMetadataRetryFinish(t *testing.T) {
	for _, operation := range []string{"remote", "metadata"} {
		t.Run(operation, func(t *testing.T) {
			store := state.NewMemStore()
			ref := retentionFixture(t, store, "eu-local-fault", "2026-06-01")
			fault := &retentionMetadataFault{MemStore: store, key: ref.StorageKey, fail: operation == "metadata"}
			remote := &faultCollectionDelete{fail: operation == "remote"}
			collector, err := NewRetentionCollector(RetentionParams{Store: fault, Archives: remote, Now: func() time.Time { return time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC) }})
			if err != nil {
				t.Fatal(err)
			}
			spool := NewSpool(t.TempDir(), 1<<20)
			at, _ := time.Parse("2006-01-02", ref.Day)
			if _, err := spool.Write(ref.InstanceID, 1, "stdout", at, "pending"); err != nil {
				t.Fatal(err)
			}
			client := &noCollectionUpload{}
			sh, err := NewShipper(Config{Regional: true}, spool, client, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			sh.WithCatalog(fault, fault)
			if result, err := collector.RunOnce(t.Context()); err == nil || result.Collected != 0 {
				t.Fatalf("faulted collection: %+v %v", result, err)
			}
			if n, _, err := sh.RunOnce(t.Context()); err != nil || n != 0 {
				t.Fatalf("faulted shipper: %d %v", n, err)
			}
			if len(spool.FilesSnapshot()) != 1 || spool.LocalBytes() == 0 {
				t.Fatal("failed collection discarded backlog")
			}
			remote.fail = false
			if result, err := collector.RunOnce(t.Context()); err != nil || result.Collected != 1 {
				t.Fatalf("retry: %+v %v", result, err)
			}
			if n, _, err := sh.RunOnce(t.Context()); err != nil || n != 0 {
				t.Fatalf("collected shipper: %d %v", n, err)
			}
			if len(spool.FilesSnapshot()) != 0 || spool.LocalBytes() != 0 || client.calls != 0 {
				t.Fatal("confirmed retry retained backlog or republished")
			}
		})
	}
}

func TestLocalCollectionCountsPartialProgressOnce(t *testing.T) {
	spool := NewSpool(t.TempDir(), 1<<20)
	at := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	instance, day := "instance-partial-cleanup", "2026-06-01"
	sealed, err := spool.Write(instance, 1, "stdout", at, "sealed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := spool.PrepareUpload(instance, day); err != nil {
		t.Fatal(err)
	}
	buffered, err := spool.Write(instance, 2, "stdout", at, "buffered")
	if err != nil {
		t.Fatal(err)
	}
	base, err := spool.collectionBase(instance, day)
	if err != nil {
		t.Fatal(err)
	}
	// A previously authorized witness survives a later filesystem failure.
	if err := installCollectedMarker(base); err != nil {
		t.Fatal(err)
	}
	if err := syncArchiveDir(base); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "protected")
	if err := os.WriteFile(outside, []byte("protected"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, base+spoolCommittedSuffix); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(base+archiveGzipSuffix, []byte("collected history"), 0o640); err != nil {
		t.Fatal(err)
	}
	registry := prometheus.NewRegistry()
	sh, err := NewShipper(Config{Regional: true}, spool, &noCollectionUpload{}, nil, NewMetrics(registry))
	if err != nil {
		t.Fatal(err)
	}
	file := FileInfo{Instance: instance, Day: day}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := sh.uploadFile(t.Context(), file); err == nil {
			t.Fatal("unsafe committed payload was accepted")
		}
		if spool.LocalBytes() != 0 {
			t.Fatal("partial cleanup did not release pending capacity")
		}
	}
	if raw, err := os.ReadFile(outside); err != nil || string(raw) != "protected" {
		t.Fatalf("outside payload changed: %q %v", raw, err)
	}
	// Operator fixes the unsafe entry; the remaining compressed history resumes.
	if err := os.Remove(base + spoolCommittedSuffix); err != nil {
		t.Fatal(err)
	}
	if len(spool.FilesSnapshot()) != 1 {
		t.Fatal("compressed recovery omitted")
	}
	if _, err := sh.uploadFile(t.Context(), file); err != nil {
		t.Fatal(err)
	}
	if len(spool.FilesSnapshot()) != 0 {
		t.Fatal("recovered payload remained")
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{"apid_log_archive_local_collected_files_total": 3, "apid_log_archive_local_collected_pending_bytes_total": float64(sealed + buffered)}
	for _, family := range families {
		if expected, ok := want[family.GetName()]; ok {
			if family.Metric[0].Counter.GetValue() != expected {
				t.Fatalf("metric %s=%v want %v", family.GetName(), family.Metric[0].Counter.GetValue(), expected)
			}
			delete(want, family.GetName())
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing metrics: %v", want)
	}
}

func TestLocalCollectionAfterOwnerSoftDeletion(t *testing.T) {
	for _, owner := range []string{"app", "account"} {
		t.Run(owner, func(t *testing.T) {
			store := state.NewMemStore()
			ref := retentionFixture(t, store, "eu-local-inactive", "2026-06-01")
			if err := store.RetireRuntimeLogArchive(t.Context(), ref); err != nil {
				t.Fatal(err)
			}
			if err := store.DeleteCollectedRuntimeLogArchive(t.Context(), ref); err != nil {
				t.Fatal(err)
			}
			if owner == "app" {
				if err := store.DeleteApp(t.Context(), ref.AppID); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := store.MarkAccountDeletionPending(t.Context(), ref.AccountID); err != nil {
					t.Fatal(err)
				}
			}
			spool := NewSpool(t.TempDir(), 1<<20)
			at, _ := time.Parse("2006-01-02", ref.Day)
			if _, err := spool.Write(ref.InstanceID, 1, "stdout", at, "collected delayed frame"); err != nil {
				t.Fatal(err)
			}
			client := &noCollectionUpload{}
			sh, err := NewShipper(Config{Regional: true}, spool, client, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			sh.WithCatalog(store, store)
			if n, _, err := sh.RunOnce(t.Context()); err != nil || n != 0 {
				t.Fatalf("inactive cleanup: %d %v", n, err)
			}
			if len(spool.FilesSnapshot()) != 0 || spool.LocalBytes() != 0 || client.calls != 0 {
				t.Fatal("inactive owner hid confirmed collection")
			}
		})
	}
}
