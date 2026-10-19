package objectnode

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectinventory"
	bolt "go.etcd.io/bbolt"
)

// This private projection is never backup, replication or deletion authority.
const catalogIndexName = ".catalog-index.db"

const (
	catalogBuckets = "buckets"
	catalogObjects = "objects"
	catalogReverse = "object-paths"
	catalogUploads = "uploads"
	catalogDue     = "expiry"
	catalogCounts  = "active-counts"
)

type catalogIndex struct {
	db     *bolt.DB
	ready  bool
	failed atomic.Bool // Listing detects stale projections while holding a read lock.
}

type catalogUpload struct {
	Scope  string `json:"scope"`
	Due    int64  `json:"due"`
	Active bool   `json:"active"`
}

func catalogScope(hash, generation string) string { return hash + generation }

// Never reuse an unverified completeness checkpoint. A crash can occur between
// source publication and its projection, including on unjournaled format 3.
// Rebuilding after recovery also removes hints from a foreign/copied directory.
func newCatalogIndex(root string) (*catalogIndex, error) {
	path := filepath.Join(root, catalogIndexName)
	if info, err := os.Lstat(path); err == nil {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || !ok || stat.Nlink != 1 {
			return nil, ErrCorrupt
		}
		if err = os.Remove(path); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: api.ObjectNodeCatalogIndexOpenTimeout})
	if err != nil {
		return nil, err
	}
	i := &catalogIndex{db: db}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, name := range []string{catalogBuckets, catalogObjects, catalogReverse, catalogUploads, catalogDue, catalogCounts, usageTotalsBucket, usageObjectsBucket, usageUploadsBucket, usageMembersBucket, usagePartsBucket, inventoryAuditBucket} {
			if _, err := tx.CreateBucket([]byte(name)); err != nil {
				return err
			}
		}
		for _, name := range auditTables {
			if _, err := tx.CreateBucket([]byte(name)); err != nil {
				return err
			}
		}
		return writeUsage(tx, usageRecord{Scope: usageGlobalKey})
	})
	if err == nil {
		err = syncDirectory(root)
	}
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return i, nil
}

func catalogPutBucket(tx *bolt.Tx, hash, generation string) error {
	b := tx.Bucket([]byte(catalogBuckets))
	if old := b.Get([]byte(hash)); old != nil && string(old) != generation {
		return ErrCorrupt
	}
	if b.Get([]byte(hash)) == nil {
		if err := createUsageScope(tx, catalogScope(hash, generation)); err != nil {
			return err
		}
	} else if _, err := readUsage(tx, catalogScope(hash, generation)); err != nil {
		return err
	}
	if err := b.Put([]byte(hash), []byte(generation)); err != nil {
		return err
	}
	_, err := tx.Bucket([]byte(catalogObjects)).CreateBucketIfNotExists([]byte(catalogScope(hash, generation)))
	return err
}

func catalogPutObject(tx *bolt.Tx, hash string, o Object) error {
	generation := tx.Bucket([]byte(catalogBuckets)).Get([]byte(hash))
	if generation == nil {
		return ErrCorrupt
	}
	b := tx.Bucket([]byte(catalogObjects)).Bucket([]byte(catalogScope(hash, string(generation))))
	if b == nil {
		return ErrCorrupt
	}
	if err := putUsageObject(tx, catalogScope(hash, string(generation)), nameHash(o.Key), o); err != nil {
		return err
	}
	if err := b.Put([]byte(o.Key), []byte(o.ID)); err != nil {
		return err
	}
	return tx.Bucket([]byte(catalogReverse)).Put([]byte(hash+nameHash(o.Key)), []byte(o.Key))
}

func catalogRemoveObject(tx *bolt.Tx, hash, objectHash string) error {
	r := tx.Bucket([]byte(catalogReverse))
	path := []byte(hash + objectHash)
	key := r.Get(path)
	if key == nil {
		return nil // Idempotent journal replay.
	}
	generation := tx.Bucket([]byte(catalogBuckets)).Get([]byte(hash))
	b := tx.Bucket([]byte(catalogObjects)).Bucket([]byte(catalogScope(hash, string(generation))))
	if b == nil || nameHash(string(key)) != objectHash {
		return ErrCorrupt
	}
	if err := removeUsageObject(tx, catalogScope(hash, string(generation)), objectHash); err != nil {
		return err
	}
	if err := b.Delete(key); err != nil {
		return err
	}
	return r.Delete(path)
}

func catalogRemoveBucket(tx *bolt.Tx, hash string) error {
	b := tx.Bucket([]byte(catalogBuckets))
	generation := b.Get([]byte(hash))
	if generation == nil {
		return nil
	}
	objects := tx.Bucket([]byte(catalogObjects))
	scope := []byte(catalogScope(hash, string(generation)))
	if child := objects.Bucket(scope); child == nil {
		return ErrCorrupt
	} else if key, _ := child.Cursor().First(); key != nil {
		return ErrCorrupt // Source bucket removal must already have proved emptiness.
	}
	if err := objects.DeleteBucket(scope); err != nil {
		return err
	}
	r, err := readUsage(tx, string(scope))
	if err != nil || r.Totals != (objectinventory.Totals{}) {
		return ErrCatalogIndex
	}
	if err = tx.Bucket([]byte(usageTotalsBucket)).Delete(scope); err != nil {
		return err
	}
	if err = tx.Bucket([]byte(usageMembersBucket)).DeleteBucket(scope); err != nil {
		return err
	}
	return b.Delete([]byte(hash))
}

func catalogCount(tx *bolt.Tx, scope string) (uint64, error) {
	b := tx.Bucket([]byte(catalogCounts))
	if b == nil {
		return 0, ErrCatalogIndex
	}
	v := b.Get([]byte(scope))
	if v == nil {
		return 0, nil
	}
	if len(v) != 8 {
		return 0, ErrCorrupt
	}
	return binary.BigEndian.Uint64(v), nil
}

func catalogChangeCount(tx *bolt.Tx, scope string, add bool) error {
	n, err := catalogCount(tx, scope)
	if err != nil || !add && n == 0 {
		return ErrCorrupt
	}
	if add {
		n++
	} else {
		n--
	}
	if n == 0 {
		return tx.Bucket([]byte(catalogCounts)).Delete([]byte(scope))
	}
	v := make([]byte, 8)
	binary.BigEndian.PutUint64(v, n)
	return tx.Bucket([]byte(catalogCounts)).Put([]byte(scope), v)
}

func catalogPutUpload(tx *bolt.Tx, id string, next *catalogUpload) error {
	if err := requireUsageTables(tx); err != nil {
		return err
	}
	b := tx.Bucket([]byte(catalogUploads))
	if old := b.Get([]byte(id)); old != nil {
		var u catalogUpload
		if json.Unmarshal(old, &u) != nil || len(u.Scope) != 100 || u.Due < 0 {
			return ErrCorrupt
		}
		if err := tx.Bucket([]byte(catalogDue)).Delete(cleanupDueKey(id, u.Due)); err != nil {
			return err
		}
		if u.Active {
			if err := catalogChangeCount(tx, u.Scope, false); err != nil {
				return err
			}
		}
	}
	if next == nil {
		return b.Delete([]byte(id))
	}
	if next.Active {
		if err := catalogChangeCount(tx, next.Scope, true); err != nil {
			return err
		}
	}
	body, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if err = b.Put([]byte(id), body); err != nil {
		return err
	}
	return tx.Bucket([]byte(catalogDue)).Put(cleanupDueKey(id, next.Due), []byte(id))
}

func catalogUploadHint(u MultipartUpload, parts bool) *catalogUpload {
	active := u.Status == multipartActive || u.Status == multipartCompleting
	if !active && !parts {
		return nil
	}
	due := time.Now().UTC()
	if active {
		due = u.ExpiresAt
	}
	return &catalogUpload{Scope: catalogScope(nameHash(u.Bucket), u.Generation), Due: due.UnixMilli(), Active: active}
}
