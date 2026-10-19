package objectnode

import (
	"errors"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectinventory"
	bolt "go.etcd.io/bbolt"
)

type auditObservation struct {
	Cycle string
	Node  auditNode
}
type auditScopeObservation struct {
	Cycle  string
	Totals objectinventory.Totals
}

func auditLogical(n auditNode) objectinventory.Totals {
	switch n.Key[0] {
	case 'o':
		return objectinventory.Totals{Objects: 1, CommittedBytes: n.Size}
	case 'u':
		if n.Active {
			return objectinventory.Totals{MultipartUploads: 1, MultipartParts: n.Parts.Count, MultipartBytes: n.PartBytes}
		}
	}
	return objectinventory.Totals{}
}

func changeAuditNumber(value *int64, change int64) error {
	if change < 0 && *value < -change || change > 0 && *value > api.MaxObjectStoragePolicyValue-change {
		return ErrCatalogIndex
	}
	*value += change
	return nil
}

func changeAuditTotals(dst *objectinventory.Totals, n auditNode, sign int64) error {
	t := auditLogical(n)
	for _, pair := range []struct {
		value  *int64
		change int64
	}{{&dst.Objects, t.Objects}, {&dst.CommittedBytes, t.CommittedBytes}, {&dst.MultipartUploads, t.MultipartUploads}, {&dst.MultipartParts, t.MultipartParts}, {&dst.MultipartBytes, t.MultipartBytes}} {
		if err := changeAuditNumber(pair.value, sign*pair.change); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) changeAuditObservation(tx *bolt.Tx, p *inventoryAuditState, n auditNode, add bool) error {
	sign := int64(-1)
	if add {
		sign = 1
	}
	if err := s.changeAuditSet(&p.Observed, n, add); err != nil {
		return err
	}
	u := &p.Status.Usage
	total := objectinventory.Totals{Objects: u.Objects, CommittedBytes: u.CommittedBytes, MultipartUploads: u.MultipartUploads, MultipartParts: u.MultipartParts, MultipartBytes: u.MultipartBytes}
	if err := changeAuditTotals(&total, n, sign); err != nil {
		return err
	}
	u.Objects, u.CommittedBytes, u.MultipartUploads, u.MultipartParts, u.MultipartBytes = total.Objects, total.CommittedBytes, total.MultipartUploads, total.MultipartParts, total.MultipartBytes
	switch n.Key[0] {
	case 'b':
		if err := changeAuditNumber(&u.Buckets, sign); err != nil {
			return err
		}
	case 'u':
		if err := changeAuditNumber(&u.Sessions, sign); err != nil {
			return err
		}
	case 'r':
		if err := changeAuditNumber(&u.Receipts, sign); err != nil {
			return err
		}
		if err := changeAuditNumber(&u.ReceiptBytes, sign*n.Size); err != nil {
			return err
		}
	}
	if n.Scope == "" {
		return nil
	}
	var scope auditScopeObservation
	_, err := s.readAuditRow(tx, auditScopes, n.Scope, &scope)
	if err != nil {
		return err
	}
	if scope.Cycle != p.Cycle {
		scope = auditScopeObservation{Cycle: p.Cycle}
	}
	if err = changeAuditTotals(&scope.Totals, n, sign); err != nil {
		return err
	}
	return s.putAuditRow(tx, auditScopes, n.Scope, scope)
}

func (s *Store) replaceAuditObserved(tx *bolt.Tx, p *inventoryAuditState, key string, next *auditNode) error {
	var old auditObservation
	exists, err := s.readAuditRow(tx, auditSeen, key, &old)
	if err != nil {
		return err
	}
	if exists && old.Cycle == p.Cycle {
		if old.Node.Key != key {
			return ErrCorrupt
		}
		if err = s.changeAuditObservation(tx, p, old.Node, false); err != nil {
			return err
		}
	}
	if next == nil {
		return tx.Bucket([]byte(auditSeen)).Delete([]byte(key))
	}
	if err = s.changeAuditObservation(tx, p, *next, true); err != nil {
		return err
	}
	return s.putAuditRow(tx, auditSeen, key, auditObservation{Cycle: p.Cycle, Node: *next})
}

func (s *Store) checkAuditSource(tx *bolt.Tx, p *inventoryAuditState, key string) error {
	if strings.HasPrefix(key, "p/") {
		return s.checkAuditPart(tx, p, key)
	}
	n, err := s.sourceAuditNode(key, &p.Status.Usage)
	if err != nil {
		addInventoryIssue(&p.Status.Usage, "invalid_source_catalog", key, true)
		return nil //nolint:nilerr // Preserve source damage as a finding, never as zero usage.
	}
	if n != nil {
		var registered auditNode
		exists, readErr := s.readAuditRow(tx, auditSource, key, &registered)
		if readErr != nil {
			return readErr
		}
		registered.Parts, registered.PartBytes = auditSet{}, 0
		if !exists || registered != *n {
			s.noteUsageDrift(&p.Status.Usage, key)
		}
		var previous auditObservation
		_, err = s.readAuditRow(tx, auditSeen, key, &previous)
		if err != nil {
			return err
		}
		if n.Active && previous.Cycle == p.Cycle && previous.Node.Active {
			n.Parts, n.PartBytes = previous.Node.Parts, previous.Node.PartBytes
		}
		if s.auditSourceProjection(tx, *n) != nil {
			s.noteUsageDrift(&p.Status.Usage, key)
		}
	}
	return s.replaceAuditObserved(tx, p, key, n)
}

func (s *Store) checkAuditPart(tx *bolt.Tx, p *inventoryAuditState, key string) error {
	pieces := strings.Split(key, "/")
	if len(pieces) != 3 {
		return ErrCorrupt
	}
	parentKey := "u/" + pieces[1]
	if err := s.checkAuditSource(tx, p, parentKey); err != nil {
		return err
	}
	var parent auditObservation
	exists, err := s.readAuditRow(tx, auditSeen, parentKey, &parent)
	if err != nil {
		return err
	}
	if !exists || parent.Cycle != p.Cycle || !parent.Node.Active {
		return nil
	}
	n, err := s.sourceAuditNode(key, &p.Status.Usage)
	if err != nil {
		addInventoryIssue(&p.Status.Usage, "invalid_part_catalog", key, true)
		return nil //nolint:nilerr // Preserve the finding and continue bounded coverage.
	}
	var old auditObservation
	exists, err = s.readAuditRow(tx, auditSeenParts, key, &old)
	if err != nil {
		return err
	}
	if exists && old.Cycle == p.Cycle {
		if err = s.changeAuditSet(&parent.Node.Parts, old.Node, false); err != nil {
			return err
		}
		parent.Node.PartBytes -= old.Node.Size
	}
	if n != nil {
		var registered auditNode
		exists, readErr := s.readAuditRow(tx, auditSourceParts, key, &registered)
		if readErr != nil {
			return readErr
		}
		if !exists || registered != *n {
			s.noteUsageDrift(&p.Status.Usage, key)
		}
		if s.auditSourceProjection(tx, *n) != nil {
			s.noteUsageDrift(&p.Status.Usage, key)
		}
		if err = s.changeAuditSet(&parent.Node.Parts, *n, true); err != nil {
			return err
		}
		if err = addInventoryBytes(&parent.Node.PartBytes, n.Size); err != nil {
			return err
		}
		if err = s.putAuditRow(tx, auditSeenParts, key, auditObservation{Cycle: p.Cycle, Node: *n}); err != nil {
			return err
		}
	} else if err = tx.Bucket([]byte(auditSeenParts)).Delete([]byte(key)); err != nil {
		return err
	}
	return s.replaceAuditObserved(tx, p, parentKey, &parent.Node)
}

func (s *Store) checkAuditScope(tx *bolt.Tx, p *inventoryAuditState, scope string) error {
	if len(scope) != 100 || !validScrubFindingID(scope[:64]) || !validReceiptID(scope[64:]) {
		return ErrCatalogIndex
	}
	n, err := s.sourceAuditNode("b/"+scope[:64], &p.Status.Usage)
	if err != nil {
		addInventoryIssue(&p.Status.Usage, "invalid_bucket_catalog", scope, true)
		return nil //nolint:nilerr // Source corruption withdraws health without repairing metadata.
	}
	if n == nil || n.Scope != scope {
		for _, table := range []string{usageTotalsBucket, catalogCounts} {
			if tx.Bucket([]byte(table)).Get([]byte(scope)) != nil {
				return ErrCatalogIndex
			}
		}
		for _, table := range []string{catalogObjects, usageMembersBucket} {
			if tx.Bucket([]byte(table)).Bucket([]byte(scope)) != nil {
				return ErrCatalogIndex
			}
		}
		if k, _ := auditNextRow(tx.Bucket([]byte(usageObjectsBucket)), "", scope); k != nil {
			return ErrCatalogIndex
		}
		return nil
	}
	var observed auditScopeObservation
	_, err = s.readAuditRow(tx, auditScopes, scope, &observed)
	if err != nil {
		return err
	}
	if observed.Cycle != p.Cycle {
		observed.Totals = objectinventory.Totals{}
	}
	row, err := readUsage(tx, scope)
	count, countErr := catalogCount(tx, scope)
	if err != nil || countErr != nil || row.Totals != observed.Totals || count != uint64(observed.Totals.MultipartUploads) {
		return ErrCatalogIndex
	}
	return nil
}

func (s *Store) checkAuditUpload(tx *bolt.Tx, p *inventoryAuditState, id string) error {
	var seen auditObservation
	exists, err := s.readAuditRow(tx, auditSeen, "u/"+id, &seen)
	if err != nil {
		return err
	}
	row, active, err := readUsageUpload(tx, id)
	if err != nil {
		return err
	}
	if !exists || seen.Cycle != p.Cycle || !seen.Node.Active {
		if active {
			return ErrCatalogIndex
		}
		return nil
	}
	var source auditNode
	exists, err = s.readAuditRow(tx, auditSource, "u/"+id, &source)
	if err != nil || !exists || source != seen.Node || !active || row.Scope != source.Scope || row.Parts != source.Parts.Count || row.Bytes != source.PartBytes {
		return errors.Join(err, ErrCatalogIndex)
	}
	return nil
}
