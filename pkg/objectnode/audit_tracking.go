package objectnode

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectmultipart"
	bolt "go.etcd.io/bbolt"
)

const (
	auditSource      = "audit-source"
	auditSourceParts = "audit-source-parts"
	auditSeen        = "audit-seen"
	auditSeenParts   = "audit-seen-parts"
	auditScopes      = "audit-scopes"
	auditDirty       = "audit-dirty"
)

var auditTables = []string{auditSource, auditSourceParts, auditSeen, auditSeenParts, auditScopes, auditDirty}

// The expected root and key live only in this process. Disk rows are enumeration
// hints; neither a copied checkpoint nor a missing row can establish coverage.
type auditSet struct {
	Hash  [32]byte
	Count int64
}

type auditNode struct {
	Key, Scope, Meta string
	Size             int64
	Active           bool
	Parts            auditSet
	PartBytes        int64
}

type auditEnvelope struct {
	Body json.RawMessage
	MAC  string
}

type auditChanges struct {
	Active, Gap    bool
	Limit, Pending int
	Bytes          int64
	Since          time.Time
}

func (s *Store) auditMAC(key string, body []byte) []byte {
	h := hmac.New(sha256.New, s.auditKey[:])
	_, _ = h.Write([]byte(key))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(body)
	return h.Sum(nil)
}

func (s *Store) putAuditRow(tx *bolt.Tx, table, key string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	envelope, err := json.Marshal(auditEnvelope{Body: body, MAC: hex.EncodeToString(s.auditMAC(table+"/"+key, body))})
	if err != nil {
		return err
	}
	b := tx.Bucket([]byte(table))
	if b == nil {
		return ErrCatalogIndex
	}
	return b.Put([]byte(key), envelope)
}

func (s *Store) readAuditRow(tx *bolt.Tx, table, key string, value any) (bool, error) {
	b := tx.Bucket([]byte(table))
	if b == nil {
		return false, ErrCatalogIndex
	}
	body := b.Get([]byte(key))
	if body == nil {
		return false, nil
	}
	var e auditEnvelope
	if decodeBackupRecord(body, &e) != nil {
		return false, ErrCorrupt
	}
	mac, err := hex.DecodeString(e.MAC)
	if err != nil || !hmac.Equal(mac, s.auditMAC(table+"/"+key, e.Body)) || decodeBackupRecord(e.Body, value) != nil {
		return false, ErrCorrupt
	}
	return true, nil
}

// Addition modulo 2^256, with a separate cardinality. XOR would allow even
// multiplicities to cancel and is unsuitable for this completeness anchor.
func (s *Store) changeAuditSet(set *auditSet, node auditNode, add bool) error {
	body, err := json.Marshal(node)
	if err != nil {
		return err
	}
	digest := s.auditMAC("identity", body)
	carry := 0
	for i := len(set.Hash) - 1; i >= 0; i-- {
		n := int(set.Hash[i]) + carry
		if add {
			n += int(digest[i])
		} else {
			n -= int(digest[i])
		}
		set.Hash[i], carry = byte(n&255), n>>8
	}
	if add {
		set.Count++
	} else {
		set.Count--
	}
	if set.Count < 0 || set.Count > api.MaxObjectStoragePolicyValue {
		return ErrCatalogIndex
	}
	return nil
}

func (s *Store) replaceAuditSource(tx *bolt.Tx, root *auditSet, key string, next *auditNode) error {
	var old auditNode
	exists, err := s.readAuditRow(tx, auditSource, key, &old)
	if err != nil {
		return err
	}
	if exists {
		if old.Key != key {
			return ErrCorrupt
		}
		if err = s.changeAuditSet(root, old, false); err != nil {
			return err
		}
	}
	if next == nil {
		return tx.Bucket([]byte(auditSource)).Delete([]byte(key))
	}
	if err = s.changeAuditSet(root, *next, true); err != nil {
		return err
	}
	return s.putAuditRow(tx, auditSource, key, next)
}

func (s *Store) trackAuditPart(tx *bolt.Tx, root *auditSet, node auditNode) error {
	id := strings.Split(node.Key, "/")[1]
	var parent, old auditNode
	exists, err := s.readAuditRow(tx, auditSource, "u/"+id, &parent)
	if err != nil || !exists || !parent.Active {
		return errors.Join(err, ErrCatalogIndex)
	}
	exists, err = s.readAuditRow(tx, auditSourceParts, node.Key, &old)
	if err != nil {
		return err
	}
	if exists {
		if err = s.changeAuditSet(&parent.Parts, old, false); err != nil {
			return err
		}
		parent.PartBytes -= old.Size
	}
	if err = s.changeAuditSet(&parent.Parts, node, true); err != nil {
		return err
	}
	if err = addInventoryBytes(&parent.PartBytes, node.Size); err != nil {
		return err
	}
	if err = s.putAuditRow(tx, auditSourceParts, node.Key, node); err != nil {
		return err
	}
	return s.replaceAuditSource(tx, root, parent.Key, &parent)
}

func (s *Store) readAuditChanges(tx *bolt.Tx) (auditChanges, error) {
	var m auditChanges
	exists, err := s.readAuditRow(tx, inventoryAuditBucket, "coverage", &m)
	body := tx.Bucket([]byte(inventoryAuditBucket)).Get([]byte("coverage"))
	if err != nil || !exists || nameHash(string(body)) != s.auditMetaProof || m.Limit < 1 || m.Limit > api.ObjectNodeAuditMaxPendingRecords || m.Pending < 0 || m.Pending > api.ObjectNodeAuditMaxPendingRecords || m.Bytes < 0 || m.Bytes > api.ObjectNodeAuditPendingBytes {
		return m, errors.Join(err, ErrCatalogIndex)
	}
	return m, nil
}

func (s *Store) writeAuditChanges(tx *bolt.Tx, m auditChanges) (string, error) {
	if err := s.putAuditRow(tx, inventoryAuditBucket, "coverage", m); err != nil {
		return "", err
	}
	return nameHash(string(tx.Bucket([]byte(inventoryAuditBucket)).Get([]byte("coverage")))), nil
}

func (s *Store) queueAuditChange(tx *bolt.Tx, m *auditChanges, key string) error {
	if !m.Active || m.Gap {
		return nil
	}
	b := tx.Bucket([]byte(auditDirty))
	if b == nil {
		return ErrCatalogIndex
	}
	if b.Get([]byte(key)) != nil {
		var value string
		exists, err := s.readAuditRow(tx, auditDirty, key, &value)
		if err != nil || !exists || value != key {
			return ErrCorrupt
		}
		return nil
	}
	if m.Pending >= m.Limit {
		m.Gap = true
		return nil
	}
	if err := s.putAuditRow(tx, auditDirty, key, key); err != nil {
		return err
	}
	size := int64(len(key) + len(b.Get([]byte(key))))
	if size > api.ObjectNodeAuditPendingBytes-m.Bytes {
		m.Gap = true
		return b.Delete([]byte(key))
	}
	if m.Pending == 0 {
		m.Since = time.Now().UTC()
	}
	m.Pending++
	m.Bytes += size
	return nil
}

func (s *Store) cacheAuditChanges(m auditChanges, proof string) {
	s.auditMetaProof = proof
	s.auditPending.Store(int64(m.Pending))
	if m.Since.IsZero() {
		s.auditBacklogSince.Store(0)
	} else {
		s.auditBacklogSince.Store(m.Since.UnixNano())
	}
}

func (s *Store) initializeAuditTracking() error {
	if _, err := rand.Read(s.auditKey[:]); err != nil {
		return err
	}
	s.auditSourceRoot = auditSet{}
	err := s.catalogIndex.db.Update(func(tx *bolt.Tx) error {
		proof, err := s.writeAuditChanges(tx, auditChanges{Limit: api.ObjectNodeAuditPendingRecords})
		if err == nil {
			s.auditMetaProof = proof
		}
		return err
	})
	s.auditTrackedEpoch = s.inventoryEpoch
	return err
}

// Every observer runs after durable publication under mu. Updating these rows
// shares the serving projection's transaction; failure disables that projection.
func (s *Store) observeAuditPath(tx *bolt.Tx, root *auditSet, m *auditChanges, path string) error {
	p := strings.Split(filepath.ToSlash(path), "/")
	key := ""
	switch {
	case p[0] == "buckets" && len(p) >= 2:
		key = "b/" + p[1]
		if len(p) >= 4 && p[2] == "objects" {
			key = "o/" + p[1] + "/" + p[3]
		}
	case p[0] == "multipart" && len(p) >= 2:
		key = "u/" + p[1]
		if len(p) >= 4 && p[2] == "parts" {
			// Removing obsolete terminal part directories needs no logical delta.
			var parent auditNode
			exists, err := s.readAuditRow(tx, auditSource, key, &parent)
			if err != nil {
				return err
			}
			if exists && parent.Active {
				key = "p/" + p[1] + "/" + p[3]
			}
		}
	case p[0] == "receipts" && len(p) == 2 && strings.HasSuffix(p[1], ".json"):
		key = "r/" + strings.TrimSuffix(p[1], ".json")
	case path == "recovery.json":
		r, err := s.readRecoveryRetention()
		if err != nil {
			return err
		}
		if r.Retirement != nil {
			if r.Retirement.SessionSeal != "" {
				if err = s.observeAuditPath(tx, root, m, "multipart/"+objectmultipart.NativeUploadID(r.Retirement.ID)+"/session.json"); err != nil {
					return err
				}
			}
			if r.Retirement.ReceiptSeal != "" {
				if err = s.observeAuditPath(tx, root, m, "receipts/"+r.Retirement.ID+".json"); err != nil {
					return err
				}
			}
		}
		return s.queueAuditChange(tx, m, "0/f/recovery.json")
	case path == "format.json":
		return s.queueAuditChange(tx, m, "0/f/format.json")
	}
	if key == "" {
		return nil
	}
	var old auditNode
	_, err := s.readAuditRow(tx, auditSource, key, &old)
	if err != nil {
		return err
	}
	next, err := s.sourceAuditNode(key, nil)
	if err != nil {
		return err
	}
	if strings.HasPrefix(key, "p/") {
		if next == nil {
			return ErrCatalogIndex
		}
		if err = s.trackAuditPart(tx, root, *next); err != nil {
			return err
		}
	} else {
		if next != nil && next.Active && old.Active {
			next.Parts, next.PartBytes = old.Parts, old.PartBytes
		}
		if err = s.replaceAuditSource(tx, root, key, next); err != nil {
			return err
		}
	}
	if err = s.queueAuditChange(tx, m, "0/"+key); err != nil {
		return err
	}
	if key[0] == 'u' || key[0] == 'p' {
		if err = s.queueAuditChange(tx, m, "1u/"+strings.Split(key, "/")[1]); err != nil {
			return err
		}
	}
	for _, scope := range []string{old.Scope, func() string {
		if next != nil {
			return next.Scope
		}
		return ""
	}()} {
		if scope != "" {
			if err = s.queueAuditChange(tx, m, "1/"+scope); err != nil {
				return err
			}
		}
	}
	return nil
}

func auditPartNumber(value string) (int32, error) {
	n, err := strconv.ParseInt(value, 10, 32)
	if err != nil || n < 1 || n > api.MaxMultipartParts || strconv.FormatInt(n, 10) != value {
		return 0, ErrCorrupt
	}
	return int32(n), nil
}

func auditSourcePath(key string) (string, error) {
	p := strings.Split(key, "/")
	switch {
	case len(p) == 2 && p[0] == "b" && validScrubFindingID(p[1]):
		return filepath.Join("buckets", p[1]), nil
	case len(p) == 3 && p[0] == "o" && validScrubFindingID(p[1]) && validScrubFindingID(p[2]):
		return filepath.Join("buckets", p[1], "objects", p[2]), nil
	case len(p) == 2 && p[0] == "u" && validReceiptID(p[1]):
		return filepath.Join("multipart", p[1]), nil
	case len(p) == 2 && p[0] == "r" && validReceiptID(p[1]):
		return filepath.Join("receipts", p[1]+".json"), nil
	case len(p) == 3 && p[0] == "p" && validReceiptID(p[1]):
		if _, err := auditPartNumber(p[2]); err != nil {
			return "", err
		}
		return filepath.Join("multipart", p[1], "parts", p[2]), nil
	}
	return "", fmt.Errorf("invalid source identity: %w", ErrCorrupt)
}

func (s *Store) buildAuditReceipts() error {
	return visitDirectory(filepath.Join(s.root, "receipts"), func(e os.DirEntry) error {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".json") {
			return ErrCorrupt
		}
		n, err := s.sourceAuditNode("r/"+strings.TrimSuffix(e.Name(), ".json"), nil)
		if err != nil || n == nil {
			return errors.Join(err, ErrCorrupt)
		}
		return s.catalogIndex.db.Update(func(tx *bolt.Tx) error { return s.replaceAuditSource(tx, &s.auditSourceRoot, n.Key, n) })
	})
}
