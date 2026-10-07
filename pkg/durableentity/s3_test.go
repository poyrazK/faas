package durableentity

// adr: 678

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/objectstorage"
)

// This is a conditional S3 wire fixture, not evidence qualifying a real bucket.
func newS3WireServer(t *testing.T) *httptest.Server {
	t.Helper()
	objects := newMemoryStore()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") || r.URL.Path != "/private" && !strings.HasPrefix(r.URL.Path, "/private/") {
			t.Error("missing signed private-bucket request")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		key := strings.TrimPrefix(r.URL.Path, "/private/")
		if r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2" {
			if r.URL.Query().Get("delimiter") == "" {
				writeFlatS3Listing(t, w, r, objects)
				return
			}
			limit, err := strconv.ParseInt(r.URL.Query().Get("max-keys"), 10, 32)
			if err != nil || limit <= 0 || r.URL.Query().Get("delimiter") != "/" {
				writeS3Error(w, http.StatusBadRequest, "InvalidArgument")
				return
			}
			page, err := objects.ListEntityPrefixes(r.Context(), r.URL.Query().Get("prefix"), r.URL.Query().Get("continuation-token"), int32(limit))
			if err != nil {
				writeS3Error(w, http.StatusServiceUnavailable, "ServiceUnavailable")
				return
			}
			response := struct {
				XMLName   xml.Name `xml:"ListBucketResult"`
				Truncated bool     `xml:"IsTruncated"`
				Next      string   `xml:"NextContinuationToken,omitempty"`
				Prefixes  []struct {
					Prefix string `xml:"Prefix"`
				} `xml:"CommonPrefixes"`
			}{Truncated: page.NextCursor != "", Next: page.NextCursor}
			for _, prefix := range page.Prefixes {
				response.Prefixes = append(response.Prefixes, struct {
					Prefix string `xml:"Prefix"`
				}{prefix})
			}
			w.Header().Set("Content-Type", "application/xml")
			_ = xml.NewEncoder(w).Encode(response)
			return
		}
		switch r.Method {
		case http.MethodDelete:
			if err := objects.DeleteEntityObject(r.Context(), key); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet:
			body, version, err := objects.Get(r.Context(), key, 1<<20)
			if err != nil {
				writeS3Error(w, http.StatusNotFound, "NoSuchKey")
				return
			}
			w.Header().Set("ETag", version)
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			_, _ = w.Write(body)
		case http.MethodPut:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			version := r.Header.Get("If-Match")
			if version == "" && r.Header.Get("If-None-Match") != "*" {
				t.Error("unconditional state write")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			etag, err := objects.Put(r.Context(), key, body, version)
			if errors.Is(err, ErrConflict) {
				writeS3Error(w, http.StatusPreconditionFailed, "PreconditionFailed")
				return
			}
			if err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("ETag", etag)
		default:
			t.Error("unexpected S3 method", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(upstream.Close)
	return upstream
}

func writeFlatS3Listing(t *testing.T, w http.ResponseWriter, r *http.Request, objects *memoryStore) {
	t.Helper()
	limit, err := strconv.ParseInt(r.URL.Query().Get("max-keys"), 10, 32)
	if err != nil || limit <= 0 {
		writeS3Error(w, http.StatusBadRequest, "InvalidArgument")
		return
	}
	page, err := objects.ListEntityObjects(r.Context(), r.URL.Query().Get("prefix"), r.URL.Query().Get("continuation-token"), int32(limit))
	if err != nil {
		writeS3Error(w, http.StatusServiceUnavailable, "ServiceUnavailable")
		return
	}
	type content struct {
		Key  string `xml:"Key"`
		Size int    `xml:"Size"`
	}
	response := struct {
		XMLName   xml.Name  `xml:"ListBucketResult"`
		Truncated bool      `xml:"IsTruncated"`
		Next      string    `xml:"NextContinuationToken,omitempty"`
		Items     []content `xml:"Contents"`
	}{Truncated: page.NextCursor != "", Next: page.NextCursor}
	for _, key := range page.Keys {
		body, _, err := objects.Get(r.Context(), key, 1<<20)
		if err != nil {
			t.Error(err)
			continue
		}
		response.Items = append(response.Items, content{Key: key, Size: len(body)})
	}
	w.Header().Set("Content-Type", "application/xml")
	_ = xml.NewEncoder(w).Encode(response)
}

func TestS3WireRestoreAndRetryAcrossEngineRestart(t *testing.T) {
	upstream := newS3WireServer(t)
	provider, err := objectstorage.NewS3(objectstorage.BackendConfig{Endpoint: upstream.URL, S3Region: "us-east-1", PathStyle: true, AccessKeyEnv: "KEY", SecretKeyEnv: "SECRET"}, func(string) string { return "fixture-only" })
	if err != nil {
		t.Fatal(err)
	}
	conditional, ok := provider.(objectstorage.ConditionalStateProvider)
	if !ok {
		t.Fatal("S3 provider missing conditional-state capability")
	}
	store, err := NewProviderStore(conditional, "private")
	if err != nil {
		t.Fatal(err)
	}
	first, err := Open(t.Context(), store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	id := ID{AccountID: "account", AppID: "app", Namespace: "customers", Key: "customer:456"}
	claim, err := first.Acquire(t.Context(), id, "first-process")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Execute(t.Context(), claim, request("one"), increment); err != nil {
		t.Fatal(err)
	}
	if err := first.Release(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
	second, err := Open(t.Context(), store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	claim, err = second.Acquire(t.Context(), id, "second-process")
	if err != nil {
		t.Fatal(err)
	}
	result, err := second.Execute(t.Context(), claim, request("one"), increment)
	if err != nil || !result.Replayed {
		t.Fatal(result, err)
	}
	assertCount(t, t.Context(), second, id, 1, 1)
}

func TestS3WireAutomaticMaintenanceResumesAndPreservesReplay(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://unreachable:invalid@127.0.0.1:1/unavailable?connect_timeout=1")
	upstream := newS3WireServer(t)
	provider, err := objectstorage.NewS3(objectstorage.BackendConfig{Endpoint: upstream.URL, S3Region: "us-east-1", PathStyle: true, AccessKeyEnv: "KEY", SecretKeyEnv: "SECRET"}, func(string) string { return "fixture-only" })
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewProviderStore(provider.(objectstorage.ConditionalStateProvider), "private")
	if err != nil {
		t.Fatal(err)
	}
	m, err := Open(t.Context(), store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.CheckMaintenance(t.Context()); err != nil {
		t.Fatal(err)
	}
	id := ID{AccountID: "account", AppID: "app", Namespace: "counters", Key: "maintenance-wire"}
	seedMaintenanceEntity(t, m, id, 14)
	deleted := 0
	for step := range 10 {
		m, err = Open(t.Context(), store, Options{})
		if err != nil {
			t.Fatal(err)
		}
		result, err := m.MaintenanceStep(t.Context(), fmt.Sprintf("replica-%d", step), map[string]bool{"app": true})
		if err != nil || result.Failed != 0 || result.Cleanup.Failed != 0 {
			t.Fatal(result, err)
		}
		deleted += result.Cleanup.Deleted
	}
	if deleted == 0 {
		t.Fatal("native adapter did not reclaim superseded objects")
	}
	assertCount(t, t.Context(), m, id, 14, 14)
	for i := range 14 {
		result, err := m.Invoke(t.Context(), id, "retry", request(fmt.Sprint(i)), increment)
		if err != nil || !result.Replayed || result.Version != uint64(i+1) {
			t.Fatal(result, err)
		}
	}
}

func writeS3Error(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, "<Error><Code>"+code+"</Code></Error>")
}
