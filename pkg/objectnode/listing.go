package objectnode

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	bolt "go.etcd.io/bbolt"
)

type ListRequest struct {
	Prefix, Delimiter, Cursor, StartAfter string
	Limit                                 int32
}

type ListEntry struct {
	Object
	CommonPrefix bool
}

type Page struct {
	Entries    []ListEntry
	NextCursor string
}

type listCursor struct {
	Bucket, Prefix, Delimiter, After string
	Generation                       string `json:"Generation,omitempty"`
}

func listBoundary(bucket, generation string, r ListRequest) (string, error) {
	if r.Limit < 0 || r.Limit > api.MaxObjectS3ListItems ||
		r.Prefix != "" && !validKey(r.Prefix) || r.StartAfter != "" && !validKey(r.StartAfter) ||
		r.Delimiter != "" && r.Delimiter != "/" || len(r.Cursor) > api.MaxObjectS3ListCursorBytes {
		return "", ErrInvalid
	}
	if r.Cursor == "" {
		return r.StartAfter, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(r.Cursor)
	var c listCursor
	if err != nil || json.Unmarshal(b, &c) != nil || c.Bucket != bucket || c.Prefix != r.Prefix || c.Delimiter != r.Delimiter || !validKey(c.After) || c.Generation != "" && c.Generation != generation {
		return "", ErrInvalid
	}
	return c.After, nil
}

// List seeks an ordered key projection and reads at most Limit+1 source records
// (plus one boundary record). Each page is coherent under mu; tokens resume by
// key across concurrent changes, rather than pinning a catalog snapshot.
func (s *Store) List(ctx context.Context, bucket string, r ListRequest) (Page, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return Page{}, ErrClosed
	}
	if s.pending != nil {
		return Page{}, ErrCorrupt
	}
	bucketInfo, err := s.bucket(bucket)
	if err != nil {
		return Page{}, err
	}
	after, err := listBoundary(bucket, bucketInfo.Generation, r)
	if err != nil || r.Limit == 0 {
		return Page{}, err
	}
	if err = s.requireCatalogIndex(); err != nil {
		return Page{}, err
	}
	page := Page{}
	err = s.catalogIndex.db.View(func(tx *bolt.Tx) error {
		var scanErr error
		page.Entries, scanErr = s.listIndexed(ctx, tx, bucketInfo, r, after)
		return scanErr
	})
	if err != nil {
		if errors.Is(err, ErrCatalogIndex) && s.catalogIndex.failed.CompareAndSwap(false, true) {
			s.catalogReadFailures.Add(1)
		}
		return Page{}, err
	}
	if len(page.Entries) > int(r.Limit) {
		page.Entries = page.Entries[:r.Limit]
		b, _ := json.Marshal(listCursor{Bucket: bucket, Prefix: r.Prefix, Delimiter: r.Delimiter, After: page.Entries[len(page.Entries)-1].Key, Generation: bucketInfo.Generation})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(b)
	}
	return page, nil
}

func (s *Store) listIndexed(ctx context.Context, tx *bolt.Tx, b bucketRecord, r ListRequest, after string) ([]ListEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root := tx.Bucket([]byte(catalogObjects))
	if root == nil {
		return nil, ErrCatalogIndex
	}
	objects := root.Bucket([]byte(catalogScope(nameHash(b.Name), b.Generation)))
	if objects == nil {
		return nil, ErrCatalogIndex
	}
	cursor := objects.Cursor()
	key, id := cursor.Seek([]byte(max(r.Prefix, after)))
	if string(key) == after {
		key, id = cursor.Next()
	}
	entries := make([]ListEntry, 0, int(r.Limit)+1)
	for key != nil && strings.HasPrefix(string(key), r.Prefix) && len(entries) <= int(r.Limit) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s.catalogListExamined.Add(1)
		o, err := readObject(s.objectPath(b.Name, string(key)))
		if errors.Is(err, ErrNoObject) {
			return nil, ErrCatalogIndex
		}
		if err != nil {
			return nil, err
		}
		if o.ID != string(id) || o.Key != string(key) {
			return nil, ErrCatalogIndex
		}
		candidate, _ := listCandidate(o, r)
		if candidate.Key > after {
			entries = append(entries, candidate)
		}
		if candidate.CommonPrefix {
			next := prefixSuccessor(candidate.Key)
			if next == nil {
				break
			}
			key, id = cursor.Seek(next)
		} else {
			key, id = cursor.Next()
		}
	}
	return entries, nil
}

// Byte ordering matches S3 key ordering. A seek past a common prefix skips all
// of its descendants without reading or decoding each child.
func prefixSuccessor(prefix string) []byte {
	key := []byte(prefix)
	for i := len(key) - 1; i >= 0; i-- {
		if key[i] < 255 {
			key[i]++
			return key[:i+1]
		}
	}
	return nil
}

func listCandidate(o Object, r ListRequest) (ListEntry, bool) {
	if !strings.HasPrefix(o.Key, r.Prefix) {
		return ListEntry{}, false
	}
	if r.Delimiter != "" {
		if i := strings.Index(strings.TrimPrefix(o.Key, r.Prefix), r.Delimiter); i >= 0 {
			return ListEntry{Object: Object{Key: o.Key[:len(r.Prefix)+i+len(r.Delimiter)]}, CommonPrefix: true}, true
		}
	}
	return ListEntry{Object: Object{Key: o.Key, ETag: o.ETag, Size: o.Size, Modified: o.Modified}}, true
}
