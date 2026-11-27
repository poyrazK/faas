package storage

// adr: 20261117190000

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/storagegrant"
)

// fakeSignedGCS serves the XML API's object GET and PUT behind "signed"
// URLs: a request is honored only with the token the fake granter put in
// the URL, so the store cannot reach an object it was not granted.
type fakeSignedGCS struct {
	mu         sync.Mutex
	objects    map[string]fakeObject
	next       int64
	tokens     map[string]string // token -> "METHOD key"
	srv        *httptest.Server
	rejectNext bool
}

type fakeObject struct {
	body       []byte
	generation int64
	meta       map[string]string
}

func newFakeSignedGCS(t *testing.T) *fakeSignedGCS {
	f := &fakeSignedGCS{objects: map[string]fakeObject{}, next: 100, tokens: map[string]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSignedGCS) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := strings.TrimPrefix(r.URL.Path, "/bucket/")
	if f.tokens[r.URL.Query().Get("token")] != r.Method+" "+key {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if f.rejectNext {
		f.rejectNext = false
		w.WriteHeader(http.StatusBadRequest) // an expired signature
		return
	}
	obj, exists := f.objects[key]
	switch r.Method {
	case http.MethodGet:
		if g := r.URL.Query().Get("generation"); g != "" && (!exists || strconv.FormatInt(obj.generation, 10) != g) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if !exists {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("x-goog-generation", strconv.FormatInt(obj.generation, 10))
		for k, v := range obj.meta {
			w.Header().Set("x-goog-meta-"+k, v)
		}
		body := obj.body
		if r.Header.Get("Range") == "bytes=0-0" {
			body = body[:min(1, len(body))]
			w.WriteHeader(http.StatusPartialContent)
		}
		_, _ = w.Write(body)
	case http.MethodPut:
		match := r.Header.Get("x-goog-if-generation-match")
		if match == "" || (match == "0" && exists) || (match != "0" && (!exists || strconv.FormatInt(obj.generation, 10) != match)) {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		meta := map[string]string{}
		for name, v := range r.Header {
			if m, ok := strings.CutPrefix(strings.ToLower(name), "x-goog-meta-"); ok {
				meta[m] = v[0]
			}
		}
		f.next++
		f.objects[key] = fakeObject{body: body, generation: f.next, meta: meta}
		w.Header().Set("x-goog-generation", strconv.FormatInt(f.next, 10))
		w.WriteHeader(http.StatusOK)
	}
}

// fakeGranter signs everything, counting calls; refuse names keys it
// refuses.
type fakeGranter struct {
	store   *fakeSignedGCS
	calls   int
	refuse  map[string]bool
	n       int
	expires time.Time
}

func (g *fakeGranter) GrantObjects(_ context.Context, reqs []storagegrant.Request) ([]storagegrant.Grant, error) {
	g.calls++
	out := make([]storagegrant.Grant, len(reqs))
	for i, r := range reqs {
		if err := r.Validate(); err != nil {
			return nil, err
		}
		if g.refuse[r.Key] {
			return nil, errors.New("PermissionDenied")
		}
		g.n++
		token := strconv.Itoa(g.n)
		method := http.MethodGet
		headers := map[string]string{}
		q := url.Values{"token": {token}}
		switch r.Op {
		case storagegrant.OpGetGeneration:
			q.Set("generation", strconv.FormatInt(r.Generation, 10))
		case storagegrant.OpCreate:
			method, headers["x-goog-if-generation-match"] = http.MethodPut, "0"
		case storagegrant.OpReplace:
			method, headers["x-goog-if-generation-match"] = http.MethodPut, strconv.FormatInt(r.Generation, 10)
		}
		for k, v := range r.Metadata {
			headers["x-goog-meta-"+k] = v
		}
		g.store.mu.Lock()
		g.store.tokens[token] = method + " " + r.Key
		g.store.mu.Unlock()
		expires := g.expires
		if expires.IsZero() {
			expires = time.Now().Add(storagegrant.TTL)
		}
		out[i] = storagegrant.Grant{Method: method, URL: g.store.srv.URL + "/bucket/" + r.Key + "?" + q.Encode(), Headers: headers, Expires: expires}
	}
	return out, nil
}

func grantedFixture(t *testing.T, compression string) (*GCSStorageBackend, *fakeSignedGCS, *fakeGranter) {
	t.Helper()
	f := newFakeSignedGCS(t)
	g := &fakeGranter{store: f, refuse: map[string]bool{}}
	b, err := newGCSStorageBackend("bucket", compression, &memoryGCSStore{objects: map[string]memoryGCSObject{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UseGrants(b, g, f.srv.Client()); err == nil {
		t.Fatal("UseGrants accepted a backend that still deletes")
	}
	if _, err := DeferDeletes(b, &recordingDeferrer{}); err != nil {
		t.Fatal(err)
	}
	if n, err := UseGrants(b, g, f.srv.Client()); err != nil || n != 1 {
		t.Fatalf("UseGrants = %d, %v", n, err)
	}
	return b, f, g
}

const grantedKey = "layers/0b8f5a8e-7c1e-4f3a-9d2b-1a2b3c4d5e6f.ext4"

func TestGrantedGCS_PutGetReplace(t *testing.T) {
	for _, compression := range []string{snapshotCompressionNone, snapshotCompressionZstd} {
		t.Run(compression, func(t *testing.T) {
			b, f, _ := grantedFixture(t, compression)
			ctx := t.Context()
			if _, err := b.Get(ctx, grantedKey); !errors.Is(err, ErrNotFound) {
				t.Fatalf("missing key: %v", err)
			}
			for _, body := range []string{"first layer", "second layer"} {
				if err := b.Put(ctx, grantedKey, strings.NewReader(body)); err != nil {
					t.Fatalf("put %q: %v", body, err)
				}
				rc, err := b.Get(ctx, grantedKey)
				if err != nil {
					t.Fatal(err)
				}
				got, _ := io.ReadAll(rc)
				_ = rc.Close()
				if string(got) != body {
					t.Fatalf("get = %q, want %q", got, body)
				}
			}
			if f.objects[grantedKey].generation != 102 {
				t.Fatalf("generation = %d; want a create then a conditioned replace", f.objects[grantedKey].generation)
			}
		})
	}
}

// Deletes are deferred, never sent; listing reports nothing.
func TestGrantedGCS_DeleteDefersAndListIsEmpty(t *testing.T) {
	b, f, g := grantedFixture(t, snapshotCompressionNone)
	if err := b.Put(t.Context(), grantedKey, strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	calls := g.calls
	if err := b.Delete(t.Context(), grantedKey); err != nil {
		t.Fatalf("deferred delete: %v", err)
	}
	if _, ok := f.objects[grantedKey]; !ok || g.calls != calls {
		t.Fatal("a granted backend deleted or asked for a grant to delete")
	}
	if keys, err := b.List(t.Context(), "jobs/"); err != nil || len(keys) != 0 {
		t.Fatalf("list = %v, %v", keys, err)
	}
}

func TestGrantedGCS_ExclusiveReceipt(t *testing.T) {
	b, f, _ := grantedFixture(t, snapshotCompressionNone)
	key := "snap/0b8f5a8e-7c1e-4f3a-9d2b-1a2b3c4d5e6f/captures/c1/v2/mem"
	r, err := PutExclusiveArtifact(t.Context(), b, key, strings.NewReader("memory"), 6)
	if err != nil {
		t.Fatal(err)
	}
	if r.Generation != f.objects[key].generation {
		t.Fatalf("receipt generation %d, object %d", r.Generation, f.objects[key].generation)
	}
	rc, err := GetExclusiveArtifact(t.Context(), b, r)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(got) != "memory" {
		t.Fatalf("read %q", got)
	}
	if _, err := PutExclusiveArtifact(t.Context(), b, key, strings.NewReader("again!"), 6); !errors.Is(err, ErrArtifactExists) {
		t.Fatalf("second exclusive put: %v, want ErrArtifactExists", err)
	}
	// Retirement is recorded, never performed by the node.
	if err := RetireExclusiveArtifact(t.Context(), b, r); err == nil {
		t.Fatal("a deferrer without generations retired an artifact")
	}
	if _, ok := f.objects[key]; !ok {
		t.Fatal("retirement deleted from the node")
	}
}

// Read grants are reused until near expiry; a refused one is renewed once.
func TestGrantedGCS_ReadGrantCache(t *testing.T) {
	b, f, g := grantedFixture(t, snapshotCompressionNone)
	if err := b.Put(t.Context(), grantedKey, strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	read := func() {
		t.Helper()
		rc, err := b.Get(t.Context(), grantedKey)
		if err != nil {
			t.Fatal(err)
		}
		_ = rc.Close()
	}
	read()
	calls := g.calls
	read()
	read()
	if g.calls != calls {
		t.Fatalf("reads asked for %d more grants, want cached", g.calls-calls)
	}
	f.mu.Lock()
	f.rejectNext = true
	f.mu.Unlock()
	read()
	if g.calls != calls+1 {
		t.Fatalf("a refused grant was renewed %d times, want once", g.calls-calls)
	}
	g.refuse[grantedKey] = true
	gs := b.store.(*grantedGCSStore)
	gs.now = func() time.Time { return time.Now().Add(storagegrant.TTL) }
	if _, err := b.Get(t.Context(), grantedKey); err == nil {
		t.Fatal("an expired cached grant was used after the control plane refused")
	}
}

func TestGrantedGCS_RefusedWrite(t *testing.T) {
	b, f, g := grantedFixture(t, snapshotCompressionNone)
	g.refuse[grantedKey] = true
	if err := b.Put(t.Context(), grantedKey, bytes.NewReader([]byte("x"))); err == nil {
		t.Fatal("a refused grant wrote")
	}
	if len(f.objects) != 0 {
		t.Fatal("object written without a grant")
	}
}
