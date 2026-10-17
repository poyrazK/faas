package objectnode

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectrecovery"
	bolt "go.etcd.io/bbolt"
)

// This local projection is excluded from backups and replication. Sealed
// catalog records and the recovery barrier remain the sole deletion authority.
const recoveryIndexName = ".recovery-index.db"
const recoveryDueBucket = "due"
const recoveryIDBucket = "identities"
const recoveryMetaBucket = "checkpoint"

type recoveryIndexPosition struct {
	Version  int    `json:"version"`
	Stream   string `json:"stream"`
	Sequence int64  `json:"sequence"`
	Hash     string `json:"hash"`
	Count    int64  `json:"count"`
	Building bool   `json:"building,omitempty"`
}

type recoveryCleanupIndex struct {
	db       *bolt.DB
	position recoveryIndexPosition
	ready    bool
}

type recoveryIndexEntry struct {
	eligible, check int64 // UTC milliseconds; check may defer a protected proof.
}

func openRecoveryIndex(root string) (*recoveryCleanupIndex, error) {
	path := filepath.Join(root, recoveryIndexName)
	if _, err := os.Lstat(path); err == nil {
		f, err := openBackupFile(path) // Reject symlinks and hard-link aliases.
		if err != nil {
			return nil, err
		}
		info, err := f.Stat()
		_ = f.Close()
		if err != nil {
			return nil, err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Nlink != 1 || info.Mode().Perm()&0o077 != 0 {
			return nil, ErrCorrupt
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: api.ObjectNodeRecoveryIndexOpenTimeout})
	if err != nil {
		return nil, err
	}
	i := &recoveryCleanupIndex{db: db}
	if err = db.Update(func(tx *bolt.Tx) error {
		for _, name := range []string{recoveryDueBucket, recoveryIDBucket, recoveryMetaBucket} {
			if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
				return err
			}
		}
		return nil
	}); err == nil {
		err = db.View(func(tx *bolt.Tx) error {
			body := tx.Bucket([]byte(recoveryMetaBucket)).Get([]byte("position"))
			if body == nil {
				return nil // A new or interrupted initial build is not ready.
			}
			if json.Unmarshal(body, &i.position) != nil || i.position.Version != 1 || !validReceiptID(i.position.Stream) || i.position.Sequence < 0 || !validScrubFindingID(i.position.Hash) || i.position.Count < 0 {
				return ErrCorrupt
			}
			i.ready = !i.position.Building
			return nil
		})
	}
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if err = syncDirectory(root); err != nil {
		_ = db.Close()
		return nil, err
	}
	return i, nil
}

func cleanupDueKey(id string, at int64) []byte {
	key := make([]byte, 8+len(id))
	binary.BigEndian.PutUint64(key, uint64(at))
	copy(key[8:], id)
	return key
}

func encodeCleanupEntry(e recoveryIndexEntry) []byte {
	value := make([]byte, 16)
	binary.BigEndian.PutUint64(value, uint64(e.eligible))
	binary.BigEndian.PutUint64(value[8:], uint64(e.check))
	return value
}

func decodeCleanupEntry(body []byte) (recoveryIndexEntry, error) {
	if len(body) != 16 {
		return recoveryIndexEntry{}, ErrCorrupt
	}
	e := recoveryIndexEntry{int64(binary.BigEndian.Uint64(body)), int64(binary.BigEndian.Uint64(body[8:]))}
	if e.eligible < 1 || e.check < e.eligible {
		return e, ErrCorrupt
	}
	return e, nil
}

func (i *recoveryCleanupIndex) entry(id string) (recoveryIndexEntry, bool, error) {
	var e recoveryIndexEntry
	var found bool
	err := i.db.View(func(tx *bolt.Tx) error {
		body := tx.Bucket([]byte(recoveryIDBucket)).Get([]byte(id))
		if body == nil {
			return nil
		}
		var err error
		e, err = decodeCleanupEntry(body)
		found = err == nil
		return err
	})
	return e, found, err
}

// put preserves an existing backoff when eligibility has not changed.
func (i *recoveryCleanupIndex) put(id string, eligible time.Time) error {
	old, exists, err := i.entry(id)
	if err != nil {
		return err
	}
	at := eligible.UnixMilli()
	if exists && old.eligible == at {
		return nil
	}
	return i.replace(id, recoveryIndexEntry{at, at}, false)
}

func (i *recoveryCleanupIndex) remove(id string) error {
	_, exists, err := i.entry(id)
	if err != nil || !exists {
		return err
	}
	return i.replace(id, recoveryIndexEntry{}, true)
}

func (i *recoveryCleanupIndex) deferEntry(id string, until time.Time) error {
	e, exists, err := i.entry(id)
	if err != nil || !exists {
		return err
	}
	e.check = max(e.eligible, until.UnixMilli())
	return i.replace(id, e, false)
}

func (i *recoveryCleanupIndex) replace(id string, next recoveryIndexEntry, remove bool) error {
	if _, ok := objectrecovery.IssuedAt(id); !ok || !remove && (next.eligible < 1 || next.check < next.eligible) {
		return ErrCorrupt
	}
	position := i.position
	err := i.db.Update(func(tx *bolt.Tx) error {
		if err := replaceCleanupEntry(tx, &position, id, next, remove); err != nil {
			return err
		}
		return putCleanupPosition(tx, position)
	})
	if err == nil {
		i.position = position
	}
	return err
}

func replaceCleanupEntry(tx *bolt.Tx, position *recoveryIndexPosition, id string, next recoveryIndexEntry, remove bool) error {
	identities, due := tx.Bucket([]byte(recoveryIDBucket)), tx.Bucket([]byte(recoveryDueBucket))
	if body := identities.Get([]byte(id)); body != nil {
		old, err := decodeCleanupEntry(body)
		if err != nil {
			return err
		}
		if err = due.Delete(cleanupDueKey(id, old.check)); err != nil {
			return err
		}
		position.Count--
	}
	if remove {
		if err := identities.Delete([]byte(id)); err != nil {
			return err
		}
	} else {
		if err := identities.Put([]byte(id), encodeCleanupEntry(next)); err != nil {
			return err
		}
		if err := due.Put(cleanupDueKey(id, next.check), []byte(id)); err != nil {
			return err
		}
		position.Count++
	}
	return nil
}

type recoveryIndexCandidate struct {
	id       string
	eligible time.Time
}

// Startup batches bound transaction memory and leave Building set until the
// complete catalog projection and its source checkpoint have been synced.
func (i *recoveryCleanupIndex) addBatch(candidates []recoveryIndexCandidate) error {
	position := i.position
	err := i.db.Update(func(tx *bolt.Tx) error {
		for _, c := range candidates {
			at := c.eligible.UnixMilli()
			if err := replaceCleanupEntry(tx, &position, c.id, recoveryIndexEntry{at, at}, false); err != nil {
				return err
			}
		}
		return putCleanupPosition(tx, position)
	})
	if err == nil {
		i.position = position
	}
	return err
}

func putCleanupPosition(tx *bolt.Tx, position recoveryIndexPosition) error {
	if position.Version != 1 || position.Count < 0 {
		return ErrCorrupt
	}
	body, err := json.Marshal(position)
	if err != nil {
		return err
	}
	return tx.Bucket([]byte(recoveryMetaBucket)).Put([]byte("position"), body)
}

func (i *recoveryCleanupIndex) savePosition() error {
	return i.db.Update(func(tx *bolt.Tx) error { return putCleanupPosition(tx, i.position) })
}

// first is a B+tree seek and two point reads, independent of catalog size.
func (i *recoveryCleanupIndex) first() (id string, at time.Time, err error) {
	err = i.db.View(func(tx *bolt.Tx) error {
		key, value := tx.Bucket([]byte(recoveryDueBucket)).Cursor().First()
		if key == nil {
			if i.position.Count != 0 {
				return ErrCorrupt
			}
			return nil
		}
		if len(key) != 44 || string(key[8:]) != string(value) {
			return ErrCorrupt
		}
		id = string(value)
		if _, ok := objectrecovery.IssuedAt(id); !ok {
			return ErrCorrupt
		}
		e, err := decodeCleanupEntry(tx.Bucket([]byte(recoveryIDBucket)).Get(value))
		if err != nil || e.check != int64(binary.BigEndian.Uint64(key)) {
			return ErrCorrupt
		}
		at = time.UnixMilli(e.check).UTC()
		return nil
	})
	return id, at, err
}
