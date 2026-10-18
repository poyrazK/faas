package objectnode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectinventory"
	bolt "go.etcd.io/bbolt"
)

const inventoryAuditBucket = "inventory-audit"

type InventoryAuditOptions struct {
	MaxRecords int
	StepTime   time.Duration
	// Zero selects the default, preserving callers that only set turn budgets.
	MaxPending int
}

func DefaultInventoryAuditOptions() InventoryAuditOptions {
	return InventoryAuditOptions{MaxRecords: api.ObjectNodeAuditRecords, StepTime: api.ObjectNodeAuditStepTime, MaxPending: api.ObjectNodeAuditPendingRecords}
}

func (o InventoryAuditOptions) Validate() error {
	if o.MaxRecords < 1 || o.MaxRecords > api.ObjectNodeAuditMaxRecords || o.StepTime <= 0 || o.StepTime > api.ObjectNodeAuditMaxStepTime || o.MaxPending < 0 || o.MaxPending > api.ObjectNodeAuditMaxPendingRecords {
		return fmt.Errorf("object node: invalid inventory audit budget: %w", ErrInvalid)
	}
	return nil
}

// Cached progress; only a complete healthy cycle is capacity evidence.
type InventoryAuditStatus struct {
	State           string                `json:"state"`
	Phase           string                `json:"phase"`
	StepRecords     int                   `json:"step_records"`
	Steps           uint64                `json:"steps"`
	Examined        uint64                `json:"examined"`
	Restarts        uint64                `json:"restarts"`
	Cycles          uint64                `json:"cycles"`
	Failures        uint64                `json:"failures"`
	Reconciled      uint64                `json:"reconciled"`
	Overflows       uint64                `json:"overflows"`
	Pending         int64                 `json:"pending"`
	BacklogSince    time.Time             `json:"backlog_since"`
	LastCompletedAt time.Time             `json:"last_completed_at"`
	LastHealthyAt   time.Time             `json:"last_healthy_at"`
	Usage           objectinventory.Usage `json:"usage"`
}

type inventoryAuditState struct {
	Version   int
	Boot      string
	Cycle     string
	Status    InventoryAuditStatus
	Walk      []auditDirectory
	Observed  auditSet
	Cursor    string
	Child     string
	Scope     string
	Table     int
	DirtyTurn bool
	Seal      string
}

func (s *Store) InventoryAuditStatus() InventoryAuditStatus {
	s.auditMu.Lock()
	defer s.auditMu.Unlock()
	status := s.auditStats
	status.Pending, status.Overflows = s.auditPending.Load(), s.auditOverflows.Load()
	if since := s.auditBacklogSince.Load(); since != 0 {
		status.BacklogSince = time.Unix(0, since).UTC()
	} else {
		status.BacklogSince = time.Time{}
	}
	status.Usage.Issues = append([]objectinventory.Issue(nil), status.Usage.Issues...)
	return status
}

// The source is stable for one bounded turn. Ledger changes, consumed mutations
// and the checkpoint share one transaction; no descriptor survives the turn.
func (s *Store) AuditStep(ctx context.Context, options InventoryAuditOptions) (InventoryAuditStatus, error) {
	if err := options.Validate(); err != nil {
		return InventoryAuditStatus{}, err
	}
	if options.MaxPending == 0 {
		options.MaxPending = api.ObjectNodeAuditPendingRecords
	}
	s.life.RLock()
	defer s.life.RUnlock()
	s.auditMu.Lock()
	defer s.auditMu.Unlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.auditReady(ctx); err != nil {
		return s.auditStats, err
	}
	p, err := s.loadInventoryAudit()
	if err != nil {
		s.auditStats.Failures++
		return s.auditStats, fmt.Errorf("object node: audit checkpoint: %w", err)
	}
	var changes auditChanges
	var proof, metaProof string
	overflowed := false
	err = s.catalogIndex.db.Update(func(tx *bolt.Tx) error {
		var e error
		changes, e = s.readAuditChanges(tx)
		if e != nil {
			s.latchUsageDrift()
			return e
		}
		if p == nil || p.Status.State == "complete" {
			p = s.newInventoryAudit(p)
			changes.Active = true
		}
		if p.Boot != s.inventoryBoot {
			return ErrCorrupt
		}
		changes.Limit = options.MaxPending
		if changes.Pending > changes.Limit {
			overflowed = !changes.Gap
			changes.Gap = true
		}
		if changes.Gap && p.Status.Phase != "reset" {
			p = s.newInventoryAudit(p)
			p.Status.Phase = "reset"
		}
		p.Status.Failures = s.auditStats.Failures
		p.Status.StepRecords = 0
		deadline := time.Now().Add(options.StepTime)
		for p.Status.State == "running" && p.Status.StepRecords < options.MaxRecords && time.Now().Before(deadline) {
			if e = ctx.Err(); e != nil {
				return e
			}
			if changes.Gap {
				e = s.auditResetUnit(tx, p, &changes)
			} else {
				e = s.inventoryAuditUnit(tx, p, &changes)
			}
			p.Status.StepRecords++
			p.Status.Examined++
			if p.Status.Usage.Errors != 0 {
				s.withdrawInventoryHealth()
			}
			if e != nil {
				return e
			}
			if s.requireCatalogIndex() != nil {
				break
			}
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		p.Status.Steps++
		p.Status.Pending, p.Status.BacklogSince, p.Status.Overflows = int64(changes.Pending), changes.Since, s.auditOverflows.Load()
		if overflowed {
			p.Status.Overflows++
		}
		if metaProof, e = s.writeAuditChanges(tx, changes); e != nil {
			return e
		}
		proof, e = s.saveInventoryAuditTx(tx, p)
		return e
	})
	if err != nil {
		s.auditStats.Failures++
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && (errors.Is(err, ErrCatalogIndex) || errors.Is(err, ErrCorrupt)) {
			s.withdrawInventoryHealth()
			s.latchUsageDrift()
		}
		return s.auditStats, fmt.Errorf("object node: audit step: %w", err)
	}
	s.auditProof = proof
	if overflowed {
		s.auditOverflows.Add(1)
	}
	s.cacheAuditChanges(changes, metaProof)
	if err = ctx.Err(); err != nil {
		return s.auditStats, err
	} // Commit cannot promote canceled evidence.
	s.auditStats = p.Status
	if p.Status.State == "complete" {
		s.publishInventoryHealth(p)
	}
	return s.auditStats, s.requireCatalogIndex()
}

func (s *Store) auditReady(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.closed {
		return ErrClosed
	}
	if err := s.servingError(); err != nil {
		return err
	}
	if s.pending != nil || s.replicationDirty || s.replication != nil && s.replication.ReceivedSequence != s.replication.AppliedSequence {
		return ErrCatalogBusy
	}
	if err := s.requireCatalogIndex(); err != nil {
		return err
	}
	if s.auditTrackedEpoch != s.inventoryEpoch {
		s.latchUsageDrift()
		return ErrCatalogIndex
	}
	if err := s.catalogIndex.db.View(func(tx *bolt.Tx) error {
		if err := requireUsageTables(tx); err != nil {
			return err
		}
		for _, name := range auditTables {
			if tx.Bucket([]byte(name)) == nil {
				return ErrCatalogIndex
			}
		}
		return nil
	}); err != nil {
		s.latchUsageDrift()
		return err
	}
	return nil
}

func (s *Store) newInventoryAudit(previous *inventoryAuditState) *inventoryAuditState {
	status := s.auditStats
	if previous != nil {
		status = previous.Status
		if status.State == "running" {
			status.Restarts++
		}
	}
	// An fsync may have committed a complete checkpoint after cancellation. Only
	// accepted process-local counts and timestamps may cross a cycle boundary.
	status.Cycles, status.LastCompletedAt, status.LastHealthyAt = s.auditStats.Cycles, s.auditStats.LastCompletedAt, s.auditStats.LastHealthyAt
	status.State, status.Phase = "running", "files"
	status.Usage = objectinventory.Usage{Version: objectinventory.Version, Snapshot: s.inventorySnapshot(), StartedAt: time.Now().UTC(), CatalogHealthy: true, PhysicalSample: true, Issues: []objectinventory.Issue{}}
	p := &inventoryAuditState{Version: 2, Boot: s.inventoryBoot, Cycle: uuid.NewString(), Status: status, Walk: []auditDirectory{{}}}
	s.auditFixedSource(p, "recovery.json")
	if p.Status.Usage.Errors != 0 {
		s.withdrawInventoryHealth()
	}
	return p
}

func (s *Store) loadInventoryAudit() (*inventoryAuditState, error) {
	var p *inventoryAuditState
	err := s.catalogIndex.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(inventoryAuditBucket))
		if b == nil {
			return ErrCatalogIndex
		}
		body := b.Get([]byte("checkpoint"))
		if body == nil && s.auditProof == "" {
			return nil
		}
		if len(body) == 0 || len(body) > api.ObjectNodeAuditCheckpointMaxBytes || nameHash(string(body)) != s.auditProof || decodeBackupRecord(body, &p) != nil || p == nil || p.Version != 2 {
			return ErrCorrupt
		}
		seal := p.Seal
		p.Seal = ""
		if seal != scrubSeal(p) {
			return ErrCorrupt
		}
		return nil
	})
	return p, err
}

func (s *Store) saveInventoryAuditTx(tx *bolt.Tx, p *inventoryAuditState) (string, error) {
	p.Seal = ""
	p.Seal = scrubSeal(p)
	body, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	if len(body) > api.ObjectNodeAuditCheckpointMaxBytes {
		return "", ErrCapacity
	}
	if s.auditCheckpoint != nil {
		if err = s.auditCheckpoint(); err != nil {
			return "", err
		}
	}
	b := tx.Bucket([]byte(inventoryAuditBucket))
	if b == nil {
		return "", ErrCatalogIndex
	}
	if err = b.Put([]byte("checkpoint"), body); err != nil {
		return "", err
	}
	return nameHash(string(body)), nil
}

func (s *Store) saveInventoryAudit(p *inventoryAuditState) error {
	var proof string
	err := s.catalogIndex.db.Update(func(tx *bolt.Tx) error { var e error; proof, e = s.saveInventoryAuditTx(tx, p); return e })
	if err == nil {
		s.auditProof = proof
	}
	return err
}

func (s *Store) withdrawInventoryHealth() {
	s.usageAudit.Store(&usageAuditState{At: time.Now().UTC(), Healthy: false})
}

func (s *Store) publishInventoryHealth(p *inventoryAuditState) {
	next := &usageAuditState{At: p.Status.Usage.StartedAt, Healthy: p.Status.Usage.CatalogHealthy && s.requireCatalogIndex() == nil}
	for {
		previous := s.usageAudit.Load()
		if previous != nil && previous.At.After(next.At) {
			return
		}
		if s.usageAudit.CompareAndSwap(previous, next) {
			return
		}
	}
}
