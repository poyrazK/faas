package objectnode

import (
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/objectinventory"
	bolt "go.etcd.io/bbolt"
	"golang.org/x/sys/unix"
)

func (s *Store) inventoryAuditUnit(tx *bolt.Tx, p *inventoryAuditState, m *auditChanges) error {
	p.DirtyTurn = !p.DirtyTurn
	if p.DirtyTurn || p.Status.Phase == "drain" {
		used, err := s.auditDirtyUnit(tx, p, m)
		if err != nil || used {
			return err
		}
	}
	switch p.Status.Phase {
	case "files":
		return s.auditPhysicalUnit(tx, p)
	case "heads", "parts":
		return s.auditRegistryUnit(tx, p)
	case "projections":
		return s.auditProjectionUnit(tx, p)
	case "barrier":
		if p.Observed != s.auditSourceRoot && p.Status.Usage.Errors == 0 {
			k, _ := tx.Bucket([]byte(auditDirty)).Cursor().First()
			if k != nil && strings.HasPrefix(string(k), "0/") {
				return nil
			}
			return ErrCatalogIndex
		}
		p.Status.Phase, p.Cursor = "scopes", ""
	case "scopes", "uploads":
		if p.Observed != s.auditSourceRoot && p.Status.Usage.Errors == 0 {
			return nil
		}
		table := auditScopes
		if p.Status.Phase == "uploads" {
			table = auditSeen
		}
		k, _ := auditNextRow(tx.Bucket([]byte(table)), p.Cursor, "")
		if k == nil {
			p.Cursor = ""
			if p.Status.Phase == "scopes" {
				p.Status.Phase = "uploads"
			} else {
				p.Status.Phase, p.Table = "cleanup", 0
			}
			return nil
		}
		p.Cursor = string(k)
		// Reconcile current changes before comparing partial aggregates. A later
		// mutation also queues the affected final checks, even behind these cursors.
		if p.Observed != s.auditSourceRoot {
			return nil
		}
		var err error
		if table == auditScopes {
			err = s.checkAuditScope(tx, p, string(k))
		} else if strings.HasPrefix(string(k), "u/") {
			err = s.checkAuditUpload(tx, p, string(k)[2:])
		}
		if err != nil {
			s.noteUsageDrift(&p.Status.Usage, string(k))
		}
	case "cleanup":
		return s.auditCleanupUnit(tx, p)
	case "drain":
		if m.Pending != 0 || m.Bytes != 0 {
			return ErrCatalogIndex
		}
		if p.Observed != s.auditSourceRoot && p.Status.Usage.Errors == 0 {
			return ErrCatalogIndex
		}
		return s.finishInventoryAudit(tx, p, m)
	default:
		return ErrCorrupt
	}
	return nil
}

func (s *Store) auditRegistryUnit(tx *bolt.Tx, p *inventoryAuditState) error {
	table := auditSource
	if p.Status.Phase == "parts" {
		table = auditSourceParts
	}
	k, _ := auditNextRow(tx.Bucket([]byte(table)), p.Cursor, "")
	if k == nil {
		p.Cursor = ""
		if table == auditSource {
			p.Status.Phase = "parts"
		} else {
			p.Status.Phase, p.Table = "projections", 0
		}
		return nil
	}
	key := string(k)
	p.Cursor = key
	var node auditNode
	exists, err := s.readAuditRow(tx, table, key, &node)
	if err != nil || !exists || node.Key != key {
		return ErrCorrupt
	}
	if table == auditSourceParts {
		var parent auditNode
		exists, err = s.readAuditRow(tx, auditSource, "u/"+strings.Split(key, "/")[1], &parent)
		if err != nil {
			return err
		}
		if !exists || !parent.Active {
			return tx.Bucket([]byte(table)).Delete(k)
		}
	}
	return s.checkAuditSource(tx, p, key)
}

func (s *Store) auditDirtyUnit(tx *bolt.Tx, p *inventoryAuditState, m *auditChanges) (bool, error) {
	b := tx.Bucket([]byte(auditDirty))
	k, v := b.Cursor().First()
	if k == nil {
		if m.Pending != 0 || m.Bytes != 0 {
			return false, ErrCatalogIndex
		}
		return false, nil
	}
	if m.Pending == 0 {
		return false, ErrCatalogIndex
	}
	key := string(k)
	if !strings.HasPrefix(key, "0/") && p.Status.Phase != "drain" {
		return false, nil
	}
	// Generation and upload aggregates are compared after source reconciliation.
	if !strings.HasPrefix(key, "0/") && p.Observed != s.auditSourceRoot && p.Status.Usage.Errors == 0 {
		return false, ErrCatalogIndex
	}
	var value string
	exists, err := s.readAuditRow(tx, auditDirty, key, &value)
	if err != nil || !exists || value != key {
		return false, ErrCorrupt
	}
	switch {
	case strings.HasPrefix(key, "0/f/"):
		s.auditFixedSource(p, key[4:])
	case strings.HasPrefix(key, "0/"):
		err = s.checkAuditSource(tx, p, key[2:])
	case strings.HasPrefix(key, "1/"):
		if p.Observed == s.auditSourceRoot {
			err = s.checkAuditScope(tx, p, key[2:])
		}
	case strings.HasPrefix(key, "1u/"):
		if p.Observed == s.auditSourceRoot {
			err = s.checkAuditUpload(tx, p, key[3:])
		}
	default:
		return false, ErrCorrupt
	}
	if err != nil {
		return false, err
	}
	m.Pending--
	m.Bytes -= int64(len(k) + len(v))
	if m.Pending < 0 || m.Bytes < 0 {
		return false, ErrCatalogIndex
	}
	if m.Pending == 0 {
		m.Since = time.Time{}
	}
	p.Status.Reconciled++
	return true, b.Delete(k)
}

func (s *Store) auditResetUnit(tx *bolt.Tx, p *inventoryAuditState, m *auditChanges) error {
	b := tx.Bucket([]byte(auditDirty))
	k, v := b.Cursor().First()
	if k != nil {
		if m.Pending <= 0 {
			return ErrCatalogIndex
		}
		var value string
		exists, err := s.readAuditRow(tx, auditDirty, string(k), &value)
		if err != nil || !exists || value != string(k) {
			return ErrCorrupt
		}
		m.Pending--
		m.Bytes -= int64(len(k) + len(v))
		return b.Delete(k)
	}
	if m.Pending != 0 || m.Bytes != 0 {
		return ErrCatalogIndex
	}
	m.Gap, m.Active, m.Since = false, true, time.Time{}
	// Changes made while discarding the overflowed queue are all represented by
	// the current source anchor. Start independent coverage at this barrier.
	p.Status.Phase, p.Status.Usage.StartedAt = "files", time.Now().UTC()
	p.Status.Usage.Snapshot = s.inventorySnapshot()
	s.auditFixedSource(p, "recovery.json")
	return nil
}

func (s *Store) auditCleanupUnit(tx *bolt.Tx, p *inventoryAuditState) error {
	tables := []string{auditSeen, auditSeenParts, auditScopes}
	if p.Table >= len(tables) {
		p.Status.Phase, p.Cursor = "drain", ""
		return nil
	}
	table := tables[p.Table]
	b := tx.Bucket([]byte(table))
	k, _ := auditNextRow(b, p.Cursor, "")
	if k == nil {
		p.Table++
		p.Cursor = ""
		return nil
	}
	p.Cursor = string(k)
	cycle := ""
	if table == auditScopes {
		var row auditScopeObservation
		if _, err := s.readAuditRow(tx, table, string(k), &row); err != nil {
			return err
		}
		cycle = row.Cycle
	} else {
		var row auditObservation
		if _, err := s.readAuditRow(tx, table, string(k), &row); err != nil {
			return err
		}
		cycle = row.Cycle
		if table == auditSeenParts && cycle == p.Cycle {
			var parent auditObservation
			_, err := s.readAuditRow(tx, auditSeen, "u/"+strings.Split(string(k), "/")[1], &parent)
			if err != nil {
				return err
			}
			if parent.Cycle != p.Cycle || !parent.Node.Active {
				cycle = ""
			}
		}
	}
	if cycle != p.Cycle {
		return b.Delete(k)
	}
	return nil
}

func (s *Store) finishInventoryAudit(tx *bolt.Tx, p *inventoryAuditState, m *auditChanges) error {
	u := &p.Status.Usage
	row, err := readUsage(tx, usageGlobalKey)
	want := objectinventory.Totals{Objects: u.Objects, CommittedBytes: u.CommittedBytes, MultipartUploads: u.MultipartUploads, MultipartParts: u.MultipartParts, MultipartBytes: u.MultipartBytes}
	if err != nil || row.Totals != want {
		s.noteUsageDrift(u, catalogIndexName)
	}
	if previous := s.usageAudit.Load(); u.CatalogHealthy && previous != nil && !previous.Healthy && previous.At.After(u.StartedAt) {
		addInventoryIssue(u, "newer_failed_audit", catalogIndexName, true)
	}
	var stat unix.Statfs_t
	if err = unix.Statfs(s.root, &stat); err != nil {
		return err
	}
	u.DiskBytes, u.AvailableBytes = stat.Blocks*uint64(stat.Bsize), stat.Bavail*uint64(stat.Bsize)
	u.Snapshot, u.FinishedAt = s.inventorySnapshot(), time.Now().UTC()
	p.Status.State, p.Status.Phase = "complete", "complete"
	p.Status.Cycles++
	p.Status.LastCompletedAt = u.FinishedAt
	if u.CatalogHealthy && s.requireCatalogIndex() == nil {
		p.Status.LastHealthyAt = u.StartedAt
	}
	m.Active = false
	return nil
}
