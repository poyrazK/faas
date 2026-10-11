package objectstorage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 628
func TestGCSTrackedWriteLostAcknowledgment(t *testing.T) {
	receipt := uuid.NewString()
	store := &fakeGCSStore{}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, _ := io.ReadAll(r.Body)
		if r.Method != "PUT" || r.URL.Path != "/physical/目录 /+%.txt" || string(body) != "payload" || r.ContentLength != 7 || r.Header.Get("x-goog-meta-"+ReservedUploadReceiptMetadataKey) != receipt {
			t.Error("incorrect native tracked request")
		}
		store.object = gcsObjectState{Key: "目录 /+%.txt", Size: 7, ETag: `"etag"`, Version: 123, Metadata: map[string]string{ReservedUploadReceiptMetadataKey: receipt}}
		w.WriteHeader(500) // Stored successfully, but its acknowledgment was lost.
	}))
	defer server.Close()
	p := testGCS(server.URL, store)
	_, err := p.WriteTrackedObject(t.Context(), "physical", "目录 /+%.txt", receipt, strings.NewReader("payload"), 7, ObjectMetadata{})
	if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrWriteRejected) || calls != 1 {
		t.Fatal("write was replayed or uncertainty was discarded", err, calls)
	}
	proof, err := p.ConfirmTrackedObject(t.Context(), "physical", "目录 /+%.txt", receipt, 7)
	if err != nil || proof.ProviderVersionID != "123" {
		t.Fatal(proof, err)
	}
	store.object.Metadata[ReservedUploadReceiptMetadataKey] = uuid.NewString()
	if _, err = p.ConfirmTrackedObject(t.Context(), "physical", "目录 /+%.txt", receipt, 7); !errors.Is(err, ErrConflict) {
		t.Fatal("unrelated replacement confirmed", err)
	}
}

// adr: 628
func TestGCSHistoryProofBoundToIntentAndEncryption(t *testing.T) {
	receipt := uuid.NewString()
	store := &fakeGCSStore{versions: []gcsObjectState{{Key: "other", Size: 7, Version: 1}}, next: "native-page"}
	p := testGCS(gcsDefaultEndpoint, store)
	p.encryption = EncryptionConfig{Algorithms: []string{"AES256"}}
	enc, err := p.encryption.Resolve(uuid.NewString(), api.ObjectEncryption{Algorithm: "AES256"})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	r := ObjectHistoryProofRequest{Key: "key", Receipt: receipt, SizeBytes: 7, BeforeRequest: func(context.Context) error { calls++; return nil }, Encryption: &enc}
	page, err := p.ConfirmTrackedObjectHistory(t.Context(), "physical", r)
	if !errors.Is(err, ErrConflict) || page.Cursor == "native-page" || page.Cursor == "" {
		t.Fatal(page, err)
	}
	r.Cursor = page.Cursor
	changed := r
	changed.Key = "other"
	if _, err = p.ConfirmTrackedObjectHistory(t.Context(), "physical", changed); !errors.Is(err, ErrInvalid) || calls != 1 {
		t.Fatal("cursor reused across intent", err, calls)
	}
	store.next = ""
	store.versions = []gcsObjectState{{Key: "key", Size: 7, Version: 123, ETag: `"etag"`, Metadata: map[string]string{ReservedUploadReceiptMetadataKey: receipt, ReservedObjectEncryptionMetadataKey: enc.Proof()}, KMSKeyName: "unknown-cmek"}}
	if _, err = p.ConfirmTrackedObjectHistory(t.Context(), "physical", r); !errors.Is(err, ErrConflict) {
		t.Fatal("CMEK accepted as AES256", err)
	}
	store.versions[0].KMSKeyName = ""
	page, err = p.ConfirmTrackedObjectHistory(t.Context(), "physical", r)
	if err != nil || page.UploadResult.ProviderVersionID != "123" || page.UploadResult.Encryption.Algorithm != "AES256" {
		t.Fatal(page, err)
	}
}

// adr: 628
func TestGCSTrackedCopyFencesDataAndMetadata(t *testing.T) {
	receipt := uuid.NewString()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method == "HEAD" {
			w.Header().Set("ETag", `"source"`)
			w.Header().Set("X-Goog-Generation", "11")
			w.Header().Set("X-Goog-Metageneration", "3")
			w.Header().Set("Last-Modified", time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC).Format(http.TimeFormat))
			w.Header().Set("Content-Length", "7")
			w.Header().Set("X-Goog-Meta-Owner", "alice")
			w.Header().Set("X-Goog-Meta-"+ReservedObjectTagsMetadataKey, "team=storage")
			w.Header().Set("X-Goog-Meta-"+ReservedUploadReceiptMetadataKey, uuid.NewString())
			return
		}
		if r.Method != "PUT" || r.Header.Get("X-Goog-Copy-Source") != "/source/"+url.PathEscape("目录 /+%.txt") || r.Header.Get("X-Goog-Copy-Source-Generation") != "11" || r.Header.Get("X-Goog-Copy-Source-If-Generation-Match") != "11" || r.Header.Get("X-Goog-Copy-Source-If-Metageneration-Match") != "3" || r.Header.Get("X-Goog-Metadata-Directive") != "REPLACE" || r.Header.Get("X-Goog-Meta-Owner") != "alice" || r.Header.Get("X-Goog-Meta-"+ReservedUploadReceiptMetadataKey) != receipt {
			t.Error("copy did not fence its source and replace receipt")
		}
		w.Header().Set("ETag", `"copy"`)
		w.Header().Set("X-Goog-Generation", "22")
		_, _ = io.WriteString(w, `<CopyObjectResult><ETag>&quot;copy&quot;</ETag><LastModified>2026-10-06T12:00:00Z</LastModified></CopyObjectResult>`)
	}))
	defer server.Close()
	p := testGCS(server.URL, &fakeGCSStore{})
	source, err := p.SnapshotCopySource(t.Context(), "source", "目录 /+%.txt")
	if err != nil {
		t.Fatal(err)
	}
	r := CopyObjectRequest{SourceKey: "目录 /+%.txt", DestinationKey: "destination"}
	out, err := p.CopyCrossBucketTrackedObject(t.Context(), "source", "destination", receipt, r, source, CopySourceConditions{}, ResolvedObjectEncryption{})
	if err != nil || out.ProviderVersionID != "22" || calls != 2 {
		t.Fatal(out, err, calls)
	}
	future := source.LastModified.Add(time.Second)
	_, err = p.CopyDateConditionalTrackedObject(t.Context(), "source", receipt, r, source, CopySourceConditions{IfModifiedSince: &future})
	if !errors.Is(err, ErrPreconditionFailed) || !errors.Is(err, ErrWriteRejected) || calls != 2 {
		t.Fatal("date precondition dispatched", err, calls)
	}
}

// adr: 628
func TestGCSMultipartCopyReservesReadAndValidatesRange(t *testing.T) {
	for _, tc := range []struct {
		name      string
		malformed bool
		encrypted bool
	}{{name: "valid"}, {name: "wrong range", malformed: true}, {name: "unhandled customer key", encrypted: true}} {
		t.Run(tc.name, func(t *testing.T) {
			reads, writes, budget := 0, 0, int64(0)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					reads++
					if budget != 3 || r.URL.Query().Get("generation") != "11" || r.Header.Get("X-Goog-If-Metageneration-Match") != "3" || r.Header.Get("Range") != "bytes=1-3" {
						t.Error("unfenced or unreserved source read")
					}
					w.Header().Set("ETag", `"source"`)
					w.Header().Set("X-Goog-Generation", "11")
					w.Header().Set("Content-Length", "3")
					w.Header().Set("Content-Range", fmt.Sprintf("bytes 1-3/%d", api.MinMultipartPartBytes+1))
					if tc.malformed {
						w.Header().Set("Content-Range", "bytes 2-4/7")
					}
					if tc.encrypted {
						w.Header().Set("X-Goog-Encryption-Key-Sha256", "unhandled-key")
					}
					w.WriteHeader(206)
					_, _ = io.WriteString(w, "abc")
					return
				}
				writes++
				body, _ := io.ReadAll(r.Body)
				if string(body) != "abc" || r.URL.Query().Get("uploadId") != "native-upload" || r.ContentLength != 3 {
					t.Error("part payload changed")
				}
				w.Header().Set("ETag", `"part"`)
			}))
			defer server.Close()
			p := testGCS(server.URL, &fakeGCSStore{})
			source := CopySourceSnapshot{SizeBytes: api.MinMultipartPartBytes + 1, ETag: `"source"`, ProviderVersionID: "11", ProviderMetadataVersion: "3"}
			r := MultipartPartCopyRequest{SourceKey: "source", Key: "destination", ProviderUploadID: "native-upload", PartNumber: 1, Range: &CopySourceRange{First: 1, Last: 3}}
			denied := WithMultipartCopyReadRecorder(t.Context(), func(context.Context, int64) error { return ErrConflict })
			if _, err := p.CopyMultipartPart(denied, "bucket", r, source); !errors.Is(err, ErrWriteRejected) || reads != 0 || writes != 0 {
				t.Fatal(err, reads, writes)
			}
			ctx := WithMultipartCopyReadRecorder(t.Context(), func(_ context.Context, n int64) error { budget += n; return nil })
			_, err := p.CopyMultipartPart(ctx, "bucket", r, source)
			if tc.malformed || tc.encrypted {
				if !errors.Is(err, ErrWriteRejected) || writes != 0 {
					t.Fatal(err, writes)
				}
			} else if err != nil || writes != 1 {
				t.Fatal(err, writes)
			}
		})
	}
}

type gcsControlsFixture struct {
	fakeGCSStore
	attrs  *storage.BucketAttrs
	update storage.BucketAttrsToUpdate
	meta   int64
	calls  int
}

func (s *gcsControlsFixture) BucketAttrs(context.Context, string) (*storage.BucketAttrs, error) {
	return s.attrs, nil
}
func (s *gcsControlsFixture) UpdateBucketAttrs(_ context.Context, _ string, meta int64, u storage.BucketAttrsToUpdate) error {
	s.calls++
	s.meta, s.update = meta, u
	return nil
}

// adr: 628
func TestGCSBucketEncryptionClearsOnlyObservedDefault(t *testing.T) {
	s := &gcsControlsFixture{attrs: &storage.BucketAttrs{MetaGeneration: 9, Encryption: &storage.BucketEncryption{DefaultKMSKeyName: "native-key"}}}
	p := testGCS(gcsDefaultEndpoint, s)
	if err := p.ClearBucketEncryption(t.Context(), "physical", NativeBucketEncryption{Algorithm: "AES256"}); !errors.Is(err, ErrConflict) || s.calls != 0 {
		t.Fatal(err, s.calls)
	}
	if err := p.ClearBucketEncryption(t.Context(), "physical", NativeBucketEncryption{Algorithm: "aws:kms", KeyID: "native-key"}); err != nil || s.meta != 9 || s.update.Encryption == nil || s.update.Encryption.DefaultKMSKeyName != "" {
		t.Fatal(err, s.meta, s.update)
	}
	if err := p.PutBucketVersioning(t.Context(), "physical", "Enabled"); err != nil || s.meta != 0 || s.update.VersioningEnabled != true || s.update.Encryption != nil {
		t.Fatal(err, s.update)
	}
}
