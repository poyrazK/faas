package objectnode

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectinventory"
	bolt "go.etcd.io/bbolt"
)

// Read actual source metadata, independently of every serving/coverage row.
// A nil Usage is used by the publication observer, which already validated its
// immutable payload. Audits also check referenced file types and lengths.
func (s *Store) sourceAuditNode(key string, usage *objectinventory.Usage) (*auditNode, error) {
	path, err := auditSourcePath(key)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(filepath.Join(s.root, path))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if key[0] != 'r' && !info.IsDir() || key[0] == 'r' && !info.Mode().IsRegular() {
		return nil, ErrCorrupt
	}
	n := &auditNode{Key: key}
	p := strings.Split(key, "/")
	var metadata any
	switch p[0] {
	case "b":
		var b bucketRecord
		if readJSON(filepath.Join(s.root, path, "bucket.json"), &b) != nil || nameHash(b.Name) != p[1] {
			return nil, ErrCorrupt
		}
		if _, err = s.auditBucketRecord(b.Name); err != nil {
			return nil, err
		}
		n.Scope, metadata = catalogScope(p[1], b.Generation), b
	case "o":
		o, readErr := readObject(filepath.Join(s.root, path))
		if errors.Is(readErr, ErrNoObject) {
			return nil, nil
		}
		var b bucketRecord
		if readErr != nil || nameHash(o.Key) != p[2] || readJSON(filepath.Join(s.root, "buckets", p[1], "bucket.json"), &b) != nil || nameHash(b.Name) != p[1] {
			return nil, ErrCorrupt
		}
		if _, err = s.auditBucketRecord(b.Name); err != nil {
			return nil, err
		}
		n.Scope, n.Size, metadata = catalogScope(p[1], b.Generation), o.Size, o
		if usage != nil && inventoryBlob(filepath.Join(s.root, path, o.ID+".data"), o) != nil {
			addInventoryIssue(usage, "missing_or_invalid_object_blob", path, true)
		}
	case "u":
		u, readErr := s.readMultipart(p[1])
		if readErr != nil {
			return nil, readErr
		}
		n.Scope, metadata = catalogScope(nameHash(u.Bucket), u.Generation), u
		n.Active = u.Status == multipartActive || u.Status == multipartCompleting
		if n.Active {
			b, readErr := s.auditBucketRecord(u.Bucket)
			if readErr != nil || b.Generation != u.Generation {
				return nil, ErrCorrupt
			}
		} else if usage != nil && (u.Status == multipartCompleted || u.Status == multipartRejected) {
			r, readErr := s.readReceipt(u.ReceiptID)
			expected := ReceiptCommitted
			if u.Status == multipartRejected {
				expected = ReceiptRejected
			}
			if readErr != nil || r.Kind != "multipart" || r.Bucket != u.Bucket || r.Generation != u.Generation || r.Object.Key != u.Key || r.Status != expected || r.ManifestHash != u.Completion.Hash() {
				addInventoryIssue(usage, "missing_or_invalid_completion_receipt", path, true)
			}
		}
	case "p":
		u, readErr := s.readMultipart(p[1])
		if readErr != nil {
			return nil, readErr
		}
		if u.Status != multipartActive && u.Status != multipartCompleting {
			return nil, nil
		}
		number, readErr := auditPartNumber(p[2])
		if readErr != nil {
			return nil, readErr
		}
		part, readErr := s.readPart(p[1], number)
		if errors.Is(readErr, ErrInvalidPart) {
			return nil, nil
		}
		if readErr != nil || part.Object.Key != u.Key {
			return nil, ErrCorrupt
		}
		n.Scope, n.Size, metadata = catalogScope(nameHash(u.Bucket), u.Generation), part.Object.Size, part
		if usage != nil && inventoryBlob(filepath.Join(s.root, path, part.Object.ID+".data"), part.Object) != nil {
			addInventoryIssue(usage, "missing_or_invalid_part_blob", path, true)
		}
	case "r":
		r, readErr := s.readReceipt(p[1])
		if readErr != nil {
			return nil, readErr
		}
		n.Size, metadata = info.Size(), r
	}
	n.Meta = scrubSeal(metadata)
	return n, nil
}

// Publication observation also runs during ordered journal application, before
// replicationDirty clears. Validate the record without the serving-only gate;
// AuditStep establishes authority/readiness before using this same reader.
func (s *Store) auditBucketRecord(name string) (bucketRecord, error) {
	var b bucketRecord
	if !ValidBucket(name) || !auditRealDirectory(s.bucketPath(name)) || readJSON(filepath.Join(s.bucketPath(name), "bucket.json"), &b) != nil || b.Version != 1 || b.Name != name || uuid.Validate(b.Generation) != nil {
		return b, ErrCorrupt
	}
	return b, nil
}

func (s *Store) auditSourceProjection(tx *bolt.Tx, n auditNode) error {
	p := strings.Split(n.Key, "/")
	switch p[0] {
	case "b":
		if string(tx.Bucket([]byte(catalogBuckets)).Get([]byte(p[1]))) != n.Scope[64:] || tx.Bucket([]byte(catalogObjects)).Bucket([]byte(n.Scope)) == nil || tx.Bucket([]byte(usageMembersBucket)).Bucket([]byte(n.Scope)) == nil {
			return ErrCatalogIndex
		}
	case "o":
		o, err := readObject(filepath.Join(s.root, "buckets", p[1], "objects", p[2]))
		row, exists, readErr := readUsageObject(tx.Bucket([]byte(usageObjectsBucket)), []byte(n.Scope+p[2]))
		child := tx.Bucket([]byte(catalogObjects)).Bucket([]byte(n.Scope))
		if err != nil || readErr != nil || !exists || row.ID != o.ID || row.Size != o.Size || child == nil || string(child.Get([]byte(o.Key))) != o.ID || string(tx.Bucket([]byte(catalogReverse)).Get([]byte(p[1]+p[2]))) != o.Key {
			return ErrCatalogIndex
		}
	case "u":
		up, exists, err := readUsageUpload(tx, p[1])
		if err != nil || exists != n.Active {
			return ErrCatalogIndex
		}
		if n.Active {
			members := tx.Bucket([]byte(usageMembersBucket)).Bucket([]byte(n.Scope))
			if up.Scope != n.Scope || members == nil || !bytes.Equal(members.Get([]byte(p[1])), []byte(p[1])) {
				return ErrCatalogIndex
			}
		}
		parts, err := s.multipartPartsExist(p[1])
		if err != nil {
			return err
		}
		body := tx.Bucket([]byte(catalogUploads)).Get([]byte(p[1]))
		if n.Active || parts {
			var hint catalogUpload
			if body == nil || json.Unmarshal(body, &hint) != nil || hint.Scope != n.Scope || hint.Active != n.Active || hint.Due < 0 || string(tx.Bucket([]byte(catalogDue)).Get(cleanupDueKey(p[1], hint.Due))) != p[1] {
				return ErrCatalogIndex
			}
		} else if body != nil {
			return ErrCatalogIndex
		}
	case "p":
		number, err := auditPartNumber(p[2])
		if err != nil {
			return err
		}
		part, err := s.readPart(p[1], number)
		row, exists, readErr := readUsageObject(tx.Bucket([]byte(usagePartsBucket)), []byte(p[1]+":"+fmtSequence(int64(number))))
		if err != nil || readErr != nil || !exists || row.ID != part.Object.ID || row.Size != part.Object.Size {
			return ErrCatalogIndex
		}
	}
	return nil
}

func (s *Store) auditFixedSource(p *inventoryAuditState, path string) {
	version, err := nodeFormatVersion(s.root)
	if err != nil || version < 3 || version > 5 {
		addInventoryIssue(&p.Status.Usage, "invalid_format_marker", "format.json", true)
		return
	}
	if s.validateRecoveryCatalog(version) != nil {
		addInventoryIssue(&p.Status.Usage, "invalid_recovery_barrier", path, true)
	}
}
