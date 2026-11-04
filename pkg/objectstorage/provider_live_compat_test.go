package objectstorage

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectchecksum"
)

// The branded gateway relies on these provider behaviors beyond the basic
// Provider contract. S3-compatible services differ exactly here: a provider
// that silently ignores a write condition turns a customer's conditional write
// into an overwrite, and one that does not echo a multipart checksum fails the
// upload at completion. TestLiveProviderQualification runs each group as a
// subtest in its own bucket, so one unsupported behavior does not hide another.

// liveQualificationBucket creates a bucket that is emptied (every version and
// delete marker) and deleted when the test ends.
func liveQualificationBucket(t *testing.T, ctx context.Context, p Provider) string {
	t.Helper()
	bucket := "gregale-qual-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := p.CreateBucket(ctx, bucket); err != nil {
		t.Fatalf("create qualification bucket: %v", err)
	}
	t.Cleanup(func() { emptyLiveBucket(t, ctx, p, bucket) })
	return bucket
}

// Cleanup runs after the test's context may have ended, so it detaches from
// that cancellation and bounds itself.
func emptyLiveBucket(t *testing.T, parent context.Context, p Provider, bucket string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 2*time.Minute)
	defer cancel()
	inventory, versioned := p.(ObjectVersionInventoryProvider)
	deleter, byVersion := p.(ObjectVersionDeleter)
	mutable, byNull := p.(MutableObjectDeleter)
	for cursor := ""; versioned && byVersion && byNull; {
		page, err := inventory.ListObjectVersions(ctx, bucket, cursor, api.ObjectVersionInventoryPageSize)
		if err != nil {
			t.Errorf("cleanup: list versions of %s: %v", bucket, err)
			break
		}
		for _, v := range page.Items {
			if v.ProviderVersionID == "" || v.ProviderVersionID == "null" {
				_, err = mutable.DeleteMutableObject(ctx, bucket, v.Key, "null")
			} else {
				_, err = deleter.DeleteObjectVersion(ctx, bucket, v.Key, v.ProviderVersionID)
			}
			if err != nil && !errors.Is(err, ErrNotFound) {
				t.Errorf("cleanup: delete %q version %q: %v", v.Key, v.ProviderVersionID, err)
			}
		}
		if cursor = page.NextCursor; cursor == "" {
			break
		}
	}
	for cursor := ""; ; {
		page, err := p.ListObjects(ctx, bucket, "", cursor, 1000)
		if err != nil {
			t.Errorf("cleanup: list objects of %s: %v", bucket, err)
			break
		}
		for _, object := range page.Items {
			if err := p.DeleteObject(ctx, bucket, object.Key); err != nil && !errors.Is(err, ErrNotFound) {
				t.Errorf("cleanup: delete %q: %v", object.Key, err)
			}
		}
		if cursor = page.NextCursor; cursor == "" {
			break
		}
	}
	if err := p.DeleteBucket(ctx, bucket); err != nil {
		t.Errorf("cleanup: delete bucket %s: %v", bucket, err)
	}
}

// abortLiveUploadOnCleanup aborts an upload the test leaves open; aborting a
// completed upload is harmless.
func abortLiveUploadOnCleanup(t *testing.T, parent context.Context, p Provider, bucket, key, upload string) {
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), time.Minute)
		defer cancel()
		_ = p.AbortMultipartUpload(ctx, bucket, MultipartAbortRequest{Key: key, ProviderUploadID: upload})
	})
}

type liveResponse struct {
	status int
	header http.Header
	body   []byte
}

// sendSigned performs a presigned request and returns its status instead of
// failing on non-2xx, so precondition refusals can be asserted.
func sendSigned(ctx context.Context, signed SignedRequest, method string, body []byte, contentType string) (liveResponse, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, signed.URL, reader)
	if err != nil {
		return liveResponse{}, err
	}
	for name, value := range signed.Headers {
		if !strings.EqualFold(name, "Content-Length") {
			req.Header.Set(name, value)
		}
	}
	if body != nil {
		req.ContentLength = int64(len(body))
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := qualificationHTTPClient.Do(req)
	if err != nil {
		return liveResponse{}, err
	}
	defer resp.Body.Close()
	read, err := io.ReadAll(resp.Body)
	return liveResponse{status: resp.StatusCode, header: resp.Header, body: read}, err
}

func putLiveObject(t *testing.T, ctx context.Context, p Provider, bucket, key, body string) {
	t.Helper()
	size := int64(len(body))
	signed, err := p.Presign(ctx, bucket, SignRequest{Method: http.MethodPut, Key: key, SizeBytes: &size, ContentType: "text/plain", ExpiresIn: 300})
	if err != nil {
		t.Fatalf("sign PUT %q: %v", key, err)
	}
	if err := putSigned(ctx, signed, []byte(body), "text/plain"); err != nil {
		t.Fatalf("PUT %q: %v", key, err)
	}
}

func liveObjectETag(t *testing.T, ctx context.Context, p Provider, bucket, key string) string {
	t.Helper()
	signed, err := p.Presign(ctx, bucket, SignRequest{Method: http.MethodHead, Key: key, ExpiresIn: 300})
	if err != nil {
		t.Fatalf("sign HEAD %q: %v", key, err)
	}
	headers, err := headSigned(ctx, signed)
	if err != nil {
		t.Fatalf("HEAD %q: %v", key, err)
	}
	if headers.Get("ETag") == "" {
		t.Fatalf("HEAD %q returned no ETag", key)
	}
	return headers.Get("ETag")
}

func qualifyConditionalWrites(t *testing.T, ctx context.Context, b Backend) {
	bucket := liveQualificationBucket(t, ctx, b.Provider)
	t.Run("state objects", func(t *testing.T) { qualifyConditionalState(t, ctx, b.Provider, bucket) })
	t.Run("signed PUT", func(t *testing.T) { qualifyConditionalPut(t, ctx, b.Provider, bucket) })
	t.Run("multipart completion", func(t *testing.T) { qualifyConditionalCompletion(t, ctx, b.Provider, bucket) })
	t.Run("copy source fence", func(t *testing.T) { qualifyCopySourceFence(t, ctx, b.Provider, bucket) })
}

// pkg/durableentity keeps its state in these objects; it is correct only if
// the provider enforces If-None-Match: * and If-Match atomically.
func qualifyConditionalState(t *testing.T, ctx context.Context, p Provider, bucket string) {
	states, ok := p.(ConditionalStateProvider)
	if !ok {
		t.Skip("provider does not implement conditional state objects")
	}
	key := "qualification/conditional/state.json"
	first, err := states.WriteStateObject(ctx, bucket, key, []byte(`{"n":1}`), "")
	if err != nil {
		t.Fatalf("create state object: %v", err)
	}
	if _, err := states.WriteStateObject(ctx, bucket, key, []byte(`{"n":2}`), ""); !errors.Is(err, ErrPreconditionFailed) && !errors.Is(err, ErrConditionalConflict) {
		t.Fatalf("If-None-Match: * on an existing object returned %v; the provider must refuse it", err)
	}
	second, err := states.WriteStateObject(ctx, bucket, key, []byte(`{"n":2}`), first)
	if err != nil {
		t.Fatalf("If-Match with the current ETag: %v", err)
	}
	if _, err := states.WriteStateObject(ctx, bucket, key, []byte(`{"n":3}`), first); !errors.Is(err, ErrPreconditionFailed) && !errors.Is(err, ErrConditionalConflict) {
		t.Fatalf("If-Match with a stale ETag returned %v; the provider must refuse it", err)
	}
	body, etag, err := states.ReadStateObject(ctx, bucket, key, 1024)
	if err != nil || string(body) != `{"n":2}` || etag != second {
		t.Fatalf("state object after refused writes = %q (etag %q, err %v), want {\"n\":2} (etag %q)", body, etag, err, second)
	}
}

// Customer conditional PUTs are signed with the condition, so the provider
// alone decides whether the write lands.
func qualifyConditionalPut(t *testing.T, ctx context.Context, p Provider, bucket string) {
	presigner, ok := p.(ConditionalObjectPresigner)
	if !ok {
		t.Skip("provider does not implement conditional signed PUTs")
	}
	key := "qualification/conditional/put.txt"
	put := func(body string, c ObjectWriteConditions) int {
		t.Helper()
		size := int64(len(body))
		signed, err := presigner.PresignConditionalPut(ctx, bucket, SignRequest{Method: http.MethodPut, Key: key, SizeBytes: &size, ContentType: "text/plain", ExpiresIn: 300}, c)
		if err != nil {
			t.Fatalf("sign conditional PUT %+v: %v", c, err)
		}
		r, err := sendSigned(ctx, signed, http.MethodPut, []byte(body), "text/plain")
		if err != nil {
			t.Fatalf("conditional PUT %+v: %v", c, err)
		}
		return r.status
	}
	if status := put("first", ObjectWriteConditions{IfNoneMatch: "*"}); status != http.StatusOK {
		t.Fatalf("If-None-Match: * on a new key returned HTTP %d", status)
	}
	if status := put("second", ObjectWriteConditions{IfNoneMatch: "*"}); status != http.StatusPreconditionFailed {
		t.Fatalf("If-None-Match: * on an existing key returned HTTP %d, want 412: the provider ignores the condition", status)
	}
	if status := put("second", ObjectWriteConditions{IfMatch: `"00000000000000000000000000000000"`}); status != http.StatusPreconditionFailed {
		t.Fatalf("If-Match with a wrong ETag returned HTTP %d, want 412: the provider ignores the condition", status)
	}
	assertSignedObject(t, ctx, p, bucket, key, []byte("first"))
	if status := put("third", ObjectWriteConditions{IfMatch: liveObjectETag(t, ctx, p, bucket, key)}); status != http.StatusOK {
		t.Fatalf("If-Match with the current ETag returned HTTP %d", status)
	}
	assertSignedObject(t, ctx, p, bucket, key, []byte("third"))
}

func qualifyConditionalCompletion(t *testing.T, ctx context.Context, p Provider, bucket string) {
	if _, ok := p.(MultipartResultCompleter); !ok {
		t.Skip("provider does not implement conditional multipart completion")
	}
	key := "qualification/conditional/multipart.txt"
	putLiveObject(t, ctx, p, bucket, key, "existing")
	part := []byte("replacement")
	// A provider may discard an upload after a refused completion, so each
	// attempt uses its own upload.
	prepare := func() MultipartCompleteRequest {
		t.Helper()
		session := uuid.NewString()
		upload, err := p.EnsureMultipartUpload(ctx, bucket, MultipartCreateRequest{SessionID: session, Key: key, SizeBytes: int64(len(part)), Metadata: ObjectMetadata{ContentType: "text/plain"}})
		if err != nil {
			t.Fatalf("create multipart upload: %v", err)
		}
		abortLiveUploadOnCleanup(t, ctx, p, bucket, key, upload)
		return MultipartCompleteRequest{SessionID: session, Key: key, ProviderUploadID: upload, SizeBytes: int64(len(part)), Parts: []CompletedPart{{PartNumber: 1, ETag: uploadPart(t, ctx, p, bucket, key, upload, 1, part)}}}
	}
	_, err := CompleteMultipartWithResult(ctx, p, bucket, prepare(), ObjectWriteConditions{IfNoneMatch: "*"})
	if err == nil {
		t.Fatal("If-None-Match: * on CompleteMultipartUpload overwrote an existing object: the provider ignores the condition")
	}
	if !errors.Is(err, ErrPreconditionFailed) {
		t.Fatalf("If-None-Match: * on CompleteMultipartUpload returned %v, want a precondition failure", err)
	}
	assertSignedObject(t, ctx, p, bucket, key, []byte("existing"))
	if _, err := CompleteMultipartWithResult(ctx, p, bucket, prepare(), ObjectWriteConditions{IfMatch: liveObjectETag(t, ctx, p, bucket, key)}); err != nil {
		t.Fatalf("If-Match on CompleteMultipartUpload with the current ETag: %v", err)
	}
	assertSignedObject(t, ctx, p, bucket, key, part)
}

// A tracked copy binds the source it measured with x-amz-copy-source-if-match;
// a provider that ignores it copies bytes the copy was never admitted for.
func qualifyCopySourceFence(t *testing.T, ctx context.Context, p Provider, bucket string) {
	copier, ok := p.(TrackedObjectCopier)
	if !ok {
		t.Skip("provider does not implement tracked copies")
	}
	source, destination := "qualification/conditional/copy-source.txt", "qualification/conditional/copy-destination.txt"
	putLiveObject(t, ctx, p, bucket, source, "measured")
	stale, err := copier.SnapshotCopySource(ctx, bucket, source)
	if err != nil {
		t.Fatalf("snapshot copy source: %v", err)
	}
	putLiveObject(t, ctx, p, bucket, source, "changed after measurement")
	request := CopyObjectRequest{SourceKey: source, DestinationKey: destination, MetadataDirective: "COPY"}
	_, err = copier.CopyTrackedObject(ctx, bucket, uuid.NewString(), request, stale)
	if err == nil {
		t.Fatal("a copy from a changed source succeeded: the provider ignores x-amz-copy-source-if-match")
	}
	if !errors.Is(err, ErrPreconditionFailed) {
		t.Fatalf("a copy from a changed source returned %v, want a precondition failure", err)
	}
	fresh, err := copier.SnapshotCopySource(ctx, bucket, source)
	if err != nil {
		t.Fatalf("snapshot changed copy source: %v", err)
	}
	if _, err := copier.CopyTrackedObject(ctx, bucket, uuid.NewString(), request, fresh); err != nil {
		t.Fatalf("copy with the current source ETag: %v", err)
	}
	assertSignedObject(t, ctx, p, bucket, destination, []byte("changed after measurement"))
}

// Customers' SDKs choose the algorithm, and S3.SupportsMultipartChecksum
// accepts every valid policy, so the provider must store and echo each one.
func qualifyMultipartChecksums(t *testing.T, ctx context.Context, b Backend) {
	checksums, ok := b.Provider.(MultipartChecksumProvider)
	if _, completes := b.Provider.(MultipartResultCompleter); !ok || !completes {
		t.Skip("provider does not implement checksummed multipart uploads")
	}
	bucket := liveQualificationBucket(t, ctx, b.Provider)
	parts := [][]byte{bytes.Repeat([]byte("c"), 5<<20), []byte("checksum-tail")}
	size := int64(len(parts[0]) + len(parts[1]))
	for _, policy := range []objectchecksum.Policy{
		{Algorithm: "CRC32", Type: objectchecksum.Composite}, {Algorithm: "CRC32", Type: objectchecksum.FullObject},
		{Algorithm: "CRC32C", Type: objectchecksum.Composite}, {Algorithm: "CRC32C", Type: objectchecksum.FullObject},
		{Algorithm: "CRC64NVME", Type: objectchecksum.FullObject},
		{Algorithm: "SHA1", Type: objectchecksum.Composite}, {Algorithm: "SHA256", Type: objectchecksum.Composite},
	} {
		t.Run(policy.Algorithm+"/"+policy.Type, func(t *testing.T) {
			if !checksums.SupportsMultipartChecksum(policy) {
				t.Skip("driver declines this policy")
			}
			key := "qualification/checksums/" + strings.ToLower(policy.Algorithm+"-"+policy.Type) + ".bin"
			session := uuid.NewString()
			upload, err := b.Provider.EnsureMultipartUpload(ctx, bucket, MultipartCreateRequest{Checksum: policy, SessionID: session, Key: key, SizeBytes: size, Metadata: ObjectMetadata{ContentType: "application/octet-stream"}})
			if err != nil {
				t.Fatalf("create multipart upload: %v", err)
			}
			abortLiveUploadOnCleanup(t, ctx, b.Provider, bucket, key, upload)
			var digests []objectchecksum.Part
			var completed []CompletedPart
			for i, body := range parts {
				h := objectchecksum.NewHash(policy.Algorithm)
				_, _ = h.Write(body)
				value := base64.StdEncoding.EncodeToString(h.Sum(nil))
				number := int32(i + 1)
				etag := uploadChecksumPart(t, ctx, b.Provider, bucket, key, upload, number, body, objectchecksum.Digest{Policy: policy, Value: value})
				digests = append(digests, objectchecksum.Part{Number: number, Size: int64(len(body)), Value: value})
				completed = append(completed, CompletedPart{PartNumber: number, ETag: etag})
			}
			snapshot, err := objectchecksum.Build(policy, digests)
			if err != nil {
				t.Fatal(err)
			}
			result, err := CompleteMultipartWithResult(ctx, b.Provider, bucket, MultipartCompleteRequest{Checksum: snapshot, SessionID: session, Key: key, ProviderUploadID: upload, SizeBytes: size, Parts: completed}, ObjectWriteConditions{})
			if err != nil {
				t.Fatalf("complete: %v; the provider must store and echo this checksum, or the gateway must stop accepting it for this backend", err)
			}
			if result.Checksum != snapshot.Digest {
				t.Fatalf("completion checksum = %+v, want %+v", result.Checksum, snapshot.Digest)
			}
		})
	}
}

type liveVersioning struct {
	versioning BucketVersioningProvider
	writer     ObjectWriter
	reader     ObjectReader
	lister     ObjectVersionLister
	inventory  ObjectVersionInventoryProvider
	deleter    ObjectVersionDeleter
	mutable    MutableObjectDeleter
	reads      VersionReadPresigner
}

func liveVersioningContract(p Provider) (liveVersioning, bool) {
	var v liveVersioning
	var ok [8]bool
	v.versioning, ok[0] = p.(BucketVersioningProvider)
	v.writer, ok[1] = p.(ObjectWriter)
	v.reader, ok[2] = p.(ObjectReader)
	v.lister, ok[3] = p.(ObjectVersionLister)
	v.inventory, ok[4] = p.(ObjectVersionInventoryProvider)
	v.deleter, ok[5] = p.(ObjectVersionDeleter)
	v.mutable, ok[6] = p.(MutableObjectDeleter)
	v.reads, ok[7] = p.(VersionReadPresigner)
	return v, !slices.Contains(ok[:], false)
}

func (v liveVersioning) write(t *testing.T, ctx context.Context, bucket, key, body string) UploadResult {
	t.Helper()
	result, err := v.writer.WriteObject(ctx, bucket, key, strings.NewReader(body), int64(len(body)), ObjectMetadata{ContentType: "text/plain"})
	if err != nil {
		t.Fatalf("write %q: %v", key, err)
	}
	return result
}

// setStatus waits for the status to read back, then (for Enabled) for a probe
// write to receive a real version, since providers may propagate it lazily.
func (v liveVersioning) setStatus(t *testing.T, ctx context.Context, bucket, status string) {
	t.Helper()
	if err := v.versioning.PutBucketVersioning(ctx, bucket, status); err != nil {
		t.Fatalf("set versioning %s: %v", status, err)
	}
	deadline := time.Now().Add(2 * time.Minute)
	for {
		got, err := v.versioning.GetBucketVersioning(ctx, bucket)
		if err == nil && got.Status == status {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("versioning status = %+v (err %v), want %s", got, err, status)
		}
		time.Sleep(5 * time.Second)
	}
	for status == "Enabled" {
		if id := v.write(t, ctx, bucket, "qualification/versions/probe", "probe").ProviderVersionID; id != "" && id != "null" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("writes in an Enabled bucket still create null versions")
		}
		time.Sleep(5 * time.Second)
	}
}

func (v liveVersioning) versions(t *testing.T, ctx context.Context, bucket, key string) []ListedObjectVersion {
	t.Helper()
	page, err := v.lister.ListObjectVersionPage(ctx, bucket, ObjectVersionListRequest{Prefix: key, Limit: 100})
	if err != nil {
		t.Fatalf("list versions of %q: %v", key, err)
	}
	var out []ListedObjectVersion
	for _, item := range page.Items {
		if item.Key == key {
			out = append(out, item)
		}
	}
	return out
}

func (v liveVersioning) readVersion(t *testing.T, ctx context.Context, bucket, key, version, want string) {
	t.Helper()
	signed, err := v.reads.PresignVersionRead(ctx, bucket, http.MethodGet, key, version, false, 300)
	if err != nil {
		t.Fatalf("sign version read %q: %v", version, err)
	}
	r, err := sendSigned(ctx, signed, http.MethodGet, nil, "")
	if err != nil || r.status != http.StatusOK || string(r.body) != want || r.header.Get("X-Amz-Version-Id") != version {
		t.Fatalf("GET version %q = HTTP %d %q (version header %q, err %v), want %q", version, r.status, r.body, r.header.Get("X-Amz-Version-Id"), err, want)
	}
}

func (v liveVersioning) readCurrent(t *testing.T, ctx context.Context, bucket, key string) (string, error) {
	t.Helper()
	body, err := v.reader.ReadObject(ctx, bucket, key)
	if err != nil {
		return "", err
	}
	defer body.Close()
	read, err := io.ReadAll(body)
	return string(read), err
}

func qualifyVersioning(t *testing.T, ctx context.Context, b Backend) {
	v, ok := liveVersioningContract(b.Provider)
	if !ok {
		t.Skip("provider does not implement the versioned-object contract")
	}
	bucket := liveQualificationBucket(t, ctx, b.Provider)
	key := "qualification/versions/ünïcode key +.txt"
	v.write(t, ctx, bucket, key, "null version")
	v.setStatus(t, ctx, bucket, "Enabled")
	v1 := v.write(t, ctx, bucket, key, "first").ProviderVersionID
	v2 := v.write(t, ctx, bucket, key, "second").ProviderVersionID
	if v1 == "" || v1 == "null" || v2 == "" || v2 == v1 {
		t.Fatalf("versioned writes returned versions %q and %q", v1, v2)
	}
	listed := v.versions(t, ctx, bucket, key)
	if len(listed) != 3 || listed[0].ProviderVersionID != v2 || !listed[0].IsLatest || listed[1].ProviderVersionID != v1 || listed[2].ProviderVersionID != "null" || slices.ContainsFunc(listed, func(x ListedObjectVersion) bool { return x.DeleteMarker }) {
		t.Fatalf("versions = %+v, want %s (latest), %s, null", listed, v2, v1)
	}
	v.readVersion(t, ctx, bucket, key, v1, "first")
	v.readVersion(t, ctx, bucket, key, "null", "null version")

	marker, err := v.mutable.DeleteMutableObject(ctx, bucket, key, "")
	if err != nil || !marker.DeleteMarker || marker.ProviderVersionID == "" || marker.ProviderVersionID == "null" {
		t.Fatalf("delete in an Enabled bucket = %+v (err %v), want a new delete marker", marker, err)
	}
	if _, err := v.readCurrent(t, ctx, bucket, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GET behind a delete marker returned %v, want not found", err)
	}
	// The S3 driver lists data versions before delete markers.
	if listed = v.versions(t, ctx, bucket, key); len(listed) != 4 || !slices.ContainsFunc(listed, func(x ListedObjectVersion) bool {
		return x.DeleteMarker && x.IsLatest && x.ProviderVersionID == marker.ProviderVersionID
	}) || slices.ContainsFunc(listed, func(x ListedObjectVersion) bool { return x.IsLatest && !x.DeleteMarker }) {
		t.Fatalf("versions after delete = %+v, want the marker %s as the only latest entry", listed, marker.ProviderVersionID)
	}
	if removed, err := v.deleter.DeleteObjectVersion(ctx, bucket, key, marker.ProviderVersionID); err != nil || !removed.DeleteMarker {
		t.Fatalf("remove delete marker = %+v (err %v)", removed, err)
	}
	if got, err := v.readCurrent(t, ctx, bucket, key); err != nil || got != "second" {
		t.Fatalf("GET after removing the marker = %q (err %v), want second", got, err)
	}
	for range 2 { // A repeated version delete is a no-op, not an error.
		if _, err := v.deleter.DeleteObjectVersion(ctx, bucket, key, v1); err != nil {
			t.Fatalf("delete version %s: %v", v1, err)
		}
	}
	if _, err := v.mutable.DeleteMutableObject(ctx, bucket, key, "null"); err != nil {
		t.Fatalf("delete the null version: %v", err)
	}
	if listed = v.versions(t, ctx, bucket, key); len(listed) != 1 || listed[0].ProviderVersionID != v2 {
		t.Fatalf("versions after version deletes = %+v, want only %s", listed, v2)
	}
	page, err := v.inventory.ListObjectVersions(ctx, bucket, "", api.ObjectVersionInventoryPageSize)
	if err != nil || !slices.ContainsFunc(page.Items, func(x ObjectVersionInventoryEntry) bool {
		return x.Key == key && x.ProviderVersionID == v2 && x.SizeBytes == int64(len("second")) && !x.DeleteMarker
	}) {
		t.Fatalf("version inventory = %+v (err %v), want %q version %s", page.Items, err, key, v2)
	}

	v.setStatus(t, ctx, bucket, "Suspended")
	if id := v.write(t, ctx, bucket, key, "suspended").ProviderVersionID; id != "" && id != "null" {
		t.Fatalf("a write in a Suspended bucket returned version %q, want the null version", id)
	}
	if listed = v.versions(t, ctx, bucket, key); len(listed) != 2 || listed[0].ProviderVersionID != "null" || !listed[0].IsLatest || listed[1].ProviderVersionID != v2 {
		t.Fatalf("versions after a Suspended write = %+v, want null (latest), %s", listed, v2)
	}
}

// Default SSE-S3 needs no customer keys, so it is the encryption a new backend
// can offer first. Declare "AES256" in the backend's encryption.algorithms.
func qualifyBucketEncryption(t *testing.T, ctx context.Context, b Backend) {
	encryption, ok := b.Provider.(BucketEncryptionProvider)
	if !ok {
		t.Skip("provider does not implement bucket default encryption")
	}
	if !slices.Contains(b.Encryption.Algorithms, "AES256") {
		t.Skip(`declare "AES256" in the backend's encryption.algorithms to qualify default bucket encryption`)
	}
	resolved, err := b.Encryption.Resolve(uuid.NewString(), api.ObjectEncryption{Algorithm: "AES256"})
	if err != nil {
		t.Fatal(err)
	}
	bucket := liveQualificationBucket(t, ctx, b.Provider)
	current, err := encryption.GetBucketEncryption(ctx, bucket)
	if err != nil {
		t.Fatalf("read default encryption: %v", err)
	}
	if err := encryption.PutBucketEncryption(ctx, bucket, resolved, current); err != nil {
		t.Fatalf("set AES256 default encryption: %v", err)
	}
	applied, err := encryption.GetBucketEncryption(ctx, bucket)
	if err != nil || applied.Algorithm != "AES256" {
		t.Fatalf("default encryption after set = %+v (err %v), want AES256", applied, err)
	}
	key := "qualification/encryption/default.txt"
	putLiveObject(t, ctx, b.Provider, bucket, key, "encrypted by default")
	signed, err := b.Provider.Presign(ctx, bucket, SignRequest{Method: http.MethodHead, Key: key, ExpiresIn: 300})
	if err != nil {
		t.Fatalf("sign HEAD: %v", err)
	}
	headers, err := headSigned(ctx, signed)
	if err != nil || headers.Get("X-Amz-Server-Side-Encryption") != "AES256" {
		t.Fatalf("object written under AES256 default reports x-amz-server-side-encryption %q (err %v)", headers.Get("X-Amz-Server-Side-Encryption"), err)
	}
	if err := encryption.ClearBucketEncryption(ctx, bucket, applied); err != nil {
		t.Fatalf("clear default encryption: %v", err)
	}
}

// Object Lock is an operator declaration per backend. Retention here is
// GOVERNANCE for 30 s, so the bucket can always be cleaned up.
func qualifyObjectLock(t *testing.T, ctx context.Context, b Backend) {
	if !b.ObjectLock.Enabled {
		t.Skip("set object_lock.enabled in the qualification config to qualify Object Lock")
	}
	if !SupportsNativeObjectLock(b.Provider) {
		t.Fatal("object_lock is enabled but the driver lacks the native Object Lock contract")
	}
	v, ok := liveVersioningContract(b.Provider)
	if !ok {
		t.Fatal("object_lock is enabled but the driver lacks the versioned-object contract")
	}
	locks, versions := b.Provider.(BucketObjectLockProvider), b.Provider.(ObjectVersionLockProvider)
	bucket := liveQualificationBucket(t, ctx, b.Provider)
	v.setStatus(t, ctx, bucket, "Enabled")
	if err := locks.PutBucketObjectLock(ctx, bucket, api.ObjectBucketObjectLockConfiguration{Enabled: true}); err != nil {
		t.Fatalf("enable Object Lock on an existing versioned bucket: %v", err)
	}
	if got, err := locks.GetBucketObjectLock(ctx, bucket); err != nil || !got.Enabled {
		t.Fatalf("Object Lock configuration = %+v (err %v), want enabled", got, err)
	}
	key := "qualification/lock/held.txt"
	version := v.write(t, ctx, bucket, key, "protected").ProviderVersionID

	if err := versions.PutObjectVersionLegalHold(ctx, bucket, key, version, api.ObjectVersionLegalHold{Status: "ON"}); err != nil {
		t.Fatalf("place legal hold: %v", err)
	}
	if got, err := versions.GetObjectVersionLegalHold(ctx, bucket, key, version); err != nil || got.Status != "ON" {
		t.Fatalf("legal hold = %+v (err %v), want ON", got, err)
	}
	if _, err := v.deleter.DeleteObjectVersion(ctx, bucket, key, version); err == nil {
		t.Fatal("a version under legal hold was deleted: the provider ignores the hold")
	}
	if err := versions.PutObjectVersionLegalHold(ctx, bucket, key, version, api.ObjectVersionLegalHold{Status: "OFF"}); err != nil {
		t.Fatalf("release legal hold: %v", err)
	}

	until := time.Now().UTC().Add(30 * time.Second).Truncate(time.Second)
	t.Cleanup(func() { time.Sleep(time.Until(until.Add(2 * time.Second))) }) // Runs before the bucket is emptied.
	if err := versions.PutObjectVersionRetention(ctx, bucket, key, version, api.ObjectVersionRetention{Mode: "GOVERNANCE", RetainUntilDate: &until}, false); err != nil {
		t.Fatalf("set GOVERNANCE retention: %v", err)
	}
	if got, err := versions.GetObjectVersionRetention(ctx, bucket, key, version); err != nil || got.Mode != "GOVERNANCE" || got.RetainUntilDate == nil || !got.RetainUntilDate.Equal(until) {
		t.Fatalf("retention = %+v (err %v), want GOVERNANCE until %s", got, err, until)
	}
	if _, err := v.deleter.DeleteObjectVersion(ctx, bucket, key, version); err == nil {
		t.Fatal("a version under retention was deleted: the provider ignores retention")
	}
}
