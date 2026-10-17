package objectnode

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectchecksum"
	"github.com/onebox-faas/faas/pkg/objectmultipart"
)

func newMultipart(t *testing.T, s *Store, policy objectchecksum.Policy) MultipartUpload {
	t.Helper()
	u, err := s.CreateMultipart("test-bucket", "key", MultipartOptions{Headers: http.Header{"Content-Type": {"text/plain"}}, Checksum: policy})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func multipartPart(t *testing.T, s *Store, u MultipartUpload, number int32, body []byte) MultipartPart {
	t.Helper()
	options := PutOptions{}
	if !u.Checksum.Empty() {
		h := objectchecksum.NewHash(u.Checksum.Algorithm)
		h.Write(body)
		options.ChecksumAlgorithm, options.Checksum = u.Checksum.Algorithm, base64.StdEncoding.EncodeToString(h.Sum(nil))
	}
	p, err := s.UploadPart(t.Context(), u.Bucket, u.Key, u.ID, number, bytes.NewReader(body), int64(len(body)), options)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func completion(parts ...MultipartPart) MultipartCompletion {
	r := MultipartCompletion{}
	for _, p := range parts {
		r.Parts = append(r.Parts, objectmultipart.Part{PartNumber: p.Number, ETag: p.Object.ETag})
	}
	return r
}

func TestMultipartResumeAndCompleteAcrossRestart(t *testing.T) {
	s := testStore(t)
	u := newMultipart(t, s, objectchecksum.Policy{})
	firstBody := bytes.Repeat([]byte("a"), int(api.MinMultipartPartBytes))
	first := multipartPart(t, s, u, 1, firstBody)
	if _, _, err := s.Get(u.Bucket, u.Key); !errors.Is(err, ErrNoObject) {
		t.Fatal("part became visible as an object", err)
	}
	if err := s.DeleteBucket(u.Bucket); !errors.Is(err, ErrNotEmpty) {
		t.Fatal("bucket deletion orphaned active upload", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	parts, truncated, err := reopened.ListParts(u.Bucket, u.Key, u.ID, 0, 1)
	if err != nil || truncated || len(parts) != 1 || parts[0].Object.ETag != first.Object.ETag {
		t.Fatal(parts, truncated, err)
	}
	last := multipartPart(t, reopened, u, 2, []byte("tail"))
	o, err := reopened.CompleteMultipart(t.Context(), u.Bucket, u.Key, u.ID, completion(first, last), nil)
	if err != nil || o.Size != int64(len(firstBody))+4 || !strings.HasSuffix(o.ETag, `-2"`) {
		t.Fatal(o, err)
	}
	_, file, err := reopened.Get(u.Bucket, u.Key)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(file)
	file.Close()
	if err != nil || !bytes.Equal(data, append(firstBody, []byte("tail")...)) {
		t.Fatal("wrong assembled bytes", err)
	}
	if _, _, err = reopened.ListParts(u.Bucket, u.Key, u.ID, 0, 100); !errors.Is(err, ErrNoUpload) {
		t.Fatal("completed parts still active", err)
	}
	if entries, err := os.ReadDir(filepath.Join(reopened.multipartPath(u.ID), "parts")); !errors.Is(err, os.ErrNotExist) || len(entries) != 0 {
		t.Fatal("completed part files retained", err)
	}
	putText(t, reopened, u.Key, "replacement")
	replay, err := reopened.CompleteMultipart(t.Context(), u.Bucket, u.Key, u.ID, completion(first, last), nil)
	if err != nil || replay.ID != o.ID || readText(t, reopened, u.Key) != "replacement" {
		t.Fatal("completion replay changed current object", replay, err)
	}
	if err = reopened.Delete(u.Bucket, u.Key); err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.CompleteMultipart(t.Context(), u.Bucket, u.Key, u.ID, completion(first, last), nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err = reopened.Get(u.Bucket, u.Key); !errors.Is(err, ErrNoObject) {
		t.Fatal("replay resurrected bytes", err)
	}
	proof, err := reopened.MultipartReceipt(u.Bucket, u.Key, u.ReceiptID)
	if err != nil || proof.Status != ReceiptCommitted || proof.Object.ETag != o.ETag {
		t.Fatal(proof, err)
	}
	if _, err = reopened.Receipt(u.Bucket, u.Key, u.ID); !errors.Is(err, ErrNoReceipt) {
		t.Fatal("multipart outcome became a PUT proof", err)
	}
}

func TestMultipartChecksumPolicies(t *testing.T) {
	for _, policy := range []objectchecksum.Policy{{Algorithm: "CRC32", Type: objectchecksum.Composite}, {Algorithm: "CRC32C", Type: objectchecksum.FullObject}, {Algorithm: "CRC64NVME", Type: objectchecksum.FullObject}, {Algorithm: "SHA1", Type: objectchecksum.Composite}, {Algorithm: "SHA256", Type: objectchecksum.Composite}} {
		t.Run(policy.Algorithm+policy.Type, func(t *testing.T) {
			s := testStore(t)
			u := newMultipart(t, s, policy)
			first := multipartPart(t, s, u, 1, bytes.Repeat([]byte("x"), int(api.MinMultipartPartBytes)))
			last := multipartPart(t, s, u, 2, []byte("last"))
			snapshot, err := objectchecksum.Build(policy, []objectchecksum.Part{{Number: 1, Size: first.Object.Size, Value: first.Object.Checksum}, {Number: 2, Size: last.Object.Size, Value: last.Object.Checksum}})
			if err != nil {
				t.Fatal(err)
			}
			r := completion(first, last)
			r.Checksum = snapshot.Digest
			o, err := s.CompleteMultipart(t.Context(), u.Bucket, u.Key, u.ID, r, nil)
			if err != nil || o.Checksum != snapshot.Value || o.ChecksumType != policy.Type {
				t.Fatal(o, err)
			}
			if _, err = s.CompleteMultipart(t.Context(), u.Bucket, u.Key, u.ID, r, nil); err != nil {
				t.Fatal("checksum completion replay", err)
			}
		})
	}
}

func TestMultipartSelectionFailuresPreserveParts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*MultipartCompletion)
		want   error
	}{
		{"order", func(r *MultipartCompletion) { r.Parts[0], r.Parts[1] = r.Parts[1], r.Parts[0] }, ErrPartOrder},
		{"missing", func(r *MultipartCompletion) { r.Parts[1].PartNumber = 3 }, ErrInvalidPart},
		{"etag", func(r *MultipartCompletion) { r.Parts[1].ETag = `"incorrect"` }, ErrInvalidPart},
		{"size", func(r *MultipartCompletion) { n := int64(1); r.ExpectedSize = &n }, ErrDigest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t)
			u := newMultipart(t, s, objectchecksum.Policy{})
			first := multipartPart(t, s, u, 1, bytes.Repeat([]byte("a"), int(api.MinMultipartPartBytes)))
			last := multipartPart(t, s, u, 2, []byte("tail"))
			r := completion(first, last)
			tc.change(&r)
			if _, err := s.CompleteMultipart(t.Context(), u.Bucket, u.Key, u.ID, r, nil); !errors.Is(err, tc.want) {
				t.Fatal(err)
			}
			parts, _, err := s.ListParts(u.Bucket, u.Key, u.ID, 0, 100)
			if err != nil || len(parts) != 2 {
				t.Fatal("invalid selection lost parts", parts, err)
			}
		})
	}
	s := testStore(t)
	u := newMultipart(t, s, objectchecksum.Policy{})
	first, last := multipartPart(t, s, u, 1, []byte("small")), multipartPart(t, s, u, 2, []byte("last"))
	if _, err := s.CompleteMultipart(t.Context(), u.Bucket, u.Key, u.ID, completion(first, last), nil); !errors.Is(err, ErrPartSmall) {
		t.Fatal(err)
	}
}

func TestMultipartCompletionPinsBeforeAssemblyAndAbort(t *testing.T) {
	s := testStore(t)
	u := newMultipart(t, s, objectchecksum.Policy{})
	p := multipartPart(t, s, u, 1, []byte("original"))
	entered, resume := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := s.CompleteMultipart(t.Context(), u.Bucket, u.Key, u.ID, completion(p), func(int64) (func(), error) { close(entered); <-resume; return func() {}, nil })
		result <- err
	}()
	<-entered
	if err := s.AbortMultipart(u.Bucket, u.Key, u.ID); !errors.Is(err, ErrUploadBusy) {
		t.Error("abort crossed assembly", err)
	}
	if _, err := s.UploadPart(t.Context(), u.Bucket, u.Key, u.ID, 1, strings.NewReader("changed"), 7, PutOptions{}); !errors.Is(err, ErrNoUpload) {
		t.Error("part crossed frozen manifest", err)
	}
	if n, err := s.ExpireMultipart(t.Context(), time.Now().Add(48*time.Hour)); err != nil || n != 0 {
		t.Error("expiry crossed assembly", n, err)
	}
	close(resume)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if err := s.AbortMultipart(u.Bucket, u.Key, u.ID); !errors.Is(err, ErrNoUpload) {
		t.Fatal(err)
	}
	if readText(t, s, u.Key) != "original" {
		t.Fatal("abort deleted committed bytes")
	}
}

func TestMultipartAbortExpiryAndCapacity(t *testing.T) {
	s := testStore(t)
	u, err := s.CreateMultipart("test-bucket", "key", MultipartOptions{MaxBytes: 8})
	if err != nil {
		t.Fatal(err)
	}
	multipartPart(t, s, u, 1, []byte("original"))
	if _, err = s.UploadPart(t.Context(), u.Bucket, u.Key, u.ID, 2, strings.NewReader("x"), 1, PutOptions{}); !errors.Is(err, ErrCapacity) {
		t.Fatal("multipart bytes exceeded cap", err)
	}
	// Replacing a part accounts for the selected version, rather than charging twice.
	multipartPart(t, s, u, 1, []byte("new"))
	if count, err := s.ExpireMultipart(t.Context(), time.Now().Add(48*time.Hour)); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if _, _, err = s.ListParts(u.Bucket, u.Key, u.ID, 0, 10); !errors.Is(err, ErrNoUpload) {
		t.Fatal(err)
	}
	u = newMultipart(t, s, objectchecksum.Policy{})
	multipartPart(t, s, u, 1, []byte("abort"))
	if err = s.AbortMultipart(u.Bucket, u.Key, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CompleteMultipart(t.Context(), u.Bucket, u.Key, u.ID, MultipartCompletion{}, nil); !errors.Is(err, ErrNoUpload) {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if items, _, err := reopened.ListMultipart(t.Context(), "test-bucket", MultipartListRequest{Limit: 100}); err != nil || len(items) != 0 {
		t.Fatal(items, err)
	}
}

func TestMultipartPendingCompletionRecoversBeforeGC(t *testing.T) {
	for _, failure := range []string{"current.json", "terminal receipt", "terminal session"} {
		t.Run(failure, func(t *testing.T) {
			s := testStore(t)
			u := newMultipart(t, s, objectchecksum.Policy{})
			p := multipartPart(t, s, u, 1, []byte("committed"))
			s.persist = func(path string, value any) (bool, error) {
				fail := failure == "current.json" && path == filepath.Join(s.objectPath(u.Bucket, u.Key), "current.json")
				if r, ok := value.(writeReceipt); ok && failure == "terminal receipt" && r.Status == ReceiptCommitted {
					fail = true
				}
				if session, ok := value.(MultipartUpload); ok && failure == "terminal session" && session.Status == multipartCompleted {
					fail = true
				}
				if fail {
					return false, errors.New("injected commit interruption")
				}
				return writeJSON(path, value)
			}
			if _, err := s.CompleteMultipart(t.Context(), u.Bucket, u.Key, u.ID, completion(p), nil); err == nil {
				t.Fatal("fault not reached")
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(s.root)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			if readText(t, reopened, u.Key) != "committed" {
				t.Fatal("restart lost completed bytes")
			}
			proof, err := reopened.MultipartReceipt(u.Bucket, u.Key, u.ReceiptID)
			if err != nil || proof.Status != ReceiptCommitted {
				t.Fatal(proof, err)
			}
			if _, err = reopened.CompleteMultipart(t.Context(), u.Bucket, u.Key, u.ID, completion(p), nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMultipartInterruptedAssemblyResumesAfterRestart(t *testing.T) {
	s := testStore(t)
	u := newMultipart(t, s, objectchecksum.Policy{})
	p := multipartPart(t, s, u, 1, []byte("durable part"))
	ctx, cancel := context.WithCancel(t.Context())
	if _, err := s.CompleteMultipart(ctx, u.Bucket, u.Key, u.ID, completion(p), func(int64) (func(), error) { cancel(); return func() {}, nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	o, err := reopened.CompleteMultipart(t.Context(), u.Bucket, u.Key, u.ID, completion(p), nil)
	if err != nil || o.Size != p.Object.Size || readText(t, reopened, u.Key) != "durable part" {
		t.Fatal(o, err)
	}
}

func TestMultipartExpirySkipsUnpublishedInitiation(t *testing.T) {
	s := testStore(t)
	// A failed initial session publication leaves only an empty staging layout.
	if err := os.MkdirAll(filepath.Join(s.multipartPath(uuid.NewString()), "parts"), 0o700); err != nil {
		t.Fatal(err)
	}
	u := newMultipart(t, s, objectchecksum.Policy{})
	multipartPart(t, s, u, 1, []byte("expired"))
	if count, err := s.ExpireMultipart(t.Context(), time.Now().Add(48*time.Hour)); err != nil || count != 1 {
		t.Fatal("unpublished initiation blocked expiry", count, err)
	}
}

func TestMultipartAbortRetriesPhysicalCleanup(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires ordinary directory permissions")
	}
	for _, sweep := range []bool{false, true} {
		t.Run(map[bool]string{false: "abort retry", true: "expiry sweep"}[sweep], func(t *testing.T) {
			s := testStore(t)
			u := newMultipart(t, s, objectchecksum.Policy{})
			multipartPart(t, s, u, 1, []byte("cleanup"))
			dir := s.partPath(u.ID, 1)
			if err := os.Chmod(dir, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
			if err := s.AbortMultipart(u.Bucket, u.Key, u.ID); err == nil {
				t.Fatal("cleanup fault not reached")
			}
			if err := os.Chmod(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if sweep {
				if _, err := s.ExpireMultipart(t.Context(), time.Now()); err != nil {
					t.Fatal(err)
				}
			} else if err := s.AbortMultipart(u.Bucket, u.Key, u.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(s.multipartPath(u.ID), "parts")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("terminal cleanup not retried", err)
			}
		})
	}
}
