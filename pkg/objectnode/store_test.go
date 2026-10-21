package objectnode

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err = s.CreateBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	return s
}

func putText(t *testing.T, s *Store, key, body string) Object {
	t.Helper()
	o, err := s.Put(t.Context(), "test-bucket", key, strings.NewReader(body), int64(len(body)), PutOptions{Headers: http.Header{"Content-Type": {"text/plain"}}})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func readText(t *testing.T, s *Store, key string) string {
	t.Helper()
	_, f, err := s.Get("test-bucket", key)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestStorePublicationAndRestart(t *testing.T) {
	s := testStore(t)
	key := "../../literal key/+ç.txt"
	first := putText(t, s, key, "original")
	_, openReader, err := s.Get("test-bucket", key)
	if err != nil {
		t.Fatal(err)
	}
	defer openReader.Close()
	second := putText(t, s, key, "replacement")
	if first.ID == second.ID || first.ETag == second.ETag {
		t.Fatal("overwrite did not publish a new immutable blob")
	}
	if err = s.Delete("test-bucket", key); err != nil {
		t.Fatal(err)
	}
	oldBytes, err := io.ReadAll(openReader)
	if err != nil || string(oldBytes) != "original" {
		t.Fatal("overwrite/deletion broke an already-open reader", err)
	}
	putText(t, s, "durable", "acknowledged")
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := readText(t, reopened, "durable"); got != "acknowledged" {
		t.Fatal(got)
	}
	if _, _, err = reopened.Get("test-bucket", key); !errors.Is(err, ErrNoObject) {
		t.Fatal("deleted object returned after restart", err)
	}
}

func TestStoreRejectedWritesPreserveCurrentObject(t *testing.T) {
	s := testStore(t)
	putText(t, s, "key", "original")
	cases := []struct {
		name    string
		body    io.Reader
		size    int64
		options PutOptions
		want    error
	}{
		{"short", strings.NewReader("x"), 2, PutOptions{}, ErrInvalid},
		{"long", strings.NewReader("xx"), 1, PutOptions{}, ErrInvalid},
		{"interrupted", io.MultiReader(strings.NewReader("x"), brokenReader{}), 2, PutOptions{}, io.ErrUnexpectedEOF},
		{"md5", strings.NewReader("x"), 1, PutOptions{ContentMD5: "AAAAAAAAAAAAAAAAAAAAAA=="}, ErrDigest},
		{"sha256", strings.NewReader("x"), 1, PutOptions{PayloadSHA256: strings.Repeat("0", 64)}, ErrDigest},
		{"checksum", strings.NewReader("x"), 1, PutOptions{ChecksumAlgorithm: "CRC32", Checksum: "AAAAAA=="}, ErrDigest},
		{"condition", strings.NewReader("x"), 1, PutOptions{IfMatch: `"old"`}, ErrPrecondition},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := s.Put(t.Context(), "test-bucket", "key", test.body, test.size, test.options)
			if !errors.Is(err, test.want) || readText(t, s, "key") != "original" {
				t.Fatalf("failed write replaced the current object: %v", err)
			}
		})
	}
	entries, err := os.ReadDir(filepath.Join(s.root, "staging"))
	if err != nil || len(entries) != 0 {
		t.Fatal("failed uploads leaked staged bytes", entries, err)
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestStoreModificationTimeFollowsUpload(t *testing.T) {
	s := testStore(t)
	var streamedAt time.Time
	body := &firstReadHook{reader: strings.NewReader("data"), hook: func() { streamedAt = time.Now().UTC() }}
	o, err := s.Put(t.Context(), "test-bucket", "key", body, 4, PutOptions{})
	if err != nil || o.Modified.Before(streamedAt) {
		t.Fatal("Last-Modified predates the upload's publication", o.Modified, streamedAt, err)
	}
}

func TestStoreConcurrentConditionalCreation(t *testing.T) {
	s := testStore(t)
	var successful atomic.Int32
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			_, err := s.Put(t.Context(), "test-bucket", "once", strings.NewReader("data"), 4, PutOptions{IfNoneMatch: "*"})
			if err == nil {
				successful.Add(1)
			} else if !errors.Is(err, ErrPrecondition) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if successful.Load() != 1 {
		t.Fatalf("conditional create committed %d writers", successful.Load())
	}
}

func TestStoreBucketGenerationFencesStagedUpload(t *testing.T) {
	s := testStore(t)
	body := &firstReadHook{reader: strings.NewReader("data"), hook: func() {
		if err := s.DeleteBucket("test-bucket"); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateBucket("test-bucket"); err != nil {
			t.Fatal(err)
		}
	}}
	if _, err := s.Put(t.Context(), "test-bucket", "key", body, 4, PutOptions{}); !errors.Is(err, ErrNoBucket) {
		t.Fatal("staged upload crossed a bucket incarnation", err)
	}
}

type firstReadHook struct {
	reader io.Reader
	hook   func()
}

func (r *firstReadHook) Read(p []byte) (int, error) {
	if r.hook != nil {
		hook := r.hook
		r.hook = nil
		hook()
	}
	return r.reader.Read(p)
}

func TestStoreListingAndDiskLock(t *testing.T) {
	s := testStore(t)
	if other, err := Open(s.root); err == nil {
		other.Close()
		t.Fatal("two processes could own the same data directory")
	}
	for _, key := range []string{"a/one", "a/two", "b", "c/one", "ç"} {
		putText(t, s, key, key)
	}
	var keys []string
	r := ListRequest{Limit: 2}
	for {
		page, err := s.List(t.Context(), "test-bucket", r)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range page.Entries {
			keys = append(keys, entry.Key)
		}
		if page.NextCursor == "" {
			break
		}
		r.Cursor = page.NextCursor
	}
	if strings.Join(keys, ",") != "a/one,a/two,b,c/one,ç" {
		t.Fatal(keys)
	}
	page, err := s.List(t.Context(), "test-bucket", ListRequest{Delimiter: "/", Limit: 2})
	if err != nil || len(page.Entries) != 2 || !page.Entries[0].CommonPrefix || page.Entries[0].Key != "a/" || page.Entries[1].Key != "b" || page.NextCursor == "" {
		t.Fatal("delimiter listing lost its ordering or boundary", page, err)
	}
	if err = s.DeleteBucket("test-bucket"); !errors.Is(err, ErrNotEmpty) {
		t.Fatal("nonempty bucket deletion succeeded", err)
	}
}

func TestStoreFailsClosedOnCorruptCatalog(t *testing.T) {
	s := testStore(t)
	o := putText(t, s, "key", "keep me")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	dir := s.objectPath("test-bucket", "key")
	if err := os.WriteFile(filepath.Join(dir, "current.json"), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if other, err := Open(s.root); err == nil {
		other.Close()
		t.Fatal("corrupt catalog was accepted")
	}
	if _, err := os.Stat(filepath.Join(dir, o.ID+".data")); err != nil {
		t.Fatal("recovery removed bytes behind corrupt metadata", err)
	}
}

func TestStoreRefusesUnrecognizedDirectory(t *testing.T) {
	directory := t.TempDir()
	file := filepath.Join(directory, "existing-file")
	if err := os.WriteFile(file, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(directory); err == nil {
		s.Close()
		t.Fatal("accepted an existing non-storage directory")
	}
	if body, err := os.ReadFile(file); err != nil || string(body) != "preserve" {
		t.Fatal("initialization changed existing data", err)
	}
}

func TestStoreKilledUploadRecovery(t *testing.T) {
	if directory := os.Getenv("GREGALE_OBJECT_NODE_CRASH_TEST"); directory != "" {
		s, err := Open(directory)
		if err != nil {
			t.Fatal(err)
		}
		body := io.MultiReader(strings.NewReader("partial"), crashWaitReader{})
		_, _ = s.Put(context.Background(), "test-bucket", "key", body, 100, PutOptions{})
		return
	}
	s := testStore(t)
	putText(t, s, "key", "acknowledged")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStoreKilledUploadRecovery$")
	command.Env = append(os.Environ(), "GREGALE_OBJECT_NODE_CRASH_TEST="+s.root)
	out, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill() }()
	scanner := bufio.NewScanner(out)
	if !scanner.Scan() || scanner.Text() != "staged" {
		t.Fatal("child did not reach interrupted upload")
	}
	if err = command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	reopened, err := Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if readText(t, reopened, "key") != "acknowledged" {
		t.Fatal("killed upload replaced acknowledged data")
	}
	entries, err := os.ReadDir(filepath.Join(s.root, "staging"))
	if err != nil || len(entries) != 0 {
		t.Fatal("restart did not reclaim the killed upload", entries, err)
	}
}

type crashWaitReader struct{}

func (crashWaitReader) Read([]byte) (int, error) {
	_, _ = os.Stdout.WriteString("staged\n")
	select {}
}
