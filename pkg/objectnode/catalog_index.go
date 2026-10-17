package objectnode

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	bolt "go.etcd.io/bbolt"
)

// CatalogIndexStatus is memory-only; metrics never open the database or catalog.
type CatalogIndexStatus struct {
	Ready          bool
	Rebuilds       uint64
	Failures       uint64
	ListExamined   uint64
	ExpirySteps    uint64
	ExpiryExamined uint64
	Expired        uint64
	PartsRemoved   uint64
	ExpiryDeferred uint64
}

func (s *Store) CatalogIndexStatus() (CatalogIndexStatus, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return CatalogIndexStatus{}, ErrClosed
	}
	status := s.catalogStats
	status.ListExamined = s.catalogListExamined.Load()
	status.Failures += s.catalogReadFailures.Load()
	status.Ready = status.Ready && s.catalogIndex != nil && !s.catalogIndex.failed.Load()
	return status, nil
}

func (s *Store) startCatalogIndex() {
	if s.catalogIndex != nil || s.replication != nil && s.replication.Role != "primary" {
		return
	}
	i, err := newCatalogIndex(s.root)
	if err != nil {
		s.catalogStats.Failures++
		return
	}
	s.catalogIndex = i
	if err = s.initializeAuditTracking(); err != nil {
		s.catalogStats.Failures++
		return
	}
	if err = s.buildCatalogIndex(); err != nil {
		s.catalogStats.Failures++
		return
	}
	if err = s.buildAuditReceipts(); err != nil {
		s.catalogStats.Failures++
		return
	}
	i.ready, s.catalogStats.Ready = true, true
	s.catalogStats.Rebuilds++
	if s.cacheUsageTotals() != nil {
		s.failCatalogIndex()
		return
	}
	s.usageAudit.Store(&usageAuditState{At: time.Now().UTC(), Healthy: true})
}

func (s *Store) buildCatalogIndex() error {
	err := visitDirectory(filepath.Join(s.root, "buckets"), func(e os.DirEntry) error {
		var b bucketRecord
		if !e.IsDir() || readJSON(filepath.Join(s.root, "buckets", e.Name(), "bucket.json"), &b) != nil || e.Name() != nameHash(b.Name) {
			return ErrCorrupt
		}
		if _, err := s.bucket(b.Name); err != nil {
			return err
		}
		if err := s.catalogIndex.db.Update(func(tx *bolt.Tx) error {
			if err := catalogPutBucket(tx, e.Name(), b.Generation); err != nil {
				return err
			}
			n := auditNode{Key: "b/" + e.Name(), Scope: catalogScope(e.Name(), b.Generation), Meta: scrubSeal(b)}
			return s.replaceAuditSource(tx, &s.auditSourceRoot, n.Key, &n)
		}); err != nil {
			return err
		}
		return s.buildBucketIndex(b)
	})
	if err != nil {
		return err
	}
	batch := make([]MultipartUpload, 0, api.ObjectNodeCatalogIndexBuildBatch)
	flush := func() error {
		return s.catalogIndex.db.Update(func(tx *bolt.Tx) error {
			for _, u := range batch {
				parts, err := s.multipartPartsExist(u.ID)
				if err != nil {
					return err
				}
				if err = catalogPutUpload(tx, u.ID, catalogUploadHint(u, parts)); err != nil {
					return err
				}
				if err = putUsageUpload(tx, u); err != nil {
					return err
				}
				n := auditNode{Key: "u/" + u.ID, Scope: catalogScope(nameHash(u.Bucket), u.Generation), Meta: scrubSeal(u), Active: u.Status == multipartActive || u.Status == multipartCompleting}
				if err = s.replaceAuditSource(tx, &s.auditSourceRoot, n.Key, &n); err != nil {
					return err
				}
			}
			return nil
		})
	}
	err = visitDirectory(filepath.Join(s.root, "multipart"), func(e os.DirEntry) error {
		u, err := s.readMultipart(e.Name())
		if err != nil {
			return err
		}
		batch = append(batch, u)
		active := u.Status == multipartActive || u.Status == multipartCompleting
		if len(batch) < cap(batch) && !active {
			return nil
		}
		err = flush()
		batch = batch[:0]
		if err == nil && active {
			err = s.buildUsageParts(u)
		}
		return err
	})
	if err == nil && len(batch) != 0 {
		err = flush()
	}
	return err
}

func (s *Store) buildBucketIndex(b bucketRecord) error {
	batch := make([]Object, 0, api.ObjectNodeCatalogIndexBuildBatch)
	flush := func() error {
		return s.catalogIndex.db.Update(func(tx *bolt.Tx) error {
			for _, o := range batch {
				if err := catalogPutObject(tx, nameHash(b.Name), o); err != nil {
					return err
				}
				n := auditNode{Key: "o/" + nameHash(b.Name) + "/" + nameHash(o.Key), Scope: catalogScope(nameHash(b.Name), b.Generation), Meta: scrubSeal(o), Size: o.Size}
				if err := s.replaceAuditSource(tx, &s.auditSourceRoot, n.Key, &n); err != nil {
					return err
				}
			}
			return nil
		})
	}
	dir := filepath.Join(s.bucketPath(b.Name), "objects")
	err := visitDirectory(dir, func(e os.DirEntry) error {
		o, err := readObject(filepath.Join(dir, e.Name()))
		if errors.Is(err, ErrNoObject) {
			return nil
		}
		if err != nil {
			return err
		}
		if err = inventoryBlob(filepath.Join(dir, e.Name(), o.ID+".data"), o); err != nil {
			return err
		}
		batch = append(batch, o)
		if len(batch) < cap(batch) {
			return nil
		}
		err = flush()
		batch = batch[:0]
		return err
	})
	if err == nil && len(batch) != 0 {
		err = flush()
	}
	return err
}

func (s *Store) multipartPartsExist(id string) (bool, error) {
	info, err := os.Lstat(filepath.Join(s.multipartPath(id), "parts"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, ErrCorrupt
	}
	return true, nil
}

// Called only after successful source publication under mu. Projection errors
// disable indexed operations, while preserving the durable mutation's result.
func (s *Store) observeCatalogPath(path string, remove bool) {
	if s.catalogIndex == nil || !s.catalogIndex.ready || s.catalogIndex.failed.Load() {
		return
	}
	// One publication may invalidate the revision once; replay/physical cleanup
	// can also publish without advancing it. A later write must not mask a gap.
	if s.inventoryEpoch < s.auditTrackedEpoch || s.inventoryEpoch > s.auditTrackedEpoch && s.inventoryEpoch-s.auditTrackedEpoch != 1 {
		s.failCatalogIndex()
		return
	}
	rel, err := filepath.Rel(s.root, path)
	if s.catalogIndexFault != nil && s.catalogIndexFault() != nil {
		s.failCatalogIndex()
		return
	}
	root := s.auditSourceRoot
	var changes auditChanges
	var proof string
	wasGap := false
	if err == nil {
		err = s.catalogIndex.db.Update(func(tx *bolt.Tx) error {
			if err := requireUsageTables(tx); err != nil {
				return err
			}
			changes, err = s.readAuditChanges(tx)
			if err != nil {
				return err
			}
			wasGap = changes.Gap
			p := strings.Split(filepath.ToSlash(rel), "/")
			if catalogPathRelevant(p, remove) {
				if err = s.updateCatalogPath(tx, p, remove); err != nil {
					return err
				}
			}
			if err = s.observeAuditPath(tx, &root, &changes, filepath.ToSlash(rel)); err != nil {
				return err
			}
			proof, err = s.writeAuditChanges(tx, changes)
			return err
		})
	}
	if err == nil {
		s.auditSourceRoot, s.auditTrackedEpoch = root, s.inventoryEpoch
		s.cacheAuditChanges(changes, proof)
		if changes.Gap && !wasGap {
			s.auditOverflows.Add(1)
		}
	}
	if err == nil && s.catalogCheckpoint != nil {
		err = s.catalogCheckpoint("index applied")
	}
	if err == nil {
		err = s.cacheUsageTotals()
	}
	if err != nil {
		s.failCatalogIndex()
	}
}

func catalogPathRelevant(p []string, remove bool) bool {
	return p[0] == "buckets" && (len(p) == 3 && p[2] == "bucket.json" && !remove || len(p) == 5 && p[2] == "objects" && p[4] == "current.json" && !remove || len(p) == 4 && p[2] == "objects" && remove || len(p) == 2 && remove) ||
		p[0] == "multipart" && (len(p) == 3 && (p[2] == "session.json" && !remove || p[2] == "parts" && remove) || len(p) == 5 && p[2] == "parts" && p[4] == "current.json" && !remove)
}

func (s *Store) updateCatalogPath(tx *bolt.Tx, p []string, remove bool) error {
	switch {
	case p[0] == "buckets" && len(p) == 3 && p[2] == "bucket.json" && !remove:
		var b bucketRecord
		if readJSON(filepath.Join(s.root, filepath.Join(p...)), &b) != nil || nameHash(b.Name) != p[1] {
			return ErrCorrupt
		}
		return catalogPutBucket(tx, p[1], b.Generation)
	case p[0] == "buckets" && len(p) == 5 && p[2] == "objects" && p[4] == "current.json" && !remove:
		o, err := readObject(filepath.Join(s.root, filepath.Join(p[:4]...)))
		if err != nil {
			return err
		}
		return catalogPutObject(tx, p[1], o)
	case p[0] == "buckets" && len(p) == 4 && p[2] == "objects" && remove:
		return catalogRemoveObject(tx, p[1], p[3])
	case p[0] == "buckets" && len(p) == 2 && remove:
		return catalogRemoveBucket(tx, p[1])
	case p[0] == "multipart" && len(p) == 3 && (p[2] == "session.json" && !remove || p[2] == "parts" && remove):
		u, err := s.readMultipart(p[1])
		if err != nil {
			return err
		}
		parts, err := s.multipartPartsExist(u.ID)
		if err != nil {
			return err
		}
		if err = putUsageUpload(tx, u); err != nil {
			return err
		}
		return catalogPutUpload(tx, u.ID, catalogUploadHint(u, parts))
	case p[0] == "multipart" && len(p) == 5 && p[2] == "parts" && p[4] == "current.json" && !remove:
		u, err := s.readMultipart(p[1])
		if err != nil {
			return err
		}
		number, err := strconv.ParseInt(p[3], 10, 32)
		if err != nil {
			return ErrCorrupt
		}
		part, err := s.readPart(u.ID, int32(number))
		if err != nil {
			return err
		}
		return putUsagePart(tx, u, part)
	}
	return nil
}

func (s *Store) failCatalogIndex() {
	if s.catalogIndex != nil && s.catalogIndex.ready {
		s.catalogIndex.ready, s.catalogStats.Ready = false, false
		s.catalogStats.Failures++
	}
}

func (s *Store) closeCatalogIndex() error {
	if s.catalogIndex == nil {
		return nil
	}
	i := s.catalogIndex
	s.catalogIndex = nil
	return i.db.Close()
}

func (s *Store) requireCatalogIndex() error {
	if s.catalogIndex == nil || !s.catalogIndex.ready || s.catalogIndex.failed.Load() {
		return ErrCatalogIndex
	}
	return nil
}
