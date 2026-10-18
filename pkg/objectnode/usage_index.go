package objectnode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectinventory"
	bolt "go.etcd.io/bbolt"
)

type usageAuditState struct {
	At      time.Time
	Healthy bool
}

type UsageCountersStatus struct {
	Ready                  bool
	AuditedAt              time.Time
	Queries, AuditFailures uint64
	Totals                 objectinventory.Totals
}

func (s *Store) UsageCountersStatus() (UsageCountersStatus, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return UsageCountersStatus{}, ErrClosed
	}
	status := UsageCountersStatus{Totals: s.usageTotals, Queries: s.usageQueries.Load(), AuditFailures: s.usageAuditFailures.Load()}
	if audit := s.usageAudit.Load(); audit != nil {
		status.AuditedAt = audit.At
	}
	status.Ready = s.requireCatalogIndex() == nil && s.healthyUsageAudit(time.Now().UTC())
	return status, nil
}

func (s *Store) healthyUsageAudit(now time.Time) bool {
	return healthyUsageAuditAt(s.usageAudit.Load(), now)
}

func healthyUsageAuditAt(a *usageAuditState, now time.Time) bool {
	return a != nil && a.Healthy && !a.At.IsZero() && !a.At.After(now) && now.Sub(a.At) <= time.Duration(api.ObjectStorageInventoryMaxAgeSeconds)*time.Second
}

// BucketUsageCounters is one coherent logical observation. It is not receipt
// settlement evidence, physical free space, or a cumulative billing meter.
func (s *Store) BucketUsageCounters(ctx context.Context, bucket string) (objectinventory.Counters, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return objectinventory.Counters{}, err
	}
	if err := s.servingError(); err != nil {
		return objectinventory.Counters{}, err
	}
	if s.pending != nil || s.replicationDirty || s.replication != nil && s.replication.ReceivedSequence != s.replication.AppliedSequence {
		return objectinventory.Counters{}, ErrCatalogBusy
	}
	b, err := s.bucket(bucket)
	if err != nil {
		return objectinventory.Counters{}, err
	}
	if err = s.requireCatalogIndex(); err != nil {
		return objectinventory.Counters{}, err
	}
	now := time.Now().UTC()
	audit := s.usageAudit.Load()
	if !healthyUsageAuditAt(audit, now) {
		return objectinventory.Counters{}, ErrUsageAudit
	}
	var record usageRecord
	err = s.catalogIndex.db.View(func(tx *bolt.Tx) error {
		var err error
		record, err = readUsage(tx, catalogScope(nameHash(bucket), b.Generation))
		return err
	})
	if err != nil {
		s.latchUsageDrift()
		return objectinventory.Counters{}, ErrCatalogIndex
	}
	if err = s.requireCatalogIndex(); err != nil {
		return objectinventory.Counters{}, err
	}
	if latest := s.usageAudit.Load(); latest != nil && !latest.Healthy {
		return objectinventory.Counters{}, ErrUsageAudit
	}
	s.usageQueries.Add(1)
	return objectinventory.Counters{Version: objectinventory.Version, Kind: objectinventory.CountersKind, Bucket: bucket, Generation: b.Generation, Snapshot: s.inventorySnapshot(), ObservedAt: now, AuditedAt: audit.At, Totals: record.Totals}, nil
}

func (s *Store) cacheUsageTotals() error {
	return s.catalogIndex.db.View(func(tx *bolt.Tx) error {
		r, err := readUsage(tx, usageGlobalKey)
		if err == nil {
			s.usageTotals = r.Totals
		}
		return err
	})
}

func (s *Store) latchUsageDrift() {
	if s.catalogIndex != nil && s.catalogIndex.failed.CompareAndSwap(false, true) {
		s.catalogReadFailures.Add(1)
		s.usageAuditFailures.Add(1)
	}
}

// Startup only. Each part batch is bounded even when an upload has 10,000 parts.
func (s *Store) buildUsageParts(u MultipartUpload) error {
	batch := make([]MultipartPart, 0, api.ObjectNodeCatalogIndexBuildBatch)
	flush := func() error {
		return s.catalogIndex.db.Update(func(tx *bolt.Tx) error {
			for _, p := range batch {
				if err := putUsagePart(tx, u, p); err != nil {
					return err
				}
				n := auditNode{Key: "p/" + u.ID + "/" + strconv.FormatInt(int64(p.Number), 10), Scope: catalogScope(nameHash(u.Bucket), u.Generation), Meta: scrubSeal(p), Size: p.Object.Size}
				if err := s.trackAuditPart(tx, &s.auditSourceRoot, n); err != nil {
					return err
				}
			}
			return nil
		})
	}
	dir := filepath.Join(s.multipartPath(u.ID), "parts")
	err := visitDirectory(dir, func(e os.DirEntry) error {
		n, err := strconv.ParseInt(e.Name(), 10, 32)
		if err != nil || !e.IsDir() || strconv.FormatInt(n, 10) != e.Name() || n < 1 || n > api.MaxMultipartParts {
			return ErrCorrupt
		}
		p, err := s.readPart(u.ID, int32(n))
		if errors.Is(err, ErrInvalidPart) {
			return nil
		}
		if err != nil || p.Object.Key != u.Key {
			return ErrCorrupt
		}
		if err = inventoryBlob(filepath.Join(dir, e.Name(), p.Object.ID+".data"), p.Object); err != nil {
			return err
		}
		batch = append(batch, p)
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
