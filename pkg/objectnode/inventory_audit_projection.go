package objectnode

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	bolt "go.etcd.io/bbolt"
)

func auditNextRow(b *bolt.Bucket, after, prefix string) ([]byte, []byte) {
	if b == nil {
		return nil, nil
	}
	c := b.Cursor()
	k, v := c.Seek([]byte(prefix))
	if after != "" {
		k, v = c.Seek([]byte(after))
		if bytes.Equal(k, []byte(after)) {
			k, v = c.Next()
		}
	}
	if k == nil || !bytes.HasPrefix(k, []byte(prefix)) {
		return nil, nil
	}
	return k, v
}

// Reverse checks catch extra rows even if an attacker preserves all totals.
// Nested bucket boundaries also consume a work unit, including empty buckets.
func (s *Store) auditProjectionUnit(tx *bolt.Tx, p *inventoryAuditState) error {
	tables := []string{usageObjectsBucket, usageUploadsBucket, usagePartsBucket, catalogBuckets, catalogReverse, catalogCounts, usageTotalsBucket, catalogObjects, usageMembersBucket, catalogUploads, catalogDue}
	if p.Table >= len(tables) {
		p.Status.Phase, p.Cursor, p.Scope, p.Child = "barrier", "", "", ""
		return nil
	}
	table := tables[p.Table]
	b := tx.Bucket([]byte(table))
	nested := table == catalogObjects || table == usageMembersBucket
	if nested && p.Scope == "" {
		k, v := auditNextRow(b, p.Cursor, "")
		if k == nil {
			p.Table++
			p.Cursor = ""
			return nil
		}
		scope := string(k)
		if v != nil || !s.auditLiveScope(scope) {
			s.noteUsageDrift(&p.Status.Usage, table)
			return nil
		}
		p.Scope, p.Cursor, p.Child = scope, scope, ""
		return nil
	}
	scope := p.Scope
	after := p.Cursor
	if table == catalogDue && after != "" {
		decoded, err := hex.DecodeString(after)
		if err != nil {
			return ErrCorrupt
		}
		after = string(decoded)
	}
	if nested {
		b = b.Bucket([]byte(scope))
		after = p.Child
	}
	k, v := auditNextRow(b, after, "")
	if k == nil {
		if nested {
			p.Scope, p.Child = "", ""
		} else {
			p.Table++
			p.Cursor = ""
		}
		return nil
	}
	if nested {
		p.Child = string(k)
	} else {
		p.Cursor = string(k)
		if table == catalogDue {
			p.Cursor = hex.EncodeToString(k)
		}
	}
	if err := s.auditProjectionRow(tx, p, table, scope, string(k), v); err != nil {
		identity := string(k)
		if table == catalogDue {
			identity = hex.EncodeToString(k)
		}
		s.noteUsageDrift(&p.Status.Usage, table+"/"+scope+"/"+identity)
	}
	return nil
}

func (s *Store) auditLiveScope(scope string) bool {
	if len(scope) != 100 || !validScrubFindingID(scope[:64]) || !validReceiptID(scope[64:]) {
		return false
	}
	var b bucketRecord
	return readJSON(filepath.Join(s.root, "buckets", scope[:64], "bucket.json"), &b) == nil && nameHash(b.Name) == scope[:64] && b.Generation == scope[64:]
}

func (s *Store) reverseAuditSource(tx *bolt.Tx, p *inventoryAuditState, key string) (*auditNode, error) {
	n, err := s.sourceAuditNode(key, &p.Status.Usage)
	if err != nil {
		addInventoryIssue(&p.Status.Usage, "invalid_source_catalog", key, true)
		return nil, nil //nolint:nilerr // Source damage remains a diagnostic, never a repair instruction.
	}
	if n == nil {
		return nil, ErrCatalogIndex
	}
	if err = s.auditSourceProjection(tx, *n); err != nil {
		return nil, err
	}
	return n, nil
}

func (s *Store) auditProjectionRow(tx *bolt.Tx, p *inventoryAuditState, table, scope, key string, value []byte) error {
	switch table {
	case usageObjectsBucket:
		if len(key) != 164 {
			return ErrCatalogIndex
		}
		if _, exists, err := readUsageObject(tx.Bucket([]byte(table)), []byte(key)); err != nil || !exists {
			return ErrCatalogIndex
		}
		n, err := s.reverseAuditSource(tx, p, "o/"+key[:64]+"/"+key[100:])
		if err != nil || n != nil && n.Scope != key[:100] {
			return ErrCatalogIndex
		}
	case usageUploadsBucket:
		row, exists, err := readUsageUpload(tx, key)
		if err != nil || !exists {
			return ErrCatalogIndex
		}
		n, err := s.reverseAuditSource(tx, p, "u/"+key)
		if err != nil || n != nil && (!n.Active || n.Scope != row.Scope) {
			return ErrCatalogIndex
		}
	case usagePartsBucket:
		if len(key) != 57 || key[36] != ':' || !validReceiptID(key[:36]) {
			return ErrCatalogIndex
		}
		number, err := strconv.ParseInt(key[37:], 10, 32)
		if err != nil || number < 1 || number > api.MaxMultipartParts || fmtSequence(number) != key[37:] {
			return ErrCatalogIndex
		}
		if _, exists, err := readUsageObject(tx.Bucket([]byte(table)), []byte(key)); err != nil || !exists {
			return ErrCatalogIndex
		}
		// Terminal sessions deliberately leave obsolete part hints until rebuild.
		parent, err := s.sourceAuditNode("u/"+key[:36], &p.Status.Usage)
		if err != nil {
			addInventoryIssue(&p.Status.Usage, "invalid_upload_catalog", key[:36], true)
			return nil //nolint:nilerr // Retain source corruption as a diagnostic.
		}
		if parent == nil || !parent.Active {
			return nil
		}
		_, err = s.reverseAuditSource(tx, p, "p/"+key[:36]+"/"+strconv.FormatInt(number, 10))
		return err
	case catalogBuckets:
		if !validScrubFindingID(key) || !validReceiptID(string(value)) {
			return ErrCatalogIndex
		}
		n, err := s.reverseAuditSource(tx, p, "b/"+key)
		if err != nil || n != nil && n.Scope != key+string(value) {
			return ErrCatalogIndex
		}
	case catalogReverse:
		if len(key) != 128 || !validKey(string(value)) || nameHash(string(value)) != key[64:] {
			return ErrCatalogIndex
		}
		_, err := s.reverseAuditSource(tx, p, "o/"+key[:64]+"/"+key[64:])
		return err
	case catalogCounts:
		if !s.auditLiveScope(key) || len(value) != 8 {
			return ErrCatalogIndex
		}
	case usageTotalsBucket:
		if key != usageGlobalKey && !s.auditLiveScope(key) {
			return ErrCatalogIndex
		}
		_, err := readUsage(tx, key)
		return err
	case catalogObjects:
		if !validKey(key) || !validReceiptID(string(value)) {
			return ErrCatalogIndex
		}
		n, err := s.reverseAuditSource(tx, p, "o/"+scope[:64]+"/"+nameHash(key))
		if err != nil || n != nil && n.Scope != scope {
			return ErrCatalogIndex
		}
	case usageMembersBucket:
		if key != string(value) || !validReceiptID(key) {
			return ErrCatalogIndex
		}
		n, err := s.reverseAuditSource(tx, p, "u/"+key)
		if err != nil || n != nil && (!n.Active || n.Scope != scope) {
			return ErrCatalogIndex
		}
	case catalogUploads:
		var row catalogUpload
		if json.Unmarshal(value, &row) != nil || row.Due < 0 || len(row.Scope) != 100 {
			return ErrCatalogIndex
		}
		n, err := s.reverseAuditSource(tx, p, "u/"+key)
		if err != nil {
			return err
		}
		if n == nil {
			return nil
		}
		parts, err := s.multipartPartsExist(key)
		if err != nil || n.Scope != row.Scope || n.Active != row.Active || !n.Active && !parts || string(tx.Bucket([]byte(catalogDue)).Get(cleanupDueKey(key, row.Due))) != key {
			return ErrCatalogIndex
		}
	case catalogDue:
		if !validReceiptID(string(value)) {
			return ErrCatalogIndex
		}
		var row catalogUpload
		body := tx.Bucket([]byte(catalogUploads)).Get(value)
		if body == nil || json.Unmarshal(body, &row) != nil || string(cleanupDueKey(string(value), row.Due)) != key {
			return ErrCatalogIndex
		}
	default:
		return ErrCorrupt
	}
	return nil
}
