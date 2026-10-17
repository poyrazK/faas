package ci

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	gcs "cloud.google.com/go/storage"
	"github.com/google/uuid"
	"google.golang.org/api/option"
)

type gcsWireObject struct {
	Name       string            `json:"name"`
	Metadata   map[string]string `json:"metadata"`
	Size       string            `json:"size"`
	Generation string            `json:"generation"`
	Data       []byte            `json:"-"`
}

type gcsWireFake struct {
	mu            sync.Mutex
	objects       map[string]gcsWireObject
	unconditional bool
	unsafeBucket  bool
}

func (f *gcsWireFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet && !strings.Contains(r.URL.Path, "/o/") {
		if f.unsafeBucket {
			_, _ = io.WriteString(w, `{"name":"ci-wire","versioning":{"enabled":true}}`)
			return
		}
		_, _ = io.WriteString(w, `{"name":"ci-wire","iamConfiguration":{"publicAccessPrevention":"enforced","uniformBucketLevelAccess":{"enabled":true}},"softDeletePolicy":{"retentionDurationSeconds":"0"}}`)
		return
	}
	if r.Method == http.MethodPost {
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || params["boundary"] == "" {
			http.Error(w, "multipart required", 400)
			return
		}
		reader := multipart.NewReader(r.Body, params["boundary"])
		metadata, err := reader.NextPart()
		if err != nil {
			http.Error(w, "metadata required", 400)
			return
		}
		var incoming gcsWireObject
		if json.NewDecoder(metadata).Decode(&incoming) != nil {
			http.Error(w, "invalid metadata", 400)
			return
		}
		data, err := reader.NextPart()
		if err != nil {
			http.Error(w, "data required", 400)
			return
		}
		incoming.Data, err = io.ReadAll(data)
		if err != nil {
			http.Error(w, "invalid data", 400)
			return
		}
		expected := r.URL.Query().Get("ifGenerationMatch")
		if expected == "" {
			f.unconditional = true
			http.Error(w, "precondition required", 400)
			return
		}
		old, exists := f.objects[incoming.Name]
		if expected == "0" && exists || expected != "0" && (!exists || old.Generation != expected) {
			w.WriteHeader(http.StatusPreconditionFailed)
			_, _ = io.WriteString(w, `{"error":{"code":412,"message":"precondition failed"}}`)
			return
		}
		generation, _ := strconv.Atoi(old.Generation)
		incoming.Generation = strconv.Itoa(generation + 1)
		incoming.Size = strconv.Itoa(len(incoming.Data))
		f.objects[incoming.Name] = incoming
		_ = json.NewEncoder(w).Encode(incoming)
		return
	}
	if r.Method == http.MethodGet {
		_, key, found := strings.Cut(r.URL.Path, "/o/")
		object, exists := f.objects[key]
		if !found || !exists || r.URL.Query().Get("generation") != "" && r.URL.Query().Get("generation") != object.Generation {
			w.WriteHeader(404)
			_, _ = io.WriteString(w, `{"error":{"code":404,"message":"not found"}}`)
			return
		}
		if r.URL.Query().Get("alt") == "media" {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", object.Size)
			w.Header().Set("X-Goog-Generation", object.Generation)
			_, _ = w.Write(object.Data)
			return
		}
		_ = json.NewEncoder(w).Encode(object)
		return
	}
	http.Error(w, "unsupported request", 400)
}

func TestCIStorageGCSConditionalRetirement(t *testing.T) {
	fake := &gcsWireFake{objects: map[string]gcsWireObject{}}
	server := httptest.NewServer(fake)
	defer server.Close()
	client, err := gcs.NewClient(t.Context(), option.WithHTTPClient(server.Client()), option.WithEndpoint(server.URL+"/storage/v1/"), option.WithoutAuthentication(), gcs.WithJSONReads(), gcs.WithDisabledClientMetrics())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	objects := &GCSObjects{client: client, bucket: client.Bucket("ci-wire")}
	if err := objects.checkBucket(t.Context()); err != nil {
		t.Fatal(err)
	}
	key := "ci-v1/" + uuid.NewString() + "/" + uuid.NewString()
	data := []byte("wire artifact")
	object := RuntimeObject{Digest: jobsDigest(data), Bytes: int64(len(data))}
	if err := objects.Publish(t.Context(), key, object, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	reader, err := objects.Open(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || !bytes.Equal(raw, data) {
		t.Fatal("GCS generation read lost payload")
	}
	if err := objects.Retire(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	if err := objects.Publish(t.Context(), key, object, bytes.NewReader(data)); err == nil {
		t.Fatal("retired object was resurrected")
	}
	if err := objects.Retire(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	if _, err := objects.Open(t.Context(), key); err == nil {
		t.Fatal("retired payload was readable")
	}
	// Retire before the delayed writer's first body byte reaches GCS.
	lateKey := "ci-v1/" + uuid.NewString() + "/" + uuid.NewString()
	blocked := &blockedStorageReader{ready: make(chan struct{}), resume: make(chan struct{}), data: bytes.NewReader(data)}
	done := make(chan error, 1)
	go func() { done <- objects.Publish(t.Context(), lateKey, object, blocked) }()
	select {
	case <-blocked.ready:
	case <-t.Context().Done():
		t.Fatal("writer never started")
	}
	if err := objects.Retire(t.Context(), lateKey); err != nil {
		close(blocked.resume)
		t.Fatal(err)
	}
	close(blocked.resume)
	if err := <-done; err == nil {
		t.Fatal("late publication bypassed retirement fence")
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.unconditional || len(fake.objects[lateKey].Data) != 0 || fake.objects[lateKey].Metadata[physicalState] != "retired" {
		t.Fatal("GCS tombstone contract violated")
	}
	fake.unsafeBucket = true
}

type blockedStorageReader struct {
	ready, resume chan struct{}
	once          sync.Once
	data          io.Reader
}

func (r *blockedStorageReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.ready); <-r.resume })
	return r.data.Read(p)
}

func TestCIStorageGCSRejectsUnqualifiedBucket(t *testing.T) {
	fake := &gcsWireFake{objects: map[string]gcsWireObject{}, unsafeBucket: true}
	server := httptest.NewServer(fake)
	defer server.Close()
	client, err := gcs.NewClient(context.Background(), option.WithHTTPClient(server.Client()), option.WithEndpoint(server.URL+"/storage/v1/"), option.WithoutAuthentication(), gcs.WithDisabledClientMetrics())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	if err := (&GCSObjects{client: client, bucket: client.Bucket("ci-wire")}).checkBucket(t.Context()); err == nil {
		t.Fatal("versioned/nonprivate bucket accepted")
	}
}
