package objectnode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectchecksum"
	"github.com/onebox-faas/faas/pkg/objectinventory"
)

func inventoryPage(t *testing.T, s *Store, kind, cursor string) objectinventory.Page {
	t.Helper()
	p, err := s.Inventory(t.Context(), "test-bucket", InventoryRequest{Kind: kind, Cursor: cursor, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestInventoryUsageLifecycleAndRestart(t *testing.T) {
	s := testStore(t)
	putText(t, s, "object", "old")
	putText(t, s, "object", "new data")
	putText(t, s, "zero", "")
	u := newMultipart(t, s, objectchecksum.Policy{})
	multipartPart(t, s, u, 1, []byte("old part"))
	part := multipartPart(t, s, u, 1, []byte("part"))
	usage, err := s.Usage(t.Context())
	if err != nil || !usage.CatalogHealthy || usage.Objects != 2 || usage.CommittedBytes != 8 || usage.MultipartBytes != 4 || usage.MultipartParts != 1 || usage.MultipartUploads != 1 || usage.DataBytes != 12 || usage.UnreferencedBytes != 0 || usage.MetadataBytes < 1 || usage.FileBytes != usage.DataBytes+usage.MetadataBytes+usage.StagingBytes {
		t.Fatal(usage, err)
	}
	first := inventoryPage(t, s, objectinventory.Objects, "")
	second := inventoryPage(t, s, objectinventory.Objects, first.NextCursor)
	if len(first.Objects) != 1 || first.Objects[0].Key != "object" || first.NextCursor == "" || len(second.Objects) != 1 || second.Objects[0].Key != "zero" || second.NextCursor != "" || first.Snapshot != second.Snapshot || !first.ObservedAt.Equal(second.ObservedAt) {
		t.Fatal(first, second)
	}
	uploads := inventoryPage(t, s, objectinventory.Uploads, "")
	if len(uploads.Uploads) != 1 || uploads.Uploads[0].ID != u.ID || uploads.Uploads[0].Size != 4 {
		t.Fatal(uploads)
	}
	buckets, err := s.Inventory(t.Context(), "", InventoryRequest{Kind: objectinventory.Buckets, Limit: 1})
	if err != nil || len(buckets.Buckets) != 1 || buckets.Buckets[0].Generation != first.Generation {
		t.Fatal(buckets, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err = reopened.Inventory(t.Context(), "test-bucket", InventoryRequest{Kind: objectinventory.Objects, Cursor: first.NextCursor, Limit: 1}); !errors.Is(err, ErrInventoryChanged) {
		t.Fatal("old process cursor survived restart", err)
	}
	usage, err = reopened.Usage(t.Context())
	if err != nil || !usage.CatalogHealthy || usage.CommittedBytes != 8 || usage.MultipartBytes != 4 {
		t.Fatal(usage, err)
	}
	if _, err = reopened.CompleteMultipart(t.Context(), u.Bucket, u.Key, u.ID, completion(part), nil); err != nil {
		t.Fatal(err)
	}
	usage, err = reopened.Usage(t.Context())
	if err != nil || usage.Objects != 3 || usage.CommittedBytes != 12 || usage.MultipartBytes != 0 || usage.MultipartUploads != 0 || usage.Receipts != 1 || usage.ReceiptBytes < 1 || usage.Sessions != 1 {
		t.Fatal(usage, err)
	}
	if err = reopened.Delete(u.Bucket, u.Key); err != nil {
		t.Fatal(err)
	}
	aborted := newMultipart(t, reopened, objectchecksum.Policy{})
	multipartPart(t, reopened, aborted, 1, []byte("discard"))
	if err = reopened.AbortMultipart(aborted.Bucket, aborted.Key, aborted.ID); err != nil {
		t.Fatal(err)
	}
	usage, err = reopened.Usage(t.Context())
	if err != nil || usage.CommittedBytes != 8 || usage.MultipartBytes != 0 || usage.DataBytes != 8 || usage.Receipts != 1 || usage.Sessions != 2 {
		t.Fatal(usage, err)
	}
	if _, err = reopened.MultipartReceipt(u.Bucket, u.Key, u.ReceiptID); err != nil {
		t.Fatal("inventory or abort removed completion evidence", err)
	}
}

func TestInventoryMutationInvalidatesContinuations(t *testing.T) {
	for _, operation := range []string{"put", "tracked put", "delete", "create upload", "part", "abort", "expire", "complete", "create bucket", "delete bucket", "uncertain publication"} {
		t.Run(operation, func(t *testing.T) {
			s := testStore(t)
			putText(t, s, "a", "a")
			putText(t, s, "z", "z")
			if err := s.CreateBucket("other-bucket"); err != nil {
				t.Fatal(err)
			}
			u := newMultipart(t, s, objectchecksum.Policy{})
			part := multipartPart(t, s, u, 1, []byte("part"))
			first := inventoryPage(t, s, objectinventory.Objects, "")
			var err error
			switch operation {
			case "put":
				putText(t, s, "b", "new")
			case "tracked put":
				_, err = s.Put(t.Context(), u.Bucket, "b", strings.NewReader("new"), 3, PutOptions{Receipt: uuid.NewString()})
			case "delete":
				err = s.Delete(u.Bucket, "z")
			case "create upload":
				_, err = s.CreateMultipart(u.Bucket, "b", MultipartOptions{})
			case "part":
				multipartPart(t, s, u, 1, []byte("changed"))
			case "abort":
				err = s.AbortMultipart(u.Bucket, u.Key, u.ID)
			case "expire":
				_, err = s.ExpireMultipart(t.Context(), time.Now().Add(api.ObjectMultipartUploadTTL+time.Second))
			case "complete":
				_, err = s.CompleteMultipart(t.Context(), u.Bucket, u.Key, u.ID, completion(part), nil)
			case "create bucket":
				err = s.CreateBucket("new-bucket")
			case "delete bucket":
				err = s.DeleteBucket("other-bucket")
			case "uncertain publication":
				s.persist = func(path string, value any) (bool, error) {
					published, err := writeJSON(path, value)
					if err != nil {
						return published, err
					}
					return published, errors.New("injected directory fsync failure")
				}
				if _, err = s.Put(t.Context(), u.Bucket, "b", strings.NewReader("new"), 3, PutOptions{}); err == nil {
					t.Fatal("fault did not trigger")
				}
				err = nil
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.Inventory(t.Context(), u.Bucket, InventoryRequest{Kind: objectinventory.Objects, Cursor: first.NextCursor, Limit: 1}); !errors.Is(err, ErrInventoryChanged) {
				t.Fatal("mixed catalog revisions were accepted", err)
			}
		})
	}
}

func TestUsageAuditReportsDamageAndRetainedBytesWithoutDeleting(t *testing.T) {
	s := testStore(t)
	o := putText(t, s, "key", "stored")
	missing := filepath.Join(s.objectPath("test-bucket", o.Key), o.ID+".data")
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(s.objectPath("test-bucket", o.Key), uuid.NewString()+".data")
	stage := filepath.Join(s.root, "staging", uuid.NewString())
	for path, contents := range map[string]string{orphan: "leftover", stage: "stream"} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(s.root, "staging", "link")
	if err := os.Symlink(orphan, link); err != nil {
		t.Fatal(err)
	}
	u, err := s.Usage(t.Context())
	if err != nil || u.CatalogHealthy || u.CommittedBytes != 6 || u.Objects != 1 || u.UnreferencedBytes != 8 || u.UnreferencedBlobs != 1 || u.StagingBytes != 6 || u.Errors != 2 || u.Warnings != 1 || !u.PhysicalSample {
		t.Fatal(u, err)
	}
	if _, err = s.Inventory(t.Context(), "test-bucket", InventoryRequest{Kind: objectinventory.Objects, Limit: 1}); !errors.Is(err, ErrCorrupt) {
		t.Fatal("missing referenced bytes became usage proof", err)
	}
	for _, path := range []string{orphan, stage, link} {
		if _, err = os.Lstat(path); err != nil {
			t.Fatal("audit deleted diagnostic evidence", path, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if u, err = s.Usage(ctx); !errors.Is(err, context.Canceled) || !u.FinishedAt.IsZero() {
		t.Fatal("canceled audit published partial totals", u, err)
	}
}

func TestUsageAuditBoundsDiagnostics(t *testing.T) {
	s := testStore(t)
	o := putText(t, s, "key", "valid")
	for range api.ObjectNodeInventoryMaxIssues + 1 {
		if err := os.WriteFile(filepath.Join(s.objectPath("test-bucket", o.Key), uuid.NewString()+".data"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	u, err := s.Usage(t.Context())
	if err != nil || !u.CatalogHealthy || !u.IssuesTruncated || len(u.Issues) != api.ObjectNodeInventoryMaxIssues || u.UnreferencedBlobs != api.ObjectNodeInventoryMaxIssues+1 {
		t.Fatal(u, err)
	}
}

func TestUsageAuditPreservesKnownMissingPartBytes(t *testing.T) {
	s := testStore(t)
	u := newMultipart(t, s, objectchecksum.Policy{})
	p := multipartPart(t, s, u, 1, []byte("part"))
	if err := os.Remove(filepath.Join(s.partPath(u.ID, 1), p.Object.ID+".data")); err != nil {
		t.Fatal(err)
	}
	usage, err := s.Usage(t.Context())
	if err != nil || usage.CatalogHealthy || usage.MultipartBytes != 4 || usage.MultipartParts != 1 || usage.MultipartUploads != 1 {
		t.Fatal("missing part became a measured zero", usage, err)
	}
	if _, err = s.Inventory(t.Context(), u.Bucket, InventoryRequest{Kind: objectinventory.Uploads, Limit: 1}); !errors.Is(err, ErrCorrupt) {
		t.Fatal("damaged upload became a valid inventory page", err)
	}
}

func TestInventoryCursorScopesAndBucketIncarnation(t *testing.T) {
	s := testStore(t)
	putText(t, s, "a", "a")
	putText(t, s, "z", "z")
	p := inventoryPage(t, s, objectinventory.Objects, "")
	if _, err := s.Inventory(t.Context(), "test-bucket", InventoryRequest{Kind: objectinventory.Uploads, Cursor: p.NextCursor, Limit: 1}); !errors.Is(err, ErrInvalid) {
		t.Fatal("cursor crossed inventory kinds", err)
	}
	if err := s.Delete("test-bucket", "a"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("test-bucket", "z"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Inventory(t.Context(), "test-bucket", InventoryRequest{Kind: objectinventory.Objects, Cursor: p.NextCursor, Limit: 1}); !errors.Is(err, ErrInventoryChanged) {
		t.Fatal("cursor crossed bucket incarnations", err)
	}
}
