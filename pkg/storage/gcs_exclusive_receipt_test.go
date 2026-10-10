// adr: 568 — modeled generation receipts do not qualify native VM lifecycle.
package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gcs "cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

type receiptGCSStore struct {
	*exclusiveMemoryGCSStore
	reads, retirements, ordinaryDeletes int
	changeReceipt                       func(*gcsExclusiveObjectReceipt)
}

func (s *receiptGCSStore) PutExclusiveReceipt(ctx context.Context, bucket, key string, body io.Reader, metadata map[string]string) (gcsExclusiveObjectReceipt, error) {
	if err := s.PutExclusive(ctx, bucket, key, body, metadata); err != nil {
		return gcsExclusiveObjectReceipt{}, err
	}
	object := s.objects[bucket+"/"+key]
	r := gcsExclusiveObjectReceipt{Generation: object.generation, Size: int64(len(object.body))}
	if s.changeReceipt != nil {
		s.changeReceipt(&r)
	}
	return r, nil
}
func (s *receiptGCSStore) GetGeneration(_ context.Context, bucket, key string, generation, size int64) (io.ReadCloser, error) {
	s.reads++
	o, ok := s.objects[bucket+"/"+key]
	if !ok || o.generation != generation {
		return nil, gcs.ErrObjectNotExist
	}
	if int64(len(o.body)) != size {
		return nil, ErrArtifactReceiptMismatch
	}
	return io.NopCloser(bytes.NewReader(o.body)), nil
}
func (s *receiptGCSStore) RetireGeneration(_ context.Context, bucket, key string, generation int64) error {
	s.retirements++
	o, ok := s.objects[bucket+"/"+key]
	if !ok {
		return gcs.ErrObjectNotExist
	}
	if o.generation != generation {
		return &googleapi.Error{Code: http.StatusPreconditionFailed}
	}
	delete(s.objects, bucket+"/"+key)
	return nil
}
func (s *receiptGCSStore) Delete(context.Context, string, string) error {
	s.ordinaryDeletes++
	return errors.New("unconditional deletion forbidden")
}

func receiptGCSFixture(t *testing.T, compression string) (*GCSStorageBackend, *receiptGCSStore) {
	t.Helper()
	s := &receiptGCSStore{exclusiveMemoryGCSStore: &exclusiveMemoryGCSStore{memoryGCSStore: newMemoryGCSStore()}}
	b, err := newGCSStorageBackend("gregale-artifacts-test", compression, s)
	if err != nil {
		t.Fatal(err)
	}
	return b, s
}

func TestExclusiveArtifactReceiptBindsSourceSizeCompressionAndGeneration(t *testing.T) {
	for _, kind := range []string{"mem", "drive", "vmstate", "backing"} {
		t.Run(kind, func(t *testing.T) {
			b, s := receiptGCSFixture(t, snapshotCompressionZstd)
			key := exclusiveCapturePrefix + kind
			body := bytes.Repeat([]byte("original-pinned-source"), 8192)
			r, err := PutExclusiveArtifact(t.Context(), b, key, bytes.NewReader(body), int64(len(body)))
			if err != nil {
				t.Fatal(err)
			}
			object := s.objects[b.bucket+"/"+key]
			if r.Generation != 1 || r.Key != key || r.ObjectKey != key || r.StoredBytes != int64(len(object.body)) || r.LogicalBytes != int64(len(body)) {
				t.Fatal("receipt was not supplied by original successful commit", r)
			}
			if (kind == "mem" || kind == "drive") && (r.Encoding != snapshotCompressionZstd || r.StoredBytes >= r.LogicalBytes) {
				t.Fatal("receipt lost encoded byte accounting", r)
			}
			reader, err := GetExclusiveArtifact(t.Context(), b, r)
			if err != nil {
				t.Fatal(err)
			}
			actual, err := io.ReadAll(reader)
			if err := errors.Join(err, reader.Close()); err != nil || !bytes.Equal(actual, body) {
				t.Fatal("receipt-bound read changed source", err)
			}
			if err := RetireExclusiveArtifact(t.Context(), b, r); err != nil {
				t.Fatal(err)
			}
			if err := RetireExclusiveArtifact(t.Context(), b, r); err != nil {
				t.Fatal("missing acknowledged generation was not idempotent", err)
			}
			if s.ordinaryDeletes != 0 {
				t.Fatal("retirement used ordinary deletion")
			}
		})
	}
}

func TestExclusiveArtifactReceiptRefusesUncertainAndInvalidAcknowledgements(t *testing.T) {
	for _, outcome := range []string{"lost", "generation", "size", "short", "long", "early", "cancelled"} {
		t.Run(outcome, func(t *testing.T) {
			b, s := receiptGCSFixture(t, snapshotCompressionNone)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var source io.Reader = strings.NewReader("original")
			var size int64 = 8
			switch outcome {
			case "lost":
				s.committed = errors.New("commit acknowledgement lost")
			case "generation":
				s.changeReceipt = func(r *gcsExclusiveObjectReceipt) { r.Generation = 0 }
			case "size":
				s.changeReceipt = func(r *gcsExclusiveObjectReceipt) { r.Size++ }
			case "short":
				size++
			case "long":
				size--
			case "early":
				s.consume = func(context.Context, io.Reader) error { return nil }
			case "cancelled":
				s.consume = func(_ context.Context, r io.Reader) error { _, err := io.ReadAll(r); cancel(); return err }
			}
			r, err := PutExclusiveArtifact(ctx, b, exclusiveCapturePrefix+"vmstate", source, size)
			if err == nil || r.Version != 0 || s.reads != 0 || s.retirements != 0 {
				t.Fatal("uncertain writer adopted a receipt or performed cleanup", r, err)
			}
		})
	}
}

func TestExclusiveArtifactReceiptRefusesReplacementAndWrongCanonicalRoute(t *testing.T) {
	b, s := receiptGCSFixture(t, snapshotCompressionNone)
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	fallback, err := NewFallbackStorageBackend(b, &errorStorageBackend{})
	if err != nil {
		t.Fatal(err)
	}
	cache, err := NewLocalCacheBackend(fallback, cacheRoot, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewPrefixRouter(map[string]StorageBackend{"canonical/": cache}, nil)
	if err != nil {
		t.Fatal(err)
	}
	key := "canonical/" + exclusiveCapturePrefix + "vmstate"
	r, err := PutExclusiveArtifact(t.Context(), router, key, strings.NewReader("original"), 8)
	if err != nil {
		t.Fatal(err)
	}
	if r.Key != key || r.ObjectKey != exclusiveCapturePrefix+"vmstate" {
		t.Fatal("routing changed receipt object identity", r)
	}
	reader, err := GetExclusiveArtifact(t.Context(), router, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(reader); err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(cacheRoot)
	if err != nil || len(entries) != 0 {
		t.Fatal("receipt read/write populated unowned cache", entries, err)
	}
	// Ordinary mutation models another writer replacing the original generation.
	if err := s.memoryGCSStore.Put(t.Context(), b.bucket, r.ObjectKey, strings.NewReader("replaced"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := GetExclusiveArtifact(t.Context(), router, r); !errors.Is(err, ErrNotFound) {
		t.Fatal("receipt read borrowed latest generation", err)
	}
	if err := RetireExclusiveArtifact(t.Context(), router, r); !errors.Is(err, ErrArtifactReceiptMismatch) {
		t.Fatal("retirement deleted a replacement", err)
	}
	if string(s.objects[b.bucket+"/"+r.ObjectKey].body) != "replaced" || s.ordinaryDeletes != 0 {
		t.Fatal("replacement changed after refused retirement")
	}
	foreign, _ := newGCSStorageBackend("another-artifact-bucket", snapshotCompressionNone, s)
	if _, err := GetExclusiveArtifact(t.Context(), foreign, r); !errors.Is(err, ErrArtifactReceiptMismatch) {
		t.Fatal("receipt moved to foreign location", err)
	}
}

func TestExclusiveArtifactReceiptReadMustVerifyEOFAndDigest(t *testing.T) {
	for _, outcome := range []string{"early_close", "same_size_mutation", "extra_bytes", "cancelled"} {
		t.Run(outcome, func(t *testing.T) {
			b, s := receiptGCSFixture(t, snapshotCompressionNone)
			r, err := PutExclusiveArtifact(t.Context(), b, exclusiveCapturePrefix+"vmstate", strings.NewReader("original"), 8)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if outcome == "same_size_mutation" {
				o := s.objects[b.bucket+"/"+r.ObjectKey]
				o.body = []byte("replaced")
				s.objects[b.bucket+"/"+r.ObjectKey] = o
			}
			reader, err := GetExclusiveArtifact(ctx, b, r)
			if err != nil {
				t.Fatal(err)
			}
			if outcome == "cancelled" {
				cancel()
			}
			if outcome == "extra_bytes" {
				reader.(*verifiedArtifactReader).source = io.NopCloser(strings.NewReader("original-extra"))
			}
			if outcome != "early_close" {
				if _, err := io.ReadAll(reader); err == nil {
					t.Fatal("invalid read supplied full content proof")
				}
			}
			if err := reader.Close(); err == nil {
				t.Fatal("incomplete reader supplied close acknowledgement")
			}
		})
	}
}

func TestExclusiveArtifactReceiptJSONRejectsAmbiguousEvidence(t *testing.T) {
	b, _ := receiptGCSFixture(t, snapshotCompressionNone)
	r, err := PutExclusiveArtifact(t.Context(), b, exclusiveCapturePrefix+"vmstate", strings.NewReader("original"), 8)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(r)
	for _, data := range [][]byte{append([]byte(`{"version":1,`), body[1:]...), bytes.Replace(body, []byte(`"generation":1`), []byte(`"generation":null`), 1), bytes.Replace(body, []byte(`"sha256":`), []byte(`"foreign":`), 1), append(body, []byte(` {}`)...)} {
		var decoded ExclusiveArtifactReceipt
		if err := json.Unmarshal(data, &decoded); err == nil {
			t.Fatal("ambiguous receipt accepted", string(data))
		}
	}
}

func TestGoogleGCSReceiptUsesOriginalUploadResponseAndConditionalRetirement(t *testing.T) {
	var uploads, reads, deletes, lookups atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			uploads.Add(1)
			if r.URL.Query().Get("ifGenerationMatch") != "0" {
				t.Error("upload lost create-only condition", r.URL)
			}
			_, _ = io.Copy(io.Discard, r.Body)
			_, _ = io.WriteString(w, `{"bucket":"gregale-artifacts-test","name":"capture/vmstate","generation":"7","size":"8"}`)
		case http.MethodGet:
			reads.Add(1)
			if r.URL.Query().Get("generation") != "7" || r.URL.Query().Get("alt") != "media" {
				lookups.Add(1)
				t.Error("read did not pin original generation", r.URL)
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", "8")
			w.Header().Set("X-Goog-Generation", "7")
			_, _ = io.WriteString(w, "original")
		case http.MethodDelete:
			deletes.Add(1)
			if r.URL.Query().Get("generation") != "7" || r.URL.Query().Get("ifGenerationMatch") != "7" {
				t.Error("retirement omitted original generation fence", r.URL)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			lookups.Add(1)
			t.Error("receipt performed post-write adoption lookup", r.URL)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	client, err := gcs.NewClient(ctx, gcs.WithJSONReads(), gcs.WithDisabledClientMetrics(), option.WithEndpoint(server.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	b, err := newGCSStorageBackend("gregale-artifacts-test", snapshotCompressionNone, &googleGCSArtifactStore{client: client})
	if err != nil {
		t.Fatal(err)
	}
	r, err := PutExclusiveArtifact(ctx, b, "capture/vmstate", strings.NewReader("original"), 8)
	if err != nil || r.Generation != 7 || r.StoredBytes != 8 {
		t.Fatal("original SDK upload did not supply receipt", r, err)
	}
	reader, err := GetExclusiveArtifact(ctx, b, r)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(reader)
	if err := errors.Join(readErr, reader.Close()); err != nil || string(body) != "original" {
		t.Fatal("original SDK read did not verify pinned bytes", string(body), err)
	}
	if err := RetireExclusiveArtifact(ctx, b, r); err != nil {
		t.Fatal(err)
	}
	if uploads.Load() != 1 || reads.Load() != 1 || deletes.Load() != 1 || lookups.Load() != 0 {
		t.Fatal("SDK receipt escaped original acknowledgement", uploads.Load(), deletes.Load(), lookups.Load())
	}
}
