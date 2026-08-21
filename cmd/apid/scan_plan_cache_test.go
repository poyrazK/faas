// scan_plan_cache_test.go — ADR-124 plan source cache tests.
//
// Coverage:
//   - storePlanCache + lookupPlanCache round-trip
//   - lookup miss returns os.ErrNotExist
//   - cross-account lookup returns os.ErrNotExist (no leak)
//   - planToken decode round-trip
//   - expired entry is dropped on lookup
//   - sweepExpiredCacheEntries removes TTL-expired files
//   - FAAS_PLAN_CACHE_ROOT override is honoured
package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// withPlanCacheRoot sets a per-test cache root and resets the
// sync.Once so initPlanCache picks it up. Without the reset, the
// second test in the same process would inherit the first test's
// planCacheRoot (sync.Once's contract).
func withPlanCacheRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("FAAS_PLAN_CACHE_ROOT", dir)
	planCacheRootOnce = sync.Once{}
	planCacheRoot = ""
	planCacheRootErr = nil
	return dir
}

// writeSource creates a temp source file with the given bytes
// and returns its path.
func writeSource(t *testing.T, b []byte) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "src-*.tar.gz")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	if _, err := f.Write(b); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close source: %v", err)
	}
	return f.Name()
}

func TestStoreAndLookupPlanCache_RoundTrip(t *testing.T) {
	withPlanCacheRoot(t)
	src := writeSource(t, []byte("hello-cache"))
	sha := "abc1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcd"
	const acct = "acct-1"
	if err := storePlanCache(sha, src, acct); err != nil {
		t.Fatalf("storePlanCache: %v", err)
	}
	got, err := lookupPlanCache(sha, acct)
	if err != nil {
		t.Fatalf("lookupPlanCache: %v", err)
	}
	b, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(b) != "hello-cache" {
		t.Fatalf("cache bytes = %q; want %q", b, "hello-cache")
	}
}

func TestLookupPlanCache_MissReturnsErrNotExist(t *testing.T) {
	withPlanCacheRoot(t)
	_, err := lookupPlanCache("00", "acct")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v; want os.ErrNotExist", err)
	}
}

func TestLookupPlanCache_CrossAccountReturnsErrNotExist(t *testing.T) {
	withPlanCacheRoot(t)
	src := writeSource(t, []byte("x"))
	sha := "deadbeef"
	if err := storePlanCache(sha, src, "acct-A"); err != nil {
		t.Fatalf("store: %v", err)
	}
	// Same SHA, different account → must NOT leak the path.
	_, err := lookupPlanCache(sha, "acct-B")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cross-account err = %v; want os.ErrNotExist (no leak)", err)
	}
}

func TestLookupPlanCache_ExpiredDropsInMemoryEntry(t *testing.T) {
	withPlanCacheRoot(t)
	src := writeSource(t, []byte("x"))
	sha := "feedface"
	if err := storePlanCache(sha, src, "acct"); err != nil {
		t.Fatalf("store: %v", err)
	}
	// Force-expire the in-memory entry by mutating expiresAt.
	v, ok := planSourceCache.Load(sha)
	if !ok {
		t.Fatalf("entry not stored")
	}
	cs := v.(*cachedSource)
	cs.expiresAt = time.Now().Add(-1 * time.Minute)
	_, err := lookupPlanCache(sha, "acct")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v; want os.ErrNotExist after expiry", err)
	}
}

func TestSweepExpiredCacheEntries_RemovesOldFiles(t *testing.T) {
	root := withPlanCacheRoot(t)
	// Drop a file with mtime far in the past directly.
	old := filepath.Join(root, "old.tar.gz")
	if err := os.WriteFile(old, []byte("old"), 0o640); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	past := time.Now().Add(-2 * planCacheTTL)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	if err := sweepExpiredCacheEntries(root); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("expected old.tar.gz removed; stat err = %v", err)
	}
}

func TestDecodePlanToken_RoundTrip(t *testing.T) {
	pt := planTokenWire{
		Hash:      "abcd",
		AccountID: "acct-1",
		Slug:      "demo",
		TSUnix:    1700000000,
	}
	b, _ := json.Marshal(pt)
	encoded := base64.StdEncoding.EncodeToString(b)
	got, err := decodePlanToken(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.AccountID != pt.AccountID || got.Hash != pt.Hash || got.Slug != pt.Slug {
		t.Fatalf("round-trip mismatch: %+v vs %+v", got, pt)
	}
}

func TestDecodePlanToken_RejectsEmpty(t *testing.T) {
	if _, err := decodePlanToken(""); err == nil {
		t.Fatal("empty plan_token accepted; want error")
	}
}

func TestDecodePlanToken_RejectsMissingFields(t *testing.T) {
	// Empty Hash + AccountID — base64 + json unmarshal succeeds
	// but the required-field guard must trip.
	pt := planTokenWire{TSUnix: 1}
	b, _ := json.Marshal(pt)
	encoded := base64.StdEncoding.EncodeToString(b)
	if _, err := decodePlanToken(encoded); err == nil {
		t.Fatal("missing-field plan_token accepted; want error")
	}
}

func TestBuildCachedSourceRequest_EmitsMultipart(t *testing.T) {
	withPlanCacheRoot(t)
	src := writeSource(t, []byte("source-bytes"))
	req, err := buildCachedSourceRequest(src, "demo", []string{"alpha", "Beta"})
	if err != nil {
		t.Fatalf("buildCachedSourceRequest: %v", err)
	}
	if req.Method != http.MethodPost {
		t.Fatalf("method = %s; want POST", req.Method)
	}
	if got := req.Header.Get("Content-Type"); !strings.HasPrefix(got, "multipart/form-data") {
		t.Fatalf("Content-Type = %q; want multipart/form-data prefix", got)
	}
	// Read the body and verify both the source part and two
	// exclude parts (with the lowercased slug for "Beta").
	mr, err := req.MultipartReader()
	if err != nil {
		t.Fatalf("MultipartReader: %v", err)
	}
	var (
		gotSource  []byte
		gotExclude []string
	)
	for {
		part, perr := mr.NextPart()
		if perr == io.EOF {
			break
		}
		if perr != nil {
			t.Fatalf("NextPart: %v", perr)
		}
		switch part.FormName() {
		case "source":
			b, _ := io.ReadAll(part)
			gotSource = b
		case "exclude":
			b, _ := io.ReadAll(part)
			gotExclude = append(gotExclude, string(b))
		}
	}
	if string(gotSource) != "source-bytes" {
		t.Fatalf("source bytes = %q; want %q", gotSource, "source-bytes")
	}
	want := []string{"alpha", "beta"} // lowercased
	if len(gotExclude) != len(want) {
		t.Fatalf("len(exclude) = %d; want %d (%v)", len(gotExclude), len(want), gotExclude)
	}
	for i, e := range want {
		if gotExclude[i] != e {
			t.Fatalf("exclude[%d] = %q; want %q", i, gotExclude[i], e)
		}
	}
}
