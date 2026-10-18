package objectnode

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectinventory"
)

type InventoryRequest struct {
	Kind, Cursor string
	Limit        int32
}

type inventoryCursor struct {
	Kind, Bucket, Generation, Snapshot, After string
	ObservedAt                                time.Time
}

func (s *Store) inventorySnapshot() string {
	return s.inventoryBoot + ":" + strconv.FormatUint(s.inventoryEpoch, 10)
}

// Inventory holds the catalog read lock for each bounded-memory scan. It never
// settles a receipt, deletes a file, or publishes a catalog change.
func (s *Store) Inventory(ctx context.Context, bucket string, r InventoryRequest) (objectinventory.Page, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return objectinventory.Page{}, ErrClosed
	}
	if s.pending != nil {
		return objectinventory.Page{}, ErrCorrupt
	}
	if r.Limit < 1 || r.Limit > api.MaxObjectS3ListItems || len(r.Cursor) > api.MaxObjectS3ListCursorBytes {
		return objectinventory.Page{}, ErrInvalid
	}
	p := objectinventory.Page{Version: objectinventory.Version, Kind: r.Kind, Bucket: bucket, Snapshot: s.inventorySnapshot(), ObservedAt: time.Now().UTC(), Buckets: []objectinventory.Bucket{}, Objects: []objectinventory.Object{}, Uploads: []objectinventory.Upload{}}
	if r.Kind == objectinventory.Buckets {
		if bucket != "" {
			return p, ErrInvalid
		}
	} else {
		if r.Kind != objectinventory.Objects && r.Kind != objectinventory.Uploads {
			return p, ErrInvalid
		}
		b, err := s.bucket(bucket)
		if err != nil {
			return p, err
		}
		p.Generation = b.Generation
	}
	after, err := inventoryBoundary(&p, r.Cursor)
	if err != nil {
		return objectinventory.Page{}, err
	}
	last, err := s.inventoryEntries(ctx, &p, after, int(r.Limit))
	if err != nil {
		return objectinventory.Page{}, err
	}
	if last != "" {
		b, _ := json.Marshal(inventoryCursor{Kind: p.Kind, Bucket: bucket, Generation: p.Generation, Snapshot: p.Snapshot, ObservedAt: p.ObservedAt, After: last})
		p.NextCursor = base64.RawURLEncoding.EncodeToString(b)
	}
	return p, nil
}

func inventoryBoundary(p *objectinventory.Page, cursor string) (string, error) {
	if cursor == "" {
		return "", nil
	}
	data, err := base64.RawURLEncoding.DecodeString(cursor)
	var c inventoryCursor
	if err != nil || json.Unmarshal(data, &c) != nil || c.After == "" || c.Kind != p.Kind || c.Bucket != p.Bucket {
		return "", ErrInvalid
	}
	if c.Generation != p.Generation || c.Snapshot != p.Snapshot || c.ObservedAt.IsZero() || c.ObservedAt.After(p.ObservedAt) || p.ObservedAt.Sub(c.ObservedAt) > time.Duration(api.ObjectStorageInventoryMaxAgeSeconds)*time.Second {
		return "", ErrInventoryChanged
	}
	p.ObservedAt = c.ObservedAt
	return c.After, nil
}

func keepInventory[T any](items []T, item T, limit int, identity func(T) string) []T {
	i := sort.Search(len(items), func(i int) bool { return identity(items[i]) >= identity(item) })
	if i >= limit {
		return items
	}
	items = append(items, item)
	copy(items[i+1:], items[i:])
	items[i] = item
	return items[:min(len(items), limit)]
}

func (s *Store) inventoryEntries(ctx context.Context, p *objectinventory.Page, after string, limit int) (string, error) {
	directory := filepath.Join(s.root, "buckets")
	if p.Kind == objectinventory.Objects {
		directory = filepath.Join(s.bucketPath(p.Bucket), "objects")
	}
	if p.Kind == objectinventory.Uploads {
		directory = filepath.Join(s.root, "multipart")
	}
	err := visitDirectory(directory, func(e os.DirEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !e.IsDir() {
			return ErrCorrupt
		}
		switch p.Kind {
		case objectinventory.Buckets:
			var b bucketRecord
			if err := readJSON(filepath.Join(directory, e.Name(), "bucket.json"), &b); err != nil {
				return ErrCorrupt
			}
			if nameHash(b.Name) != e.Name() {
				return ErrCorrupt
			}
			if _, err := s.bucket(b.Name); err != nil {
				return err
			}
			if b.Name > after {
				p.Buckets = keepInventory(p.Buckets, objectinventory.Bucket{Name: b.Name, Generation: b.Generation, CreatedAt: b.CreatedAt}, limit+1, func(b objectinventory.Bucket) string { return b.Name })
			}
		case objectinventory.Objects:
			dir := filepath.Join(directory, e.Name())
			o, err := readObject(dir)
			if errors.Is(err, ErrNoObject) {
				return nil
			}
			if err != nil {
				return err
			}
			if err = inventoryBlob(filepath.Join(dir, o.ID+".data"), o); err != nil {
				return err
			}
			if o.Key > after {
				p.Objects = keepInventory(p.Objects, objectinventory.Object{Key: o.Key, ID: o.ID, Size: o.Size, ETag: o.ETag, SHA256: o.SHA256, Modified: o.Modified}, limit+1, func(o objectinventory.Object) string { return o.Key })
			}
		case objectinventory.Uploads:
			u, err := s.readMultipart(e.Name())
			if errors.Is(err, ErrNoUpload) {
				return nil
			}
			if err != nil {
				return err
			}
			if u.Bucket != p.Bucket || u.Generation != p.Generation || u.Status != multipartActive && u.Status != multipartCompleting {
				return nil
			}
			item, err := s.inventoryUpload(ctx, u)
			if err != nil {
				return err
			}
			if uploadInventoryIdentity(item) > after {
				p.Uploads = keepInventory(p.Uploads, item, limit+1, uploadInventoryIdentity)
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(p.Buckets) > limit {
		p.Buckets = p.Buckets[:limit]
		return p.Buckets[limit-1].Name, nil
	}
	if len(p.Objects) > limit {
		p.Objects = p.Objects[:limit]
		return p.Objects[limit-1].Key, nil
	}
	if len(p.Uploads) > limit {
		p.Uploads = p.Uploads[:limit]
		return uploadInventoryIdentity(p.Uploads[limit-1]), nil
	}
	return "", nil
}

func uploadInventoryIdentity(u objectinventory.Upload) string { return u.Key + "\x00" + u.ID }

func inventoryBlob(path string, o Object) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != o.Size {
		return ErrCorrupt
	}
	return nil
}

func (s *Store) inventoryUpload(ctx context.Context, u MultipartUpload) (objectinventory.Upload, error) {
	item := objectinventory.Upload{ID: u.ID, Key: u.Key, Status: u.Status, CreatedAt: u.CreatedAt, ExpiresAt: u.ExpiresAt}
	err := visitDirectory(filepath.Join(s.multipartPath(u.ID), "parts"), func(e os.DirEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		number, err := strconv.ParseInt(e.Name(), 10, 32)
		if err != nil || strconv.FormatInt(number, 10) != e.Name() || number < 1 || number > api.MaxMultipartParts || !e.IsDir() {
			return ErrCorrupt
		}
		part, err := s.readPart(u.ID, int32(number))
		if errors.Is(err, ErrInvalidPart) {
			return nil
		}
		if err != nil {
			return err
		}
		if part.Object.Key != u.Key || part.Object.Size > u.MaxBytes-item.Size {
			return ErrCorrupt
		}
		if err = inventoryBlob(filepath.Join(s.partPath(u.ID, int32(number)), part.Object.ID+".data"), part.Object); err != nil {
			return err
		}
		item.Parts++
		item.Size += part.Object.Size
		return nil
	})
	return item, err
}
