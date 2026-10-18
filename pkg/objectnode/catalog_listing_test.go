package objectnode

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"
)

func TestIndexedListingMatchesOrderedReference(t *testing.T) {
	s := testStore(t)
	keys := []string{"a", "a/", "a/0", "a/deep/1", "a/deep/2", "a/z", "a0", "b", "b/1", "b/2", "é", "é/a", "é/字", "字", "字/😀", "😀"}
	for _, key := range keys {
		putText(t, s, key, key)
	}
	for _, prefix := range []string{"", "a", "a/", "a/deep/", "é", "字/", "none"} {
		for _, delimiter := range []string{"", "/"} {
			for _, after := range []string{"", "a", "a/", "a/d", "b/1", "é/", "😀"} {
				for _, limit := range []int32{1, 3, 10} {
					r := ListRequest{Prefix: prefix, Delimiter: delimiter, StartAfter: after, Limit: limit}
					want := referenceListing(keys, r)
					got := []string{}
					for pages := 0; ; pages++ {
						if pages > len(keys) {
							t.Fatal("nonterminating pagination", r)
						}
						page, err := s.List(t.Context(), "test-bucket", r)
						if err != nil {
							t.Fatal(r, err)
						}
						for _, entry := range page.Entries {
							got = append(got, fmt.Sprintf("%t:%s", entry.CommonPrefix, entry.Key))
						}
						if page.NextCursor == "" {
							break
						}
						r.Cursor = page.NextCursor
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatal(r, got, want)
					}
				}
			}
		}
	}
}

func referenceListing(keys []string, r ListRequest) []string {
	unique := map[string]bool{}
	for _, key := range keys {
		if !strings.HasPrefix(key, r.Prefix) {
			continue
		}
		common := false
		if r.Delimiter == "/" {
			if n := strings.IndexByte(key[len(r.Prefix):], '/'); n >= 0 {
				key, common = key[:len(r.Prefix)+n+1], true
			}
		}
		if key > r.StartAfter {
			unique[key] = common
		}
	}
	ordered := make([]string, 0, len(unique))
	for key := range unique {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	result := make([]string, 0, len(ordered))
	for _, key := range ordered {
		result = append(result, fmt.Sprintf("%t:%s", unique[key], key))
	}
	return result
}

func TestIndexedListingLargePrefixSeeksAndConcurrentWrites(t *testing.T) {
	s := testStore(t)
	for i := range 4096 {
		putText(t, s, fmt.Sprintf("a/%05d", i), "content")
	}
	for _, key := range []string{"b/one", "c", "zz/unrelated"} {
		putText(t, s, key, key)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(s.root, catalogIndexName)); err != nil {
		t.Fatal(err)
	}
	var err error
	s, err = Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	before, _ := s.CatalogIndexStatus()
	page, err := s.List(t.Context(), "test-bucket", ListRequest{Delimiter: "/", Limit: 2})
	after, _ := s.CatalogIndexStatus()
	if err != nil || len(page.Entries) != 2 || page.Entries[0].Key != "a/" || page.Entries[1].Key != "b/" || after.ListExamined-before.ListExamined != 3 {
		t.Fatal("delimiter walked descendants", page, before, after, err)
	}
	// An unrelated source error exposes accidental catalog scans in narrow pages.
	if err = os.WriteFile(filepath.Join(s.objectPath("test-bucket", "zz/unrelated"), "current.json"), []byte("damaged"), 0o600); err != nil {
		t.Fatal(err)
	}
	page, err = s.List(t.Context(), "test-bucket", ListRequest{Prefix: "a/", StartAfter: "a/04000", Limit: 3})
	if err != nil || len(page.Entries) != 3 || page.Entries[0].Key != "a/04001" {
		t.Fatal("narrow seek scanned unrelated records", page, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		for i := range 128 {
			key := fmt.Sprintf("a/new-%05d", i)
			if _, err := s.Put(ctx, "test-bucket", key, strings.NewReader("x"), 1, PutOptions{}); err != nil {
				result <- err
				return
			}
			if err := s.Delete("test-bucket", key); err != nil {
				result <- err
				return
			}
		}
		result <- nil
	}()
	r := ListRequest{Prefix: "a/", Limit: 31}
	previous := ""
	for {
		page, err = s.List(ctx, "test-bucket", r)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range page.Entries {
			if entry.Key <= previous {
				t.Fatal("duplicate or unordered page", previous, entry.Key)
			}
			previous = entry.Key
		}
		if page.NextCursor == "" {
			break
		}
		r.Cursor = page.NextCursor
	}
	if err = <-result; err != nil {
		t.Fatal(err)
	}
	t.Logf("4099-key delimiter page read %d source records", after.ListExamined-before.ListExamined)
}

func TestIndexedListingGenerationAndLegacyCursor(t *testing.T) {
	s := testStore(t)
	putText(t, s, "a", "first")
	putText(t, s, "b", "second")
	page, err := s.List(t.Context(), "test-bucket", ListRequest{Limit: 1})
	if err != nil || page.NextCursor == "" {
		t.Fatal(page, err)
	}
	for _, key := range []string{"a", "b"} {
		if err = s.Delete("test-bucket", key); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.DeleteBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	if err = s.CreateBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	putText(t, s, "b", "new generation")
	if _, err = s.List(t.Context(), "test-bucket", ListRequest{Cursor: page.NextCursor, Limit: 1}); !errors.Is(err, ErrInvalid) {
		t.Fatal("cross-generation cursor accepted", err)
	}
	body, _ := json.Marshal(listCursor{Bucket: "test-bucket", After: "a"})
	legacy := base64.RawURLEncoding.EncodeToString(body)
	if page, err = s.List(t.Context(), "test-bucket", ListRequest{Cursor: legacy, Limit: 1}); err != nil || len(page.Entries) != 1 || page.Entries[0].Key != "b" {
		t.Fatal("legacy cursor compatibility", page, err)
	}
}

func TestCatalogProjectionFailurePreservesDurableWritesAndRebuilds(t *testing.T) {
	for _, replicated := range []bool{false, true} {
		t.Run(fmt.Sprint(replicated), func(t *testing.T) {
			s := testStore(t)
			if replicated {
				s, _ = replicationPair(t)
			}
			s.catalogIndexFault = func() error { return errors.New("index disk unavailable") }
			putText(t, s, "committed", "durable bytes")
			if readText(t, s, "committed") != "durable bytes" {
				t.Fatal("projection changed durable source result")
			}
			if _, err := s.List(t.Context(), "test-bucket", ListRequest{Limit: 10}); !errors.Is(err, ErrCatalogIndex) {
				t.Fatal("failed index served an incomplete page", err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(s.root, catalogIndexName), []byte("truncated index"), 0o600); err != nil {
				t.Fatal(err)
			}
			next, err := Open(s.root)
			if err != nil {
				t.Fatal(err)
			}
			defer next.Close()
			page, err := next.List(t.Context(), "test-bucket", ListRequest{Prefix: "committed", Limit: 10})
			if err != nil || len(page.Entries) != 1 || page.Entries[0].Key != "committed" {
				t.Fatal("rebuild lost committed write", page, err)
			}
		})
	}
}

func TestCatalogIndexRefusesAliasesAndUntrustedHints(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "permissions"} {
		t.Run(kind, func(t *testing.T) {
			s := testStore(t)
			putText(t, s, "source", "safe")
			_ = s.Close()
			path, target := filepath.Join(s.root, catalogIndexName), filepath.Join(t.TempDir(), "untouched")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte("untouched"), 0o600); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "symlink":
				err = os.Symlink(target, path)
			case "hardlink":
				err = os.Link(target, path)
			case "permissions":
				err = os.WriteFile(path, nil, 0o644)
				if err == nil {
					err = os.Chmod(path, 0o644)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			next, err := Open(s.root)
			if err != nil {
				t.Fatal(err)
			}
			defer next.Close()
			status, _ := next.CatalogIndexStatus()
			if status.Ready || readText(t, next, "source") != "safe" {
				t.Fatal(status)
			}
			if body, err := os.ReadFile(target); err != nil || string(body) != "untouched" {
				t.Fatal("aliased file modified", err)
			}
		})
	}
	s := testStore(t)
	putText(t, s, "source", "safe")
	if err := s.catalogIndex.db.Update(func(tx *bolt.Tx) error {
		return catalogPutObject(tx, nameHash("test-bucket"), Object{Key: "ghost", ID: "foreign"})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(t.Context(), "test-bucket", ListRequest{Prefix: "ghost", Limit: 1}); !errors.Is(err, ErrCatalogIndex) {
		t.Fatal("hint invented source object", err)
	}
	if status, _ := s.CatalogIndexStatus(); status.Ready || status.Failures != 1 {
		t.Fatal("stale projection remained healthy", status)
	}
}
