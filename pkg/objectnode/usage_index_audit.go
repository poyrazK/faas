package objectnode

import (
	"bytes"
	"context"
	"path/filepath"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectinventory"
	bolt "go.etcd.io/bbolt"
)

func (s *Store) auditUsageObject(scope, hash string, o Object) error {
	return s.catalogIndex.db.View(func(tx *bolt.Tx) error {
		row, exists, err := readUsageObject(tx.Bucket([]byte(usageObjectsBucket)), []byte(scope+hash))
		if err != nil || !exists || row.ID != o.ID || row.Size != o.Size {
			return ErrCatalogIndex
		}
		return nil
	})
}

func (s *Store) auditUsageBucket(ctx context.Context, scope string, objects, committed int64) error {
	return s.catalogIndex.db.View(func(tx *bolt.Tx) error {
		r, err := readUsage(tx, scope)
		if err != nil || r.Objects != objects || r.CommittedBytes != committed {
			return ErrCatalogIndex
		}
		var count, size int64
		cursor := tx.Bucket([]byte(usageObjectsBucket)).Cursor()
		for k, _ := cursor.Seek([]byte(scope)); k != nil && bytes.HasPrefix(k, []byte(scope)); k, _ = cursor.Next() {
			if err := ctx.Err(); err != nil {
				return err
			}
			o, exists, err := readUsageObject(tx.Bucket([]byte(usageObjectsBucket)), k)
			if err != nil || !exists || len(k) != len(scope)+64 || addInventoryBytes(&size, o.Size) != nil {
				return ErrCatalogIndex
			}
			count++
		}
		if count != r.Objects || size != r.CommittedBytes {
			return ErrCatalogIndex
		}
		members := tx.Bucket([]byte(usageMembersBucket)).Bucket([]byte(scope))
		if members == nil {
			return ErrCatalogIndex
		}
		var uploads, parts, partBytes int64
		if err = members.ForEach(func(k, v []byte) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			u, exists, err := readUsageUpload(tx, string(k))
			if err != nil || !exists || string(k) != string(v) || u.Scope != scope || addInventoryBytes(&partBytes, u.Bytes) != nil || addInventoryBytes(&parts, u.Parts) != nil {
				return ErrCatalogIndex
			}
			uploads++
			return nil
		}); err != nil {
			return err
		}
		active, err := catalogCount(tx, scope)
		if err != nil || uploads != r.MultipartUploads || parts != r.MultipartParts || partBytes != r.MultipartBytes || active != uint64(uploads) {
			return ErrCatalogIndex
		}
		return nil
	})
}

func (s *Store) auditUsagePart(u MultipartUpload, p MultipartPart) error {
	return s.catalogIndex.db.View(func(tx *bolt.Tx) error {
		key := []byte(u.ID + ":" + fmtSequence(int64(p.Number)))
		o, exists, err := readUsageObject(tx.Bucket([]byte(usagePartsBucket)), key)
		if err != nil || !exists || o.ID != p.Object.ID || o.Size != p.Object.Size {
			return ErrCatalogIndex
		}
		return nil
	})
}

func (s *Store) auditUsageUpload(ctx context.Context, upload MultipartUpload, parts, size int64) error {
	return s.catalogIndex.db.View(func(tx *bolt.Tx) error {
		u, exists, err := readUsageUpload(tx, upload.ID)
		if upload.Status != multipartActive && upload.Status != multipartCompleting {
			if exists || err != nil {
				return ErrCatalogIndex
			}
			return nil
		}
		scope := catalogScope(nameHash(upload.Bucket), upload.Generation)
		if err != nil || !exists || u.Scope != scope || u.Parts != parts || u.Bytes != size {
			return ErrCatalogIndex
		}
		var count, total int64
		b := tx.Bucket([]byte(usagePartsBucket))
		cursor := b.Cursor()
		prefix := []byte(upload.ID + ":")
		for k, _ := cursor.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = cursor.Next() {
			if err := ctx.Err(); err != nil {
				return err
			}
			n, err := strconv.ParseInt(string(k[len(prefix):]), 10, 64)
			o, exists, readErr := readUsageObject(b, k)
			if err != nil || n < 1 || n > api.MaxMultipartParts || string(k[len(prefix):]) != fmtSequence(n) || readErr != nil || !exists || addInventoryBytes(&total, o.Size) != nil {
				return ErrCatalogIndex
			}
			count++
		}
		if count != parts || total != size {
			return ErrCatalogIndex
		}
		return nil
	})
}

func (s *Store) auditUsageGlobal(ctx context.Context, u *objectinventory.Usage) error {
	return s.catalogIndex.db.View(func(tx *bolt.Tx) error {
		r, err := readUsage(tx, usageGlobalKey)
		want := objectinventory.Totals{Objects: u.Objects, CommittedBytes: u.CommittedBytes, MultipartUploads: u.MultipartUploads, MultipartParts: u.MultipartParts, MultipartBytes: u.MultipartBytes}
		if err != nil || r.Totals != want {
			return ErrCatalogIndex
		}
		var buckets int64
		if err = tx.Bucket([]byte(catalogBuckets)).ForEach(func(k, v []byte) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !validScrubFindingID(string(k)) || !validReceiptID(string(v)) {
				return ErrCatalogIndex
			}
			var b bucketRecord
			if readJSON(filepath.Join(s.root, "buckets", string(k), "bucket.json"), &b) != nil || nameHash(b.Name) != string(k) || b.Generation != string(v) {
				return ErrCatalogIndex
			}
			buckets++
			return nil
		}); err != nil || buckets != u.Buckets {
			return ErrCatalogIndex
		}
		return nil
	})
}

func (s *Store) noteUsageDrift(u *objectinventory.Usage, path string) {
	addInventoryIssue(u, "usage_counter_drift", path, true)
	s.latchUsageDrift()
}
