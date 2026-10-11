// adr: 568 — exclusive upload tests do not grant native capture evidence.
package storage

import (
	"bytes"
	"context"
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

const exclusiveCapturePrefix = "snap/550e8400-e29b-41d4-a716-446655440000/warm/captures/660e8400-e29b-41d4-a716-446655440001/v2/"

type exclusiveMemoryGCSStore struct {
	*memoryGCSStore
	effects   int
	ordinary  int
	committed error
	consume   func(context.Context, io.Reader) error
}

func (s *exclusiveMemoryGCSStore) Put(context.Context, string, string, io.Reader, map[string]string) error {
	s.ordinary++
	return errors.New("legacy writer must never supply exclusive publication")
}

func (s *exclusiveMemoryGCSStore) PutExclusive(ctx context.Context, bucket, key string, body io.Reader, metadata map[string]string) error {
	s.effects++
	if s.consume != nil {
		return s.consume(ctx, body)
	}
	if _, present := s.objects[bucket+"/"+key]; present {
		return &googleapi.Error{Code: http.StatusPreconditionFailed}
	}
	if err := s.memoryGCSStore.Put(ctx, bucket, key, body, metadata); err != nil {
		return err
	}
	return s.committed
}

func exclusiveGCSFixture(t *testing.T, compression string) (*GCSStorageBackend, *exclusiveMemoryGCSStore) {
	t.Helper()
	store := &exclusiveMemoryGCSStore{memoryGCSStore: newMemoryGCSStore()}
	backend, err := newGCSStorageBackend("gregale-artifacts-test", compression, store)
	if err != nil {
		t.Fatal(err)
	}
	return backend, store
}

func TestGCSExclusivePublicationStreamsWithoutNamedSpools(t *testing.T) {
	for _, kind := range []string{"mem", "drive", "vmstate", "backing"} {
		t.Run(kind, func(t *testing.T) {
			backend, store := exclusiveGCSFixture(t, snapshotCompressionZstd)
			scratch := t.TempDir()
			t.Setenv("TMPDIR", scratch)
			payload := make([]byte, 4<<20)
			copy(payload, "original-pinned-capture")
			payload[len(payload)-1] = 42
			key := exclusiveCapturePrefix + kind
			if err := PutExclusive(t.Context(), backend, key, bytes.NewReader(payload), int64(len(payload))); err != nil {
				t.Fatal(err)
			}
			object := store.objects[backend.bucket+"/"+key]
			if kind == "mem" || kind == "drive" {
				if object.metadata[gcsEncodingMetadata] != snapshotCompressionZstd || object.metadata[gcsUncompressedSizeMetadata] != "4194304" || len(object.body) >= len(payload)/10 {
					t.Fatal("streaming upload lost its compression/size contract", object.metadata, len(object.body))
				}
			} else if len(object.metadata) != 0 {
				t.Fatal("small capture sidecar acquired compression metadata")
			}
			reader, err := backend.Get(t.Context(), key)
			if err != nil {
				t.Fatal(err)
			}
			actual, readErr := io.ReadAll(reader)
			if err := errors.Join(readErr, reader.Close()); err != nil || !bytes.Equal(actual, payload) {
				t.Fatal("exclusive object changed its original bytes", err)
			}
			if err := PutExclusive(t.Context(), backend, key, strings.NewReader("replacement"), 11); !errors.Is(err, ErrArtifactExists) {
				t.Fatal("existing capture was replaced", err)
			}
			if store.ordinary != 0 || store.objects[backend.bucket+"/"+key].generation != 1 || !bytes.Equal(store.objects[backend.bucket+"/"+key].body, object.body) {
				t.Fatal("exclusive upload borrowed legacy writes or changed an existing generation")
			}
			entries, err := os.ReadDir(scratch)
			if err != nil || len(entries) != 0 {
				t.Fatal("native publication created a named scratch artifact", entries, err)
			}
		})
	}
}

func TestGCSExclusivePublicationLostAcknowledgementCannotOverwrite(t *testing.T) {
	backend, store := exclusiveGCSFixture(t, snapshotCompressionNone)
	lost := errors.New("commit response lost")
	store.committed = lost
	key := exclusiveCapturePrefix + "vmstate"
	if err := PutExclusive(t.Context(), backend, key, strings.NewReader("original"), 8); !errors.Is(err, lost) {
		t.Fatal("uncertain commit supplied success", err)
	}
	store.committed = nil
	if err := PutExclusive(t.Context(), backend, key, strings.NewReader("replayed"), 8); !errors.Is(err, ErrArtifactExists) {
		t.Fatal("uncertain publication was overwritten", err)
	}
	if object := store.objects[backend.bucket+"/"+key]; string(object.body) != "original" || object.generation != 1 {
		t.Fatal("uncertain publication changed the committed original")
	}
}

type exclusiveSourceError struct{ err error }

func (r exclusiveSourceError) Read(p []byte) (int, error) {
	return copy(p, "abc"), r.err
}

func TestGCSExclusivePublicationRejectsChangedSizeAndSourceErrors(t *testing.T) {
	for _, compression := range []string{snapshotCompressionNone, snapshotCompressionZstd} {
		for _, outcome := range []string{"short", "long", "last_bytes_error", "wrapped_eof"} {
			t.Run(compression+"/"+outcome, func(t *testing.T) {
				backend, store := exclusiveGCSFixture(t, compression)
				var source io.Reader = strings.NewReader("abc")
				var size int64 = 3
				switch outcome {
				case "short":
					size++
				case "long":
					size--
				case "last_bytes_error":
					source = exclusiveSourceError{errors.New("original read failed")}
				case "wrapped_eof":
					source = exclusiveSourceError{errors.Join(io.EOF, errors.New("source interrupted"))}
				}
				if err := PutExclusive(t.Context(), backend, exclusiveCapturePrefix+"mem", source, size); err == nil || len(store.objects) != 0 {
					t.Fatal("incomplete source acquired a committed capture object", err)
				}
			})
		}
	}
}

func TestExclusivePublicationWrappersKeepCanonicalWriterAndSkipCacheSpools(t *testing.T) {
	backend, store := exclusiveGCSFixture(t, snapshotCompressionNone)
	legacy, err := newGCSStorageBackend("gregale-artifacts-legacy", snapshotCompressionNone, newMemoryGCSStore())
	if err != nil {
		t.Fatal(err)
	}
	fallback, err := NewFallbackStorageBackend(backend, legacy)
	if err != nil {
		t.Fatal(err)
	}
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	cache, err := NewLocalCacheBackend(fallback, cacheRoot, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewPrefixRouter(map[string]StorageBackend{"canonical/": cache, "legacy/": legacy}, nil)
	if err != nil {
		t.Fatal(err)
	}
	key := "canonical/" + exclusiveCapturePrefix + "vmstate"
	if err := PutExclusive(t.Context(), router, key, strings.NewReader("original"), 8); err != nil {
		t.Fatal(err)
	}
	if string(store.objects[backend.bucket+"/"+exclusiveCapturePrefix+"vmstate"].body) != "original" || store.effects != 1 || store.ordinary != 0 {
		t.Fatal("wrapper selected a different canonical destination")
	}
	entries, err := os.ReadDir(cacheRoot)
	if err != nil || len(entries) != 0 {
		t.Fatal("exclusive upload populated a named cache spool", entries, err)
	}
	if err := CheckExclusivePut(t.Context(), router, "legacy/"+exclusiveCapturePrefix+"mem"); !errors.Is(err, ErrExclusivePutUnsupported) {
		t.Fatal("unsupported delegate acquired publication capability", err)
	}
	blocked, err := NewFallbackStorageBackend(legacy, backend)
	if err != nil {
		t.Fatal(err)
	}
	if err := PutExclusive(t.Context(), blocked, exclusiveCapturePrefix+"mem", strings.NewReader("abc"), 3); !errors.Is(err, ErrExclusivePutUnsupported) || store.effects != 1 {
		t.Fatal("unsupported primary borrowed fallback publication", err)
	}
}

func TestGCSExclusivePublicationJoinsCancelledAndEarlyConsumers(t *testing.T) {
	for _, outcome := range []string{"cancelled", "early_success", "consumer_error"} {
		t.Run(outcome, func(t *testing.T) {
			backend, store := exclusiveGCSFixture(t, snapshotCompressionZstd)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			store.consume = func(_ context.Context, _ io.Reader) error {
				if outcome == "cancelled" {
					cancel()
					return context.Canceled
				}
				if outcome == "early_success" {
					return nil
				}
				return errors.New("consumer failed before draining source")
			}
			done := make(chan error, 1)
			go func() {
				done <- PutExclusive(ctx, backend, exclusiveCapturePrefix+"mem", strings.NewReader("original"), 8)
			}()
			select {
			case err := <-done:
				if err == nil || len(store.objects) != 0 {
					t.Fatal("undrained encoder acquired publication success", err)
				}
			case <-time.After(time.Second):
				t.Fatal("exclusive encoder escaped its original synchronous consumer")
			}
		})
	}
}

func TestGCSExclusivePublicationRefusesEarlyUncompressedSuccess(t *testing.T) {
	backend, store := exclusiveGCSFixture(t, snapshotCompressionNone)
	store.consume = func(context.Context, io.Reader) error { return nil }
	if err := PutExclusive(t.Context(), backend, exclusiveCapturePrefix+"vmstate", strings.NewReader("original"), 8); err == nil {
		t.Fatal("undrained raw consumer acquired publication success")
	}
}

func TestGCSExclusivePublicationCancellationAfterConsumptionSuppliesNoSuccess(t *testing.T) {
	for _, compression := range []string{snapshotCompressionNone, snapshotCompressionZstd} {
		t.Run(compression, func(t *testing.T) {
			backend, store := exclusiveGCSFixture(t, compression)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			store.consume = func(_ context.Context, body io.Reader) error {
				_, err := io.ReadAll(body)
				cancel()
				return err
			}
			if err := PutExclusive(ctx, backend, exclusiveCapturePrefix+"mem", strings.NewReader("original"), 8); !errors.Is(err, context.Canceled) {
				t.Fatal("cancelled publication supplied success", err)
			}
		})
	}
}

func TestGoogleGCSExclusiveWriterUsesGenerationZeroPrecondition(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Query().Get("ifGenerationMatch") != "0" {
			t.Error("upload omitted the create-only generation fence", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPreconditionFailed)
		_, _ = io.WriteString(w, `{"error":{"code":412,"message":"object already exists"}}`)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	client, err := gcs.NewClient(ctx, gcs.WithJSONReads(), gcs.WithDisabledClientMetrics(), option.WithEndpoint(server.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	backend, err := newGCSStorageBackend("gregale-artifacts-test", snapshotCompressionNone, &googleGCSArtifactStore{client: client})
	if err != nil {
		t.Fatal(err)
	}
	if err := PutExclusive(ctx, backend, exclusiveCapturePrefix+"vmstate", strings.NewReader("original"), 8); !errors.Is(err, ErrArtifactExists) || requests.Load() == 0 {
		t.Fatal("real SDK writer did not retain the create-only refusal", err, requests.Load())
	}
}
