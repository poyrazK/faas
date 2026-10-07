package e2etest

// adr: 638

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

// DurableEntityS3Fixture is a process-independent conditional S3 wire fixture.
// It tests native runtime integration, not a real provider's durability or SLA.
// The test process owns its objects; replacing apid cannot reset them.
type DurableEntityS3Fixture struct {
	server  *httptest.Server
	mu      sync.Mutex
	objects map[string]entityFixtureObject
	version uint64
}

type entityFixtureObject struct {
	body []byte
	etag string
}

func NewDurableEntityS3Fixture(t *testing.T) *DurableEntityS3Fixture {
	t.Helper()
	f := &DurableEntityS3Fixture{objects: map[string]entityFixtureObject{}}
	f.server = httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(f.server.Close)
	return f
}

// APIDEnv returns non-production fixture settings for this explicit app UUID.
// Apply these to apid alone; storage credentials never belong in a guest.
func (f *DurableEntityS3Fixture) APIDEnv(t *testing.T, appID string) []string {
	t.Helper()
	config := objectstorage.Config{DefaultRegion: "e2e", Defaults: map[string]string{"e2e": "entity-fixture"}, Backends: []objectstorage.BackendConfig{{
		ID: "entity-fixture", Driver: "s3", Region: "e2e", Namespace: "native-entity-fixture",
		Endpoint: f.server.URL, S3Region: "us-east-1", PathStyle: true, AllowHTTP: true,
		AccessKeyEnv: "FAAS_E2E_ENTITY_ACCESS_KEY", SecretKeyEnv: "FAAS_E2E_ENTITY_SECRET_KEY",
	}}}
	registry, err := objectstorage.NewRegistry(config, func(string) string { return "fixture-only" }, map[string]objectstorage.Factory{"s3": objectstorage.NewS3})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := registry.Default("e2e")
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "entity-storage.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return []string{
		"FAAS_OBJECT_STORAGE_CONFIG=" + path,
		"FAAS_E2E_ENTITY_ACCESS_KEY=fixture-only", "FAAS_E2E_ENTITY_SECRET_KEY=fixture-only",
		"FAAS_DURABLE_ENTITIES_ENABLED=1", "FAAS_DURABLE_ENTITY_BACKEND=" + backend.ID,
		"FAAS_DURABLE_ENTITY_BACKEND_FINGERPRINT=" + backend.Fingerprint,
		"FAAS_DURABLE_ENTITY_BUCKET=entity-fixture", "FAAS_DURABLE_ENTITY_APPS=" + appID,
	}
}

func (f *DurableEntityS3Fixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") ||
		!strings.HasPrefix(r.URL.Path, "/entity-fixture/") {
		entityFixtureError(w, http.StatusForbidden, "AccessDenied")
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/entity-fixture/")
	switch r.Method {
	case http.MethodGet:
		f.mu.Lock()
		object, ok := f.objects[key]
		f.mu.Unlock()
		if !ok {
			entityFixtureError(w, http.StatusNotFound, "NoSuchKey")
			return
		}
		w.Header().Set("ETag", object.etag)
		w.Header().Set("Content-Length", strconv.Itoa(len(object.body)))
		_, _ = w.Write(object.body)
	case http.MethodPut:
		f.put(w, r, key)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *DurableEntityS3Fixture) put(w http.ResponseWriter, r *http.Request, key string) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, api.MaxDurableEntityInvocationBytes))
	if err != nil {
		entityFixtureError(w, http.StatusBadRequest, "InvalidArgument")
		return
	}
	match, none := r.Header.Get("If-Match"), r.Header.Get("If-None-Match")
	if match == "" && none != "*" || match != "" && none != "" {
		entityFixtureError(w, http.StatusBadRequest, "InvalidArgument")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	old, exists := f.objects[key]
	if match == "" && exists || match != "" && (!exists || old.etag != match) {
		entityFixtureError(w, http.StatusPreconditionFailed, "PreconditionFailed")
		return
	}
	f.version++
	etag := strconv.Quote("fixture-" + strconv.FormatUint(f.version, 10))
	f.objects[key] = entityFixtureObject{body: body, etag: etag}
	w.Header().Set("ETag", etag)
}

func entityFixtureError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, "<Error><Code>"+code+"</Code></Error>")
}
