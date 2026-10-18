package objectnode

import (
	"encoding/json"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectinventory"
	bolt "go.etcd.io/bbolt"
)

const (
	usageTotalsBucket  = "usage-totals"
	usageObjectsBucket = "usage-objects"
	usageUploadsBucket = "usage-uploads"
	usageMembersBucket = "usage-members"
	usagePartsBucket   = "usage-parts"
	usageGlobalKey     = "total"
)

func requireUsageTables(tx *bolt.Tx) error {
	for _, name := range []string{catalogBuckets, catalogObjects, catalogReverse, catalogUploads, catalogDue, catalogCounts, usageTotalsBucket, usageObjectsBucket, usageUploadsBucket, usageMembersBucket, usagePartsBucket} {
		if tx.Bucket([]byte(name)) == nil {
			return ErrCatalogIndex
		}
	}
	return nil
}

type usageRecord struct {
	Scope string `json:"scope"`
	objectinventory.Totals
	Seal string `json:"seal"`
}

type usageObject struct {
	ID   string `json:"id"`
	Size int64  `json:"size"`
	Seal string `json:"seal"`
}

type usageUpload struct {
	ID    string `json:"id"`
	Scope string `json:"scope"`
	Parts int64  `json:"parts"`
	Bytes int64  `json:"bytes"`
	Seal  string `json:"seal"`
}

func sealUsage(r usageRecord) usageRecord       { r.Seal = ""; r.Seal = scrubSeal(r); return r }
func sealUsageObject(r usageObject) usageObject { r.Seal = ""; r.Seal = scrubSeal(r); return r }
func sealUsageUpload(r usageUpload) usageUpload { r.Seal = ""; r.Seal = scrubSeal(r); return r }

func validUsageTotals(t objectinventory.Totals) bool {
	for _, n := range []int64{t.Objects, t.CommittedBytes, t.MultipartUploads, t.MultipartParts, t.MultipartBytes} {
		if n < 0 || n > api.MaxObjectStoragePolicyValue {
			return false
		}
	}
	return (t.Objects != 0 || t.CommittedBytes == 0) && (t.MultipartUploads != 0 || t.MultipartParts == 0 && t.MultipartBytes == 0) && (t.MultipartParts != 0 || t.MultipartBytes == 0)
}

func readUsage(tx *bolt.Tx, scope string) (usageRecord, error) {
	var r usageRecord
	if err := requireUsageTables(tx); err != nil {
		return r, err
	}
	body := tx.Bucket([]byte(usageTotalsBucket)).Get([]byte(scope))
	if body == nil || decodeBackupRecord(body, &r) != nil || r.Scope != scope || r.Seal != sealUsage(r).Seal || !validUsageTotals(r.Totals) {
		return r, ErrCatalogIndex
	}
	return r, nil
}

func writeUsage(tx *bolt.Tx, r usageRecord) error {
	if !validUsageTotals(r.Totals) {
		return ErrCatalogIndex
	}
	body, err := json.Marshal(sealUsage(r))
	if err != nil {
		return err
	}
	return tx.Bucket([]byte(usageTotalsBucket)).Put([]byte(r.Scope), body)
}

func createUsageScope(tx *bolt.Tx, scope string) error {
	if tx.Bucket([]byte(usageTotalsBucket)).Get([]byte(scope)) == nil {
		if err := writeUsage(tx, usageRecord{Scope: scope}); err != nil {
			return err
		}
	}
	_, err := tx.Bucket([]byte(usageMembersBucket)).CreateBucketIfNotExists([]byte(scope))
	return err
}

func changeUsage(tx *bolt.Tx, scope string, delta objectinventory.Totals) error {
	for _, key := range []string{scope, usageGlobalKey} {
		r, err := readUsage(tx, key)
		if err != nil {
			return err
		}
		for _, pair := range []struct {
			value  *int64
			change int64
		}{{&r.Objects, delta.Objects}, {&r.CommittedBytes, delta.CommittedBytes}, {&r.MultipartUploads, delta.MultipartUploads}, {&r.MultipartParts, delta.MultipartParts}, {&r.MultipartBytes, delta.MultipartBytes}} {
			if pair.change > 0 && *pair.value > api.MaxObjectStoragePolicyValue-pair.change || pair.change < 0 && *pair.value < -pair.change {
				return ErrCatalogIndex
			}
			*pair.value += pair.change
		}
		if err = writeUsage(tx, r); err != nil {
			return err
		}
	}
	return nil
}

func readUsageObject(b *bolt.Bucket, key []byte) (usageObject, bool, error) {
	var o usageObject
	if b == nil {
		return o, false, ErrCatalogIndex
	}
	body := b.Get(key)
	if body == nil {
		return o, false, nil
	}
	if decodeBackupRecord(body, &o) != nil || !validReceiptID(o.ID) || o.Size < 0 || o.Size > api.MaxObjectUploadBytes || o.Seal != sealUsageObject(o).Seal {
		return o, false, ErrCatalogIndex
	}
	return o, true, nil
}

func putUsageObject(tx *bolt.Tx, scope, hash string, o Object) error {
	b := tx.Bucket([]byte(usageObjectsBucket))
	key := []byte(scope + hash)
	old, exists, err := readUsageObject(b, key)
	if err != nil || exists && old.ID == o.ID && old.Size != o.Size {
		return ErrCatalogIndex
	}
	delta := objectinventory.Totals{CommittedBytes: o.Size - old.Size}
	if !exists {
		delta.Objects = 1
	}
	if err = changeUsage(tx, scope, delta); err != nil {
		return err
	}
	body, _ := json.Marshal(sealUsageObject(usageObject{ID: o.ID, Size: o.Size}))
	return b.Put(key, body)
}

func removeUsageObject(tx *bolt.Tx, scope, hash string) error {
	b := tx.Bucket([]byte(usageObjectsBucket))
	key := []byte(scope + hash)
	old, exists, err := readUsageObject(b, key)
	if err != nil || !exists {
		return errors.Join(err, ErrCatalogIndex)
	}
	if err = changeUsage(tx, scope, objectinventory.Totals{Objects: -1, CommittedBytes: -old.Size}); err != nil {
		return err
	}
	return b.Delete(key)
}

func readUsageUpload(tx *bolt.Tx, id string) (usageUpload, bool, error) {
	var u usageUpload
	if err := requireUsageTables(tx); err != nil {
		return u, false, err
	}
	body := tx.Bucket([]byte(usageUploadsBucket)).Get([]byte(id))
	if body == nil {
		return u, false, nil
	}
	if decodeBackupRecord(body, &u) != nil || !validReceiptID(id) || u.ID != id || len(u.Scope) != 100 || u.Parts < 0 || u.Parts > api.MaxMultipartParts || u.Bytes < 0 || u.Bytes > api.MaxObjectUploadBytes || u.Seal != sealUsageUpload(u).Seal {
		return u, false, ErrCatalogIndex
	}
	return u, true, nil
}

func putUsageUpload(tx *bolt.Tx, u MultipartUpload) error {
	scope := catalogScope(nameHash(u.Bucket), u.Generation)
	old, exists, err := readUsageUpload(tx, u.ID)
	if err != nil || exists && old.Scope != scope {
		return ErrCatalogIndex
	}
	active := u.Status == multipartActive || u.Status == multipartCompleting
	var members *bolt.Bucket
	if active && !exists || !active && exists {
		members = tx.Bucket([]byte(usageMembersBucket)).Bucket([]byte(scope))
		if members == nil {
			return ErrCatalogIndex
		}
	}
	if active && !exists {
		if err = changeUsage(tx, scope, objectinventory.Totals{MultipartUploads: 1}); err != nil {
			return err
		}
		body, _ := json.Marshal(sealUsageUpload(usageUpload{ID: u.ID, Scope: scope}))
		if err = tx.Bucket([]byte(usageUploadsBucket)).Put([]byte(u.ID), body); err != nil {
			return err
		}
		return members.Put([]byte(u.ID), []byte(u.ID))
	}
	if !active && exists {
		if err = changeUsage(tx, scope, objectinventory.Totals{MultipartUploads: -1, MultipartParts: -old.Parts, MultipartBytes: -old.Bytes}); err != nil {
			return err
		}
		if err = tx.Bucket([]byte(usageUploadsBucket)).Delete([]byte(u.ID)); err != nil {
			return err
		}
		return members.Delete([]byte(u.ID))
	}
	return nil
}

func putUsagePart(tx *bolt.Tx, u MultipartUpload, p MultipartPart) error {
	up, exists, err := readUsageUpload(tx, u.ID)
	if err != nil || !exists {
		return errors.Join(err, ErrCatalogIndex)
	}
	b := tx.Bucket([]byte(usagePartsBucket))
	key := []byte(u.ID + ":" + fmtSequence(int64(p.Number)))
	old, replaced, err := readUsageObject(b, key)
	if err != nil || replaced && old.ID == p.Object.ID && old.Size != p.Object.Size {
		return ErrCatalogIndex
	}
	delta := objectinventory.Totals{MultipartBytes: p.Object.Size - old.Size}
	if !replaced {
		delta.MultipartParts = 1
	}
	up.Parts += delta.MultipartParts
	up.Bytes += delta.MultipartBytes
	if up.Parts > api.MaxMultipartParts || up.Bytes < 0 || up.Bytes > u.MaxBytes {
		return ErrCatalogIndex
	}
	if err = changeUsage(tx, up.Scope, delta); err != nil {
		return err
	}
	body, _ := json.Marshal(sealUsageUpload(up))
	if err = tx.Bucket([]byte(usageUploadsBucket)).Put([]byte(u.ID), body); err != nil {
		return err
	}
	body, _ = json.Marshal(sealUsageObject(usageObject{ID: p.Object.ID, Size: p.Object.Size}))
	return b.Put(key, body)
}
