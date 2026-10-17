package objectnode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectchecksum"
	"github.com/onebox-faas/faas/pkg/objectinventory"
	bolt "go.etcd.io/bbolt"
)

func assertUsageCounters(t *testing.T, s *Store, want objectinventory.Totals) {
	t.Helper()
	counters, err := s.BucketUsageCounters(t.Context(), "test-bucket")
	if err != nil || counters.Totals != want || counters.Generation == "" || counters.Snapshot == "" || counters.AuditedAt.IsZero() || counters.AuditedAt.After(counters.ObservedAt) {
		t.Fatal("invalid usage counters", counters, want, err)
	}
	audit, err := s.Usage(t.Context())
	if err != nil || !audit.CatalogHealthy {
		t.Fatal("independent audit disagrees with counters", audit, err)
	}
}

func TestUsageCountersObjectAndMultipartTransitions(t *testing.T) {
	s := testStore(t)
	want := objectinventory.Totals{}
	assertUsageCounters(t, s, want)
	putText(t, s, "item", "123")
	want.Objects, want.CommittedBytes = 1, 3
	assertUsageCounters(t, s, want)
	putText(t, s, "item", "abcde")
	want.CommittedBytes = 5
	assertUsageCounters(t, s, want)
	if _, err := receiptPut(t, s, "item", "rejected", uuid.NewString(), PutOptions{IfNoneMatch: "*"}); !errors.Is(err, ErrPrecondition) {
		t.Fatal(err)
	}
	assertUsageCounters(t, s, want)
	if _, err := receiptPut(t, s, "tracked", "proof", uuid.NewString(), PutOptions{}); err != nil {
		t.Fatal(err)
	}
	want.Objects, want.CommittedBytes = 2, 10
	assertUsageCounters(t, s, want)
	if err := s.Delete("test-bucket", "item"); err != nil {
		t.Fatal(err)
	}
	want.Objects, want.CommittedBytes = 1, 5
	assertUsageCounters(t, s, want)
	u := newMultipart(t, s, objectchecksum.Policy{})
	want.MultipartUploads = 1
	assertUsageCounters(t, s, want)
	multipartPart(t, s, u, 1, []byte("123"))
	want.MultipartParts, want.MultipartBytes = 1, 3
	assertUsageCounters(t, s, want)
	multipartPart(t, s, u, 1, []byte("12"))
	want.MultipartBytes = 2
	assertUsageCounters(t, s, want)
	multipartPart(t, s, u, 2, []byte("four"))
	want.MultipartParts, want.MultipartBytes = 2, 6
	assertUsageCounters(t, s, want)
	if err := s.AbortMultipart(u.Bucket, u.Key, u.ID); err != nil {
		t.Fatal(err)
	}
	want.MultipartUploads, want.MultipartParts, want.MultipartBytes = 0, 0, 0
	assertUsageCounters(t, s, want)
	u = newMultipart(t, s, objectchecksum.Policy{})
	part := multipartPart(t, s, u, 1, []byte("data"))
	if _, err := s.CompleteMultipart(t.Context(), u.Bucket, u.Key, u.ID, completion(part), nil); err != nil {
		t.Fatal(err)
	}
	want.Objects, want.CommittedBytes = 2, 9
	assertUsageCounters(t, s, want)
	u = newMultipart(t, s, objectchecksum.Policy{})
	part = multipartPart(t, s, u, 1, []byte("unused"))
	request := completion(part)
	request.IfNoneMatch = "*"
	if _, err := s.CompleteMultipart(t.Context(), u.Bucket, u.Key, u.ID, request, nil); !errors.Is(err, ErrPrecondition) {
		t.Fatal(err)
	}
	assertUsageCounters(t, s, want)
	u = newMultipart(t, s, objectchecksum.Policy{})
	multipartPart(t, s, u, 1, []byte("expired"))
	if n, err := s.ExpireMultipart(t.Context(), u.ExpiresAt.Add(time.Second)); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	assertUsageCounters(t, s, want)
}

func TestUsageCountersRebuildLargeCatalogAndReadWithoutScanning(t *testing.T) {
	s := testStore(t)
	for i := range 2048 {
		putText(t, s, fmt.Sprintf("key-%04d", i), "abc")
	}
	u := newMultipart(t, s, objectchecksum.Policy{})
	for i := int32(1); i <= api.ObjectNodeCatalogIndexBuildBatch+1; i++ {
		multipartPart(t, s, u, i, []byte("p"))
	}
	want := objectinventory.Totals{Objects: 2048, CommittedBytes: 6144, MultipartUploads: 1, MultipartParts: 129, MultipartBytes: 129}
	assertUsageCounters(t, s, want)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.root, catalogIndexName), []byte("partial index"), 0o600); err != nil {
		t.Fatal(err)
	}
	next, err := Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	assertUsageCounters(t, next, want)
	// Counter reads use one bucket record and point reads of the projection.
	// Filesystem verification is deliberately the separate, freshness-gated audit.
	for _, dir := range []string{filepath.Join(next.bucketPath("test-bucket"), "objects"), filepath.Join(next.root, "multipart")} {
		if err = os.Rename(dir, dir+"-hidden"); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Rename(dir+"-hidden", dir) }()
	}
	for range 20 {
		c, err := next.BucketUsageCounters(t.Context(), "test-bucket")
		if err != nil || c.Totals != want {
			t.Fatal("counter request scanned catalog directories", c, err)
		}
	}
	t.Log("2048 objects and 129 selected parts: twenty counter reads performed without catalog-directory access")
}

func TestUsageCountersAuditFreshnessAndPendingWork(t *testing.T) {
	s := testStore(t)
	putText(t, s, "key", "bytes")
	for _, audit := range []usageAuditState{{}, {At: time.Now(), Healthy: false}, {At: time.Now().Add(time.Hour), Healthy: true}, {At: time.Now().Add(-time.Duration(api.ObjectStorageInventoryMaxAgeSeconds+1) * time.Second), Healthy: true}} {
		s.usageAudit.Store(&audit)
		if _, err := s.BucketUsageCounters(t.Context(), "test-bucket"); !errors.Is(err, ErrUsageAudit) {
			t.Fatal("unhealthy or stale audit accepted", audit, err)
		}
	}
	before := s.usageAudit.Load()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.Usage(ctx); !errors.Is(err, context.Canceled) || s.usageAudit.Load() != before {
		t.Fatal("incomplete audit refreshed readiness", err)
	}
	assertUsageAuditHealthy(t, s)
	s.pending = &writeReceipt{}
	if _, err := s.BucketUsageCounters(t.Context(), "test-bucket"); !errors.Is(err, ErrCatalogBusy) {
		t.Fatal("pending write became complete usage", err)
	}
	s.pending = nil
	assertUsageCounters(t, s, objectinventory.Totals{Objects: 1, CommittedBytes: 5})
}

func assertUsageAuditHealthy(t *testing.T, s *Store) {
	t.Helper()
	if audit, err := s.Usage(t.Context()); err != nil || !audit.CatalogHealthy {
		t.Fatal(audit, err)
	}
}

func assertUsageCountersMatchSource(t *testing.T, s *Store) {
	t.Helper()
	audit, err := s.Usage(t.Context())
	if err != nil || !audit.CatalogHealthy {
		t.Fatal(audit, err)
	}
	want := objectinventory.Totals{Objects: audit.Objects, CommittedBytes: audit.CommittedBytes, MultipartUploads: audit.MultipartUploads, MultipartParts: audit.MultipartParts, MultipartBytes: audit.MultipartBytes}
	got, err := s.BucketUsageCounters(t.Context(), "test-bucket")
	if err != nil || got.Totals != want {
		t.Fatal("recovery counter projection disagrees with source", got, want, err)
	}
}

func TestUsageCountersDetectDriftAndRebuild(t *testing.T) {
	for _, fault := range []string{"bucket", "global", "object", "part", "member"} {
		t.Run(fault, func(t *testing.T) {
			s := testStore(t)
			o := putText(t, s, "object", "bytes")
			u := newMultipart(t, s, objectchecksum.Policy{})
			part := multipartPart(t, s, u, 1, []byte("part"))
			b, _ := s.bucket("test-bucket")
			scope := catalogScope(nameHash(b.Name), b.Generation)
			if err := s.catalogIndex.db.Update(func(tx *bolt.Tx) error {
				switch fault {
				case "bucket", "global":
					key := scope
					if fault == "global" {
						key = usageGlobalKey
					}
					r, err := readUsage(tx, key)
					if err != nil {
						return err
					}
					r.CommittedBytes++
					return writeUsage(tx, r) // Even a correctly sealed wrong value must be found.
				case "object", "part":
					bucket, key, id := usageObjectsBucket, scope+nameHash(o.Key), o.ID
					if fault == "part" {
						bucket, key, id = usagePartsBucket, u.ID+":"+fmtSequence(1), part.Object.ID
					}
					body, _ := json.Marshal(sealUsageObject(usageObject{ID: id, Size: 99}))
					return tx.Bucket([]byte(bucket)).Put([]byte(key), body)
				case "member":
					return tx.Bucket([]byte(usageMembersBucket)).Bucket([]byte(scope)).Delete([]byte(u.ID))
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			audit, err := s.Usage(t.Context())
			if err != nil || audit.CatalogHealthy {
				t.Fatal("usage drift passed the audit", audit, err)
			}
			found := false
			for _, issue := range audit.Issues {
				found = found || issue.Code == "usage_counter_drift"
			}
			if !found {
				t.Fatal("missing drift diagnostic", audit)
			}
			if _, err = s.BucketUsageCounters(t.Context(), "test-bucket"); !errors.Is(err, ErrCatalogIndex) {
				t.Fatal("drift became capacity evidence", err)
			}
			putText(t, s, "after-drift", "safe")
			if readText(t, s, "object") != "bytes" {
				t.Fatal("drift changed source data")
			}
			_ = s.Close()
			next, err := Open(s.root)
			if err != nil {
				t.Fatal(err)
			}
			defer next.Close()
			assertUsageCounters(t, next, objectinventory.Totals{Objects: 2, CommittedBytes: 9, MultipartUploads: 1, MultipartParts: 1, MultipartBytes: 4})
		})
	}
}

func TestUsageCountersConcurrentMutationsAndSnapshots(t *testing.T) {
	s := testStore(t)
	result := make(chan error, 1)
	go func() {
		for range 128 {
			if _, err := s.Put(t.Context(), "test-bucket", "changing", strings.NewReader("abc"), 3, PutOptions{}); err != nil {
				result <- err
				return
			}
			if err := s.Delete("test-bucket", "changing"); err != nil {
				result <- err
				return
			}
		}
		result <- nil
	}()
	for range 256 {
		c, err := s.BucketUsageCounters(t.Context(), "test-bucket")
		if err != nil || c.Objects < 0 || c.Objects > 1 || c.CommittedBytes != c.Objects*3 {
			t.Fatal("mixed mutation counters", c, err)
		}
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	assertUsageCounters(t, s, objectinventory.Totals{})
}

func TestUsageCountersSeparateBucketGenerations(t *testing.T) {
	s := testStore(t)
	if err := s.CreateBucket("second-bucket"); err != nil {
		t.Fatal(err)
	}
	putText(t, s, "one", "abc")
	if _, err := s.Put(t.Context(), "second-bucket", "two", strings.NewReader("four"), 4, PutOptions{}); err != nil {
		t.Fatal(err)
	}
	before, err := s.BucketUsageCounters(t.Context(), "test-bucket")
	if err != nil || before.CommittedBytes != 3 {
		t.Fatal(before, err)
	}
	if err = s.Delete("test-bucket", "one"); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	if err = s.CreateBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	after, err := s.BucketUsageCounters(t.Context(), "test-bucket")
	if err != nil || after.Generation == before.Generation || after.Totals != (objectinventory.Totals{}) {
		t.Fatal("old generation usage leaked", before, after, err)
	}
	second, err := s.BucketUsageCounters(t.Context(), "second-bucket")
	if err != nil || second.Objects != 1 || second.CommittedBytes != 4 {
		t.Fatal(second, err)
	}
	assertUsageAuditHealthy(t, s)
}

func TestUsageCounterProjectionFailurePreservesDurableMutation(t *testing.T) {
	for _, missing := range []string{"root", "scope"} {
		t.Run(missing, func(t *testing.T) {
			s := testStore(t)
			b, _ := s.bucket("test-bucket")
			if err := s.catalogIndex.db.Update(func(tx *bolt.Tx) error {
				if missing == "root" {
					return tx.DeleteBucket([]byte(usageObjectsBucket))
				}
				return tx.Bucket([]byte(usageMembersBucket)).DeleteBucket([]byte(catalogScope(nameHash(b.Name), b.Generation)))
			}); err != nil {
				t.Fatal(err)
			}
			if missing == "root" {
				putText(t, s, "durable", "bytes")
				if readText(t, s, "durable") != "bytes" {
					t.Fatal("projection failure changed source")
				}
			} else {
				if _, err := s.CreateMultipart("test-bucket", "key", MultipartOptions{}); err != nil {
					t.Fatal("projection failure failed durable session publication", err)
				}
			}
			if _, err := s.BucketUsageCounters(t.Context(), "test-bucket"); !errors.Is(err, ErrCatalogIndex) {
				t.Fatal("incomplete projection accepted", err)
			}
		})
	}
}

func TestUsageCountersRemainCoherentDuringAudit(t *testing.T) {
	s := testStore(t)
	putText(t, s, "key", "abc")
	done := make(chan error, 1)
	go func() {
		for range 16 {
			u, err := s.Usage(t.Context())
			if err != nil {
				done <- fmt.Errorf("usage audit: %w", err)
				return
			}
			if !u.CatalogHealthy {
				done <- errors.New("usage audit unhealthy")
				return
			}
		}
		done <- nil
	}()
	for range 512 {
		c, err := s.BucketUsageCounters(t.Context(), "test-bucket")
		if err != nil || c.Objects != 1 || c.CommittedBytes != 3 || c.AuditedAt.After(c.ObservedAt) {
			t.Fatal("concurrent audit produced an invalid observation", c, err)
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
