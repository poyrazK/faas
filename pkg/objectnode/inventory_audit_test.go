package objectnode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectchecksum"
	"github.com/onebox-faas/faas/pkg/objectinventory"
	bolt "go.etcd.io/bbolt"
)

func finishIncrementalAudit(t *testing.T, s *Store, options InventoryAuditOptions) InventoryAuditStatus {
	t.Helper()
	for range 100_000 {
		status, err := s.AuditStep(t.Context(), options)
		if err != nil {
			t.Fatal(status, err)
		}
		if status.StepRecords > options.MaxRecords {
			t.Fatal("audit exceeded its work budget", status.StepRecords)
		}
		if status.State == "complete" {
			return status
		}
	}
	t.Fatal("audit failed to finish without catalog mutations")
	return InventoryAuditStatus{}
}

func TestIncrementalAuditLargeCatalogMatchesSourceAndBoundsWork(t *testing.T) {
	s := testStore(t)
	for i := range 1025 {
		putText(t, s, fmt.Sprintf("key-%04d", i), "abc")
	}
	u := newMultipart(t, s, objectchecksum.Policy{})
	for n := int32(1); n <= 129; n++ {
		multipartPart(t, s, u, n, []byte("p"))
	}
	terminal := newMultipart(t, s, objectchecksum.Policy{})
	part := multipartPart(t, s, terminal, 1, []byte("done"))
	if _, err := s.CompleteMultipart(t.Context(), terminal.Bucket, terminal.Key, terminal.ID, completion(part), nil); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateBucket("empty-bucket"); err != nil {
		t.Fatal(err)
	}
	want, err := s.Usage(t.Context())
	if err != nil || !want.CatalogHealthy {
		t.Fatal(want, err)
	}
	options := InventoryAuditOptions{MaxRecords: 7, StepTime: api.ObjectNodeAuditMaxStepTime}
	status := finishIncrementalAudit(t, s, options)
	got := status.Usage
	if !got.CatalogHealthy || got.Buckets != want.Buckets || got.Objects != want.Objects || got.CommittedBytes != want.CommittedBytes || got.MultipartUploads != want.MultipartUploads || got.MultipartParts != want.MultipartParts || got.MultipartBytes != want.MultipartBytes || got.Sessions != want.Sessions || got.Receipts != want.Receipts || got.DataBytes != want.DataBytes || got.Errors != want.Errors || got.Warnings != want.Warnings {
		t.Fatal("incremental audit disagrees with complete source inventory", got, want)
	}
	if status.Steps < 100 || status.Restarts != 0 || status.Cycles != 1 || !status.LastHealthyAt.Equal(got.StartedAt) || !s.usageAudit.Load().At.Equal(got.StartedAt) {
		t.Fatal("audit did not checkpoint bounded coverage", status)
	}
	t.Logf("1025 initial objects, 129 active parts: %d checkpointed turns, each at most 7 work units", status.Steps)
}

func TestIncrementalAuditResumesCheckpointAfterCancellation(t *testing.T) {
	s := testStore(t)
	for i := range 20 {
		putText(t, s, fmt.Sprintf("key-%02d", i), "abc")
	}
	options := InventoryAuditOptions{MaxRecords: 1, StepTime: api.ObjectNodeAuditMaxStepTime}
	for range 12 {
		if _, err := s.AuditStep(t.Context(), options); err != nil {
			t.Fatal(err)
		}
	}
	proof, health, before := s.auditProof, s.usageAudit.Load(), s.InventoryAuditStatus()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.AuditStep(ctx, options); !errors.Is(err, context.Canceled) || s.auditProof != proof || s.usageAudit.Load() != health {
		t.Fatal("cancellation changed committed coverage or health", err)
	}
	// Every call reloads its durable phase, cookie, buffered names and totals;
	// there is no live directory iterator to silently retain worker progress.
	after, err := s.AuditStep(t.Context(), options)
	if err != nil || after.Steps != before.Steps+1 || after.Examined != before.Examined+1 || after.Restarts != 0 {
		t.Fatal("interrupted worker did not resume", before, after, err)
	}
	status := finishIncrementalAudit(t, s, options)
	if !status.Usage.CatalogHealthy || status.Usage.Objects != 20 {
		t.Fatal(status)
	}
}

func TestIncrementalAuditWritesPreserveCoverage(t *testing.T) {
	s := testStore(t)
	for i := range 100 {
		putText(t, s, fmt.Sprintf("key-%03d", i), "abc")
	}
	options := InventoryAuditOptions{MaxRecords: 1, StepTime: api.ObjectNodeAuditMaxStepTime}
	old := &usageAuditState{At: time.Now().Add(-time.Hour), Healthy: true}
	s.usageAudit.Store(old)
	for i := range 32 {
		before := s.inventorySnapshot()
		status, err := s.AuditStep(t.Context(), options)
		if err != nil || status.State != "running" || status.Usage.Snapshot != before && i == 0 || status.StepRecords > 1 || s.usageAudit.Load() != old {
			t.Fatal("partial or mixed coverage refreshed health", status, err)
		}
		putText(t, s, "hot-key", fmt.Sprint(i))
		if _, err = s.BucketUsageCounters(t.Context(), "test-bucket"); !errors.Is(err, ErrUsageAudit) {
			t.Fatal("ongoing work extended stale evidence", err)
		}
	}
	status := finishIncrementalAudit(t, s, InventoryAuditOptions{MaxRecords: 17, StepTime: api.ObjectNodeAuditMaxStepTime})
	if !status.Usage.CatalogHealthy || status.Usage.Objects != 101 || status.Restarts != 0 || readText(t, s, "hot-key") != "31" {
		t.Fatal("writes or reconciled audit lost progress", status)
	}
	if _, err := s.BucketUsageCounters(t.Context(), "test-bucket"); err != nil {
		t.Fatal("quiet complete coverage did not restore freshness", err)
	}
}

func TestIncrementalAuditConcurrentPublication(t *testing.T) {
	s := testStore(t)
	for i := range 64 {
		putText(t, s, fmt.Sprintf("key-%03d", i), "abc")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		options := InventoryAuditOptions{MaxRecords: 3, StepTime: api.ObjectNodeAuditMaxStepTime}
		for ctx.Err() == nil {
			if _, err := s.AuditStep(ctx, options); err != nil && !errors.Is(err, context.Canceled) {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for i := range 64 {
		putText(t, s, "hot", fmt.Sprint(i))
		if err := s.Delete("test-bucket", fmt.Sprintf("key-%03d", i)); err != nil {
			t.Fatal(err)
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	status := finishIncrementalAudit(t, s, DefaultInventoryAuditOptions())
	if !status.Usage.CatalogHealthy || status.Usage.Objects != 1 || status.Usage.CommittedBytes != 2 {
		t.Fatal(status)
	}
}

func TestIncrementalAuditSourceDamageWithdrawsHealthBeforeCompletion(t *testing.T) {
	for _, kind := range []string{"object", "part", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			s := testStore(t)
			o := putText(t, s, "key", "data")
			path := filepath.Join(s.objectPath("test-bucket", "key"), o.ID+".data")
			if kind == "part" {
				u := newMultipart(t, s, objectchecksum.Policy{})
				part := multipartPart(t, s, u, 1, []byte("data"))
				path = filepath.Join(s.partPath(u.ID, 1), part.Object.ID+".data")
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if kind == "symlink" {
				outside := filepath.Join(t.TempDir(), "outside")
				if err := os.WriteFile(outside, []byte("data"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, path); err != nil {
					t.Fatal(err)
				}
			}
			options := InventoryAuditOptions{MaxRecords: 1, StepTime: api.ObjectNodeAuditMaxStepTime}
			for range 1000 {
				status, err := s.AuditStep(t.Context(), options)
				if err != nil {
					t.Fatal(err)
				}
				if status.Usage.Errors > 0 {
					if status.State == "complete" || s.usageAudit.Load().Healthy {
						t.Fatal("source damage waited until completion to withdraw health", status)
					}
					break
				}
			}
			if _, err := s.BucketUsageCounters(t.Context(), "test-bucket"); !errors.Is(err, ErrUsageAudit) {
				t.Fatal("damaged references became valid capacity evidence", err)
			}
			status := finishIncrementalAudit(t, s, options)
			if status.Usage.CatalogHealthy || status.Usage.CommittedBytes != 4 {
				t.Fatal("damage became zero usage", status)
			}
			if kind == "symlink" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
				t.Fatal(err)
			}
			if status = finishIncrementalAudit(t, s, options); !status.Usage.CatalogHealthy || !s.usageAudit.Load().Healthy {
				t.Fatal("complete repaired coverage failed to restore health", status)
			}
		})
	}
}

func TestIncrementalAuditProjectionDriftLatchesIndex(t *testing.T) {
	for _, kind := range []string{"bucket", "global", "object", "part", "member", "extra object", "extra part", "schema"} {
		t.Run(kind, func(t *testing.T) {
			s := testStore(t)
			o := putText(t, s, "key", "data")
			u := newMultipart(t, s, objectchecksum.Policy{})
			part := multipartPart(t, s, u, 1, []byte("part"))
			b, _ := s.bucket("test-bucket")
			scope := catalogScope(nameHash(b.Name), b.Generation)
			if err := s.catalogIndex.db.Update(func(tx *bolt.Tx) error {
				switch kind {
				case "bucket", "global":
					key := scope
					if kind == "global" {
						key = usageGlobalKey
					}
					r, err := readUsage(tx, key)
					if err != nil {
						return err
					}
					r.CommittedBytes++
					return writeUsage(tx, r)
				case "object", "part", "extra object", "extra part":
					bucket, key, id := usageObjectsBucket, scope+nameHash(o.Key), o.ID
					if kind == "part" || kind == "extra part" {
						bucket, key, id = usagePartsBucket, u.ID+":"+fmtSequence(1), part.Object.ID
					}
					if kind == "extra object" {
						key = scope + nameHash("missing")
					}
					if kind == "extra part" {
						key = u.ID + ":" + fmtSequence(2)
					}
					body, _ := json.Marshal(sealUsageObject(usageObject{ID: id, Size: 99}))
					return tx.Bucket([]byte(bucket)).Put([]byte(key), body)
				case "member":
					return tx.Bucket([]byte(usageMembersBucket)).Bucket([]byte(scope)).Delete([]byte(u.ID))
				case "schema":
					return tx.DeleteBucket([]byte(usageObjectsBucket))
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			for range 1000 {
				status, err := s.AuditStep(t.Context(), InventoryAuditOptions{MaxRecords: 1, StepTime: api.ObjectNodeAuditMaxStepTime})
				if errors.Is(err, ErrCatalogIndex) {
					break
				}
				if err != nil || status.State == "complete" && status.Usage.CatalogHealthy {
					t.Fatal("drift became healthy evidence", status, err)
				}
			}
			if _, err := s.BucketUsageCounters(t.Context(), "test-bucket"); !errors.Is(err, ErrCatalogIndex) {
				t.Fatal("projection drift did not latch", err)
			}
			putText(t, s, "after", "safe")
			if readText(t, s, "key") != "data" {
				t.Fatal("audit changed source data")
			}
		})
	}
}

func TestIncrementalAuditCheckpointFailureAndTamperingCannotRefreshHealth(t *testing.T) {
	s := testStore(t)
	putText(t, s, "key", "data")
	old := &usageAuditState{At: time.Now().Add(-time.Hour), Healthy: true}
	s.usageAudit.Store(old)
	failure := errors.New("checkpoint fsync failed")
	s.auditCheckpoint = func() error { return failure }
	if _, err := s.AuditStep(t.Context(), DefaultInventoryAuditOptions()); !errors.Is(err, failure) || s.usageAudit.Load() != old || s.auditProof != "" {
		t.Fatal("failed checkpoint refreshed health", err)
	}
	s.auditCheckpoint = nil
	if _, err := s.AuditStep(t.Context(), InventoryAuditOptions{MaxRecords: 1, StepTime: api.ObjectNodeAuditMaxStepTime}); err != nil {
		t.Fatal(err)
	}
	if err := s.catalogIndex.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(inventoryAuditBucket))
		var p inventoryAuditState
		if err := json.Unmarshal(b.Get([]byte("checkpoint")), &p); err != nil {
			return err
		}
		p.Status.Phase = "index-buckets"
		p.Seal = ""
		p.Seal = scrubSeal(p)
		body, _ := json.Marshal(p)
		return b.Put([]byte("checkpoint"), body)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuditStep(t.Context(), DefaultInventoryAuditOptions()); !errors.Is(err, ErrCorrupt) || s.usageAudit.Load() != old {
		t.Fatal("correctly sealed foreign checkpoint was accepted", err)
	}
}

func TestIncrementalAuditColdRestartDiscardsCoverage(t *testing.T) {
	s := testStore(t)
	putText(t, s, "key", "data")
	options := InventoryAuditOptions{MaxRecords: 1, StepTime: api.ObjectNodeAuditMaxStepTime}
	for range 8 {
		if _, err := s.AuditStep(t.Context(), options); err != nil {
			t.Fatal(err)
		}
	}
	before := s.InventoryAuditStatus()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	after, err := next.AuditStep(t.Context(), options)
	if err != nil || after.Steps != 1 || after.Usage.Snapshot == before.Usage.Snapshot || after.Usage.StartedAt.Before(before.Usage.StartedAt) {
		t.Fatal("source recovery reused old coverage", before, after, err)
	}
	if status := finishIncrementalAudit(t, next, options); !status.Usage.CatalogHealthy || status.Usage.CommittedBytes != 4 {
		t.Fatal(status)
	}
}

func TestIncrementalAuditOldCoverageCannotExtendFreshness(t *testing.T) {
	s := testStore(t)
	putText(t, s, "key", "data")
	if _, err := s.AuditStep(t.Context(), InventoryAuditOptions{MaxRecords: 1, StepTime: api.ObjectNodeAuditMaxStepTime}); err != nil {
		t.Fatal(err)
	}
	p, err := s.loadInventoryAudit()
	if err != nil {
		t.Fatal(err)
	}
	p.Status.Usage.StartedAt = time.Now().Add(-time.Duration(api.ObjectStorageInventoryMaxAgeSeconds+1) * time.Second)
	if err = s.saveInventoryAudit(p); err != nil {
		t.Fatal(err)
	}
	s.usageAudit.Store(&usageAuditState{At: p.Status.Usage.StartedAt.Add(-time.Minute), Healthy: true})
	status := finishIncrementalAudit(t, s, DefaultInventoryAuditOptions())
	if !status.Usage.CatalogHealthy || !s.usageAudit.Load().At.Equal(p.Status.Usage.StartedAt) {
		t.Fatal("coverage timestamp changed at completion", status)
	}
	if _, err = s.BucketUsageCounters(t.Context(), "test-bucket"); !errors.Is(err, ErrUsageAudit) {
		t.Fatal("late completion extended stale coverage", err)
	}
}

func TestIncrementalAuditDirectoryIdentityAndInventoryCursor(t *testing.T) {
	s := testStore(t)
	putText(t, s, "a", "a")
	putText(t, s, "z", "z")
	page := inventoryPage(t, s, objectinventory.Objects, "")
	options := InventoryAuditOptions{MaxRecords: 1, StepTime: api.ObjectNodeAuditMaxStepTime}
	if _, err := s.AuditStep(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	directory := auditDirectory{Path: "buckets"}
	if _, err := s.nextAuditName(&directory); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(s.root, "buckets")
	if err := os.Rename(root, root+"-saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.nextAuditName(&directory); !errors.Is(err, ErrInventoryChanged) {
		t.Fatal("directory cookie crossed a replaced directory", err)
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(root+"-saved", root); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Inventory(t.Context(), "test-bucket", InventoryRequest{Kind: objectinventory.Objects, Cursor: page.NextCursor, Limit: 1}); err != nil {
		t.Fatal("audit checkpoints invalidated a source inventory cursor", err)
	}
	finishIncrementalAudit(t, s, options)
}

func TestIncrementalAuditMutationKindsReconcileCoverage(t *testing.T) {
	for _, kind := range []string{"overwrite", "delete", "part replacement", "abort", "complete", "bucket generation"} {
		t.Run(kind, func(t *testing.T) {
			s := testStore(t)
			putText(t, s, "key", "data")
			u := newMultipart(t, s, objectchecksum.Policy{})
			part := multipartPart(t, s, u, 1, []byte("part"))
			if err := s.CreateBucket("other-bucket"); err != nil {
				t.Fatal(err)
			}
			options := InventoryAuditOptions{MaxRecords: 1, StepTime: api.ObjectNodeAuditMaxStepTime}
			before, err := s.AuditStep(t.Context(), options)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "overwrite":
				putText(t, s, "key", "new")
			case "delete":
				err = s.Delete("test-bucket", "key")
			case "part replacement":
				multipartPart(t, s, u, 1, []byte("replacement"))
			case "abort":
				err = s.AbortMultipart(u.Bucket, u.Key, u.ID)
			case "complete":
				_, err = s.CompleteMultipart(t.Context(), u.Bucket, u.Key, u.ID, completion(part), nil)
			case "bucket generation":
				if err = s.DeleteBucket("other-bucket"); err == nil {
					err = s.CreateBucket("other-bucket")
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			after, err := s.AuditStep(t.Context(), options)
			if err != nil || after.Restarts != 0 || !after.Usage.StartedAt.Equal(before.Usage.StartedAt) {
				t.Fatal("mutation discarded independent coverage", before, after, err)
			}
			if result := finishIncrementalAudit(t, s, DefaultInventoryAuditOptions()); !result.Usage.CatalogHealthy {
				t.Fatal(result)
			}
			assertUsageCountersMatchSource(t, s)
		})
	}
}

func TestIncrementalAuditReadinessAndBudgets(t *testing.T) {
	for _, options := range []InventoryAuditOptions{{MaxRecords: 1, StepTime: time.Millisecond, MaxPending: -1}, {MaxRecords: 1, StepTime: time.Millisecond, MaxPending: api.ObjectNodeAuditMaxPendingRecords + 1}, {}, {MaxRecords: -1, StepTime: time.Millisecond}, {MaxRecords: api.ObjectNodeAuditMaxRecords + 1, StepTime: time.Millisecond}, {MaxRecords: 1, StepTime: api.ObjectNodeAuditMaxStepTime + 1}} {
		s := testStore(t)
		if _, err := s.AuditStep(t.Context(), options); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid audit budget accepted", options, err)
		}
	}
	for _, kind := range []string{"pending", "replication pending", "follower", "fenced", "index unavailable", "closed"} {
		t.Run(kind, func(t *testing.T) {
			s := testStore(t)
			health := s.usageAudit.Load()
			want := ErrCatalogBusy
			switch kind {
			case "pending":
				s.pending = &writeReceipt{}
			case "replication pending":
				s.replicationDirty = true
			case "follower":
				s.replication = &replicationState{}
				s.replication.Role = "follower"
				want = ErrReplica
			case "fenced":
				s.authorityDirty = true
				want = ErrFenced
			case "index unavailable":
				s.catalogIndex.ready = false
				want = ErrCatalogIndex
			case "closed":
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				want = ErrClosed
			}
			if status, err := s.AuditStep(t.Context(), DefaultInventoryAuditOptions()); !errors.Is(err, want) || status.Cycles != 0 || s.usageAudit.Load() != health {
				t.Fatal("non-serving source established coverage", status, err)
			}
		})
	}
}

func TestIncrementalAuditBoundsDiagnostics(t *testing.T) {
	s := testStore(t)
	for range api.ObjectNodeInventoryMaxIssues + 1 {
		if err := os.WriteFile(filepath.Join(s.root, "trash", uuid.NewString()+".data"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	status := finishIncrementalAudit(t, s, DefaultInventoryAuditOptions())
	if !status.Usage.CatalogHealthy || !status.Usage.IssuesTruncated || len(status.Usage.Issues) != api.ObjectNodeInventoryMaxIssues || status.Usage.UnreferencedBlobs != api.ObjectNodeInventoryMaxIssues+1 {
		t.Fatal("incremental diagnostics lost or exceeded their bound", status)
	}
}

func TestIncrementalAuditTracksAppliedJournalMutation(t *testing.T) {
	s, _ := replicationPair(t)
	u := newMultipart(t, s, objectchecksum.Policy{})
	multipartPart(t, s, u, 1, []byte("part"))
	if err := s.AbortMultipart(u.Bucket, u.Key, u.ID); err != nil {
		t.Fatal(err)
	}
	options := InventoryAuditOptions{MaxRecords: 1, StepTime: api.ObjectNodeAuditMaxStepTime}
	before, err := s.AuditStep(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	// A journaled idempotent removal can advance the applied position without
	// passing through writeRecord's in-memory inventory-epoch invalidation.
	if _, err = s.journalRemove(filepath.ToSlash(filepath.Join("multipart", u.ID, "parts"))); err != nil {
		t.Fatal(err)
	}
	after, err := s.AuditStep(t.Context(), options)
	if err != nil || after.Restarts != 0 || !after.Usage.StartedAt.Equal(before.Usage.StartedAt) {
		t.Fatal("applied journal change discarded independent coverage", before, after, err)
	}
	if status := finishIncrementalAudit(t, s, DefaultInventoryAuditOptions()); !status.Usage.CatalogHealthy || status.Usage.MultipartUploads != 0 {
		t.Fatal(status)
	}
}

func TestIncrementalAuditCannotOverrideNewerFailedEvidence(t *testing.T) {
	s := testStore(t)
	putText(t, s, "key", "data")
	if _, err := s.AuditStep(t.Context(), InventoryAuditOptions{MaxRecords: 1, StepTime: api.ObjectNodeAuditMaxStepTime}); err != nil {
		t.Fatal(err)
	}
	s.usageAudit.Store(&usageAuditState{At: time.Now().UTC(), Healthy: false})
	status := finishIncrementalAudit(t, s, DefaultInventoryAuditOptions())
	if status.Usage.CatalogHealthy || s.usageAudit.Load().Healthy {
		t.Fatal("older partial coverage overrode newer failed evidence", status)
	}
	status = finishIncrementalAudit(t, s, DefaultInventoryAuditOptions())
	if !status.Usage.CatalogHealthy || !s.usageAudit.Load().Healthy {
		t.Fatal("new complete coverage failed to restore health", status)
	}
}

func TestIncrementalAuditCancellationDuringCheckpointCannotPublish(t *testing.T) {
	s := testStore(t)
	putText(t, s, "key", "data")
	old := &usageAuditState{At: time.Now().Add(-time.Hour), Healthy: true}
	s.usageAudit.Store(old)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s.auditCheckpoint = func() error { cancel(); return nil }
	status, err := s.AuditStep(ctx, InventoryAuditOptions{MaxRecords: api.ObjectNodeAuditMaxRecords, StepTime: api.ObjectNodeAuditMaxStepTime})
	if !errors.Is(err, context.Canceled) || s.usageAudit.Load() != old || status.Cycles != 0 || !status.LastHealthyAt.IsZero() {
		t.Fatal("cancellation during checkpoint published an observation", status, err)
	}
	p, err := s.loadInventoryAudit()
	if err != nil || p == nil || p.Status.State != "complete" {
		t.Fatal("test did not exercise a completed checkpoint", p, err)
	}
	s.auditCheckpoint = nil
	status, err = s.AuditStep(t.Context(), InventoryAuditOptions{MaxRecords: 1, StepTime: api.ObjectNodeAuditMaxStepTime})
	if err != nil || status.State != "running" || status.Cycles != 0 || !status.LastHealthyAt.IsZero() || s.usageAudit.Load() != old {
		t.Fatal("next worker promoted canceled evidence", status, err)
	}
	status = finishIncrementalAudit(t, s, DefaultInventoryAuditOptions())
	if status.Cycles != 1 || !status.Usage.CatalogHealthy || !s.usageAudit.Load().Healthy {
		t.Fatal("new full cycle failed after canceled checkpoint", status)
	}
}
