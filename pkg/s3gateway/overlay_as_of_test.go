package s3gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// asOfBranchPoint is the branch point of the as_of fixtures.
var asOfBranchPoint = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

// testVersion is one version of the as_of fixture's source.
type testVersion struct {
	key, id, body string
	// at is the offset from the branch point.
	at     time.Duration
	marker bool
}

// overlayVersionProvider lists versions the way S3 does: by key, newest
// first, a page stopping at its last key and version.
type overlayVersionProvider struct {
	*overlayTestProvider
	versions []objectstorage.ListedObjectVersion
	bodies   map[string]string
	pageSize int
	pages    int
}

func newOverlayVersionProvider(p *overlayTestProvider, pageSize int, versions []testVersion) *overlayVersionProvider {
	out := &overlayVersionProvider{overlayTestProvider: p, bodies: map[string]string{}, pageSize: pageSize}
	latest := map[string]time.Duration{}
	for _, v := range versions {
		if at, ok := latest[v.key]; !ok || v.at > at {
			latest[v.key] = v.at
		}
	}
	for _, v := range versions {
		out.versions = append(out.versions, objectstorage.ListedObjectVersion{
			Object:            objectstorage.Object{Key: v.key, Size: int64(len(v.body)), ETag: `"` + v.id + `"`, LastModified: asOfBranchPoint.Add(v.at)},
			ProviderVersionID: v.id, IsLatest: latest[v.key] == v.at, DeleteMarker: v.marker,
		})
		out.bodies[v.id] = v.body
	}
	sort.SliceStable(out.versions, func(i, j int) bool {
		a, b := out.versions[i], out.versions[j]
		if a.Key != b.Key {
			return a.Key < b.Key
		}
		return a.LastModified.After(b.LastModified)
	})
	return out
}

func (p *overlayVersionProvider) ListObjectVersionPage(_ context.Context, bucket string, r objectstorage.ObjectVersionListRequest) (objectstorage.ObjectVersionListPage, error) {
	p.pages++
	if bucket != "gregale-source" {
		return objectstorage.ObjectVersionListPage{}, nil
	}
	limit := min(int(r.Limit), p.pageSize)
	var page objectstorage.ObjectVersionListPage
	skipping := r.ProviderVersionMarker != ""
	count := 0
	for _, v := range p.versions {
		if !strings.HasPrefix(v.Key, r.Prefix) {
			continue
		}
		if skipping {
			if v.Key < r.KeyMarker || v.Key == r.KeyMarker && v.ProviderVersionID != r.ProviderVersionMarker {
				continue
			}
			if v.Key == r.KeyMarker {
				skipping = false
				continue
			}
			skipping = false
		}
		if r.ProviderVersionMarker == "" && r.KeyMarker != "" && (v.Key <= r.KeyMarker ||
			r.Delimiter != "" && strings.HasSuffix(r.KeyMarker, r.Delimiter) && strings.HasPrefix(v.Key, r.KeyMarker)) {
			continue
		}
		prefix := ""
		if r.Delimiter != "" {
			if at := strings.Index(v.Key[len(r.Prefix):], r.Delimiter); at >= 0 {
				prefix = v.Key[:len(r.Prefix)+at+len(r.Delimiter)]
			}
		}
		if prefix != "" && len(page.CommonPrefixes) > 0 && page.CommonPrefixes[len(page.CommonPrefixes)-1] == prefix {
			continue
		}
		if count == limit {
			if n := len(page.Items); n > 0 && (len(page.CommonPrefixes) == 0 || page.Items[n-1].Key > page.CommonPrefixes[len(page.CommonPrefixes)-1]) {
				page.NextKeyMarker, page.NextProviderVersionMarker = page.Items[n-1].Key, page.Items[n-1].ProviderVersionID
			} else {
				page.NextKeyMarker = page.CommonPrefixes[len(page.CommonPrefixes)-1]
			}
			return page, nil
		}
		count++
		if prefix != "" {
			page.CommonPrefixes = append(page.CommonPrefixes, prefix)
			continue
		}
		page.Items = append(page.Items, v)
	}
	return page, nil
}

func (p *overlayVersionProvider) PresignVersionRead(_ context.Context, bucket, method, key, version string, _ bool, _ int64) (objectstorage.SignedRequest, error) {
	return objectstorage.SignedRequest{URL: "https://provider.invalid/" + bucket + "/" + key + "?versionId=" + url.QueryEscape(version), Method: method}, nil
}

// roundTrip serves version reads from the versions, and the rest from the
// buckets.
func (p *overlayVersionProvider) roundTrip(r *http.Request) (*http.Response, error) {
	id := r.URL.Query().Get("versionId")
	if id == "" {
		return p.overlayTestProvider.roundTrip(r)
	}
	body, ok := p.bodies[id]
	header := make(http.Header)
	if !ok {
		return &http.Response{StatusCode: http.StatusNotFound, Header: header, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	}
	header.Set("ETag", `"`+id+`"`)
	header.Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodHead {
		body = ""
	}
	return &http.Response{StatusCode: http.StatusOK, Header: header, ContentLength: int64(len(body)), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

// asOfSourceVersions is the source's history around the branch point.
var asOfSourceVersions = []testVersion{
	{key: "a.txt", id: "a1", body: "a-old", at: -2 * time.Hour},
	{key: "a.txt", id: "a2", body: "a-new", at: time.Hour},
	{key: "b.txt", id: "b1", body: "b-later", at: time.Hour},
	{key: "c.txt", id: "c1", body: "c-old", at: -2 * time.Hour},
	{key: "c.txt", id: "c2", at: -time.Hour, marker: true},
	{key: "c.txt", id: "c3", body: "c-revived", at: time.Hour},
	{key: "d.txt", id: "d1", body: "d-old", at: -2 * time.Hour},
	{key: "d.txt", id: "d2", at: time.Hour, marker: true},
	{key: "dir/e.txt", id: "e1", body: "e-old", at: -time.Hour},
	{key: "dir/f.txt", id: "f1", body: "f-later", at: time.Hour},
	{key: "later/g.txt", id: "g1", body: "g-later", at: time.Hour},
}

// asOfSourceLive is the source's current objects.
func asOfSourceLive() map[string]string {
	return map[string]string{"a.txt": "a-new", "b.txt": "b-later", "c.txt": "c-revived", "dir/e.txt": "e-old", "dir/f.txt": "f-later", "later/g.txt": "g-later"}
}

func newAsOfFixture(t *testing.T, pageSize int, branch map[string]string) (overlayFixture, *overlayVersionProvider) {
	t.Helper()
	var versioned *overlayVersionProvider
	f := newOverlayFixture(t, asOfSourceLive(), branch, overlayFixtureOptions{
		consistency: state.ObjectBucketOverlayAsOf, asOf: asOfBranchPoint,
		provider: func(p *overlayTestProvider) objectstorage.Provider {
			versioned = newOverlayVersionProvider(p, pageSize, asOfSourceVersions)
			return versioned
		},
		roundTrip: func(r *http.Request) (*http.Response, error) { return versioned.roundTrip(r) },
	})
	return f, versioned
}

func TestAsOfCandidateTies(t *testing.T) {
	at := asOfBranchPoint
	version := func(id string, offset time.Duration, latest, marker bool) objectstorage.ListedObjectVersion {
		return objectstorage.ListedObjectVersion{Object: objectstorage.Object{Key: "k", LastModified: at.Add(offset)}, ProviderVersionID: id, IsLatest: latest, DeleteMarker: marker}
	}
	for _, tc := range []struct {
		name     string
		versions []objectstorage.ListedObjectVersion
		want     string
		live     bool
	}{
		{"newest before the branch point", []objectstorage.ListedObjectVersion{version("new", time.Hour, true, false), version("mid", -time.Hour, false, false), version("old", -2 * time.Hour, false, false)}, "mid", true},
		{"exactly at the branch point", []objectstorage.ListedObjectVersion{version("at", 0, true, false)}, "at", true},
		{"only later versions", []objectstorage.ListedObjectVersion{version("new", time.Second, true, false)}, "", false},
		{"deleted then", []objectstorage.ListedObjectVersion{version("gone", -time.Hour, false, true), version("old", -2 * time.Hour, false, false)}, "gone", false},
		{"tie: latest wins", []objectstorage.ListedObjectVersion{version("x", -time.Hour, false, false), version("y", -time.Hour, true, false)}, "y", true},
		{"tie: marker beats data", []objectstorage.ListedObjectVersion{version("x", -time.Hour, false, false), version("m", -time.Hour, false, true)}, "m", false},
		{"tie: data never beats marker", []objectstorage.ListedObjectVersion{version("m", -time.Hour, false, true), version("x", -time.Hour, false, false)}, "m", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var c asOfCandidate
			for _, v := range tc.versions {
				c.consider(v, at)
			}
			if c.version.ProviderVersionID != tc.want || c.live() != tc.live {
				t.Fatalf("candidate = %q live=%v, want %q live=%v", c.version.ProviderVersionID, c.live(), tc.want, tc.live)
			}
		})
	}
}

func TestAsOfVersionPagesAcrossAKey(t *testing.T) {
	versions := []testVersion{{key: "k", id: "late", body: "x", at: time.Hour}}
	for i := range 5 {
		versions = append(versions, testVersion{key: "k", id: "v" + strconv.Itoa(i), body: "x", at: -time.Duration(i+1) * time.Minute})
	}
	versions = append(versions, testVersion{key: "k2", id: "other", body: "x", at: -time.Second})
	for _, pageSize := range []int{1, 2, 3, 1000} {
		p := newOverlayVersionProvider(&overlayTestProvider{gatewayTestProvider: &gatewayTestProvider{}}, pageSize, versions)
		got, live, err := asOfVersion(t.Context(), p, "gregale-source", "k", asOfBranchPoint, func() bool { return true })
		if err != nil || !live || got.ProviderVersionID != "v0" {
			t.Fatalf("page size %d: version = %q live=%v err=%v, want v0", pageSize, got.ProviderVersionID, live, err)
		}
	}
	p := newOverlayVersionProvider(&overlayTestProvider{gatewayTestProvider: &gatewayTestProvider{}}, 1, append(versions[:6:6], testVersion{key: "k", id: "more", at: -time.Hour}, testVersion{key: "k", id: "most", at: -2 * time.Hour}, testVersion{key: "k", id: "too-many", at: -3 * time.Hour}))
	if _, _, err := asOfVersion(t.Context(), p, "gregale-source", "k", asOfBranchPoint, func() bool { return true }); err == nil {
		t.Fatal("a key with more versions than the page budget was answered")
	}
	if _, _, err := asOfVersion(t.Context(), p, "gregale-source", "k", asOfBranchPoint, func() bool { return false }); err != errOverlayListAnswered {
		t.Fatalf("a refused request count = %v", err)
	}
}

func TestGatewayAsOfOverlayReadsTheBranchPoint(t *testing.T) {
	f, provider := newAsOfFixture(t, 1000, map[string]string{"own.txt": "branch-own"})
	for _, tc := range []struct {
		key, method string
		status      int
		body        string
	}{
		{"a.txt", http.MethodGet, http.StatusOK, "a-old"},
		{"a.txt", http.MethodHead, http.StatusOK, ""},
		{"b.txt", http.MethodGet, http.StatusNotFound, ""},
		{"c.txt", http.MethodGet, http.StatusNotFound, ""},
		{"d.txt", http.MethodGet, http.StatusOK, "d-old"},
		{"dir/e.txt", http.MethodGet, http.StatusOK, "e-old"},
		{"dir/f.txt", http.MethodGet, http.StatusNotFound, ""},
		{"own.txt", http.MethodGet, http.StatusOK, "branch-own"},
	} {
		got := serveOverlay(t, f.handler, tc.method, "https://s3.gregale.dev/br-preview/"+tc.key)
		if got.Code != tc.status || tc.status == http.StatusOK && got.Body.String() != tc.body {
			t.Fatalf("%s %s = %d %q, want %d %q", tc.method, tc.key, got.Code, got.Body.String(), tc.status, tc.body)
		}
		if got.Header().Get("X-Amz-Version-Id") != "" {
			t.Fatalf("%s %s leaked a version id", tc.method, tc.key)
		}
	}
	// A version a lifecycle rule removed reads as missing, never as the
	// current object.
	delete(provider.bodies, "a1")
	if got := serveOverlay(t, f.handler, http.MethodGet, "https://s3.gregale.dev/br-preview/a.txt"); got.Code != http.StatusNotFound {
		t.Fatalf("an expired version = %d %q", got.Code, got.Body.String())
	}
}

func TestGatewayAsOfOverlayListsTheBranchPoint(t *testing.T) {
	want := []string{"a.txt", "d.txt", "dir/e.txt", "own.txt"}
	for _, pageSize := range []int{1, 2, 3, 1000} {
		f, _ := newAsOfFixture(t, pageSize, map[string]string{"own.txt": "branch-own"})
		for _, limit := range []string{"1", "2", "1000"} {
			keys, _ := listOverlayKeys(t, f.handler, url.Values{"max-keys": {limit}})
			if !slices.Equal(keys, want) {
				t.Fatalf("page size %d max-keys=%s listing = %v, want %v", pageSize, limit, keys, want)
			}
		}
		// later/ holds only a key written after the branch point; the
		// provider still reports the prefix (a documented limit).
		delimited := []string{"a.txt", "d.txt", "dir/", "later/", "own.txt"}
		for _, limit := range []string{"1", "1000"} {
			keys, _ := listOverlayKeys(t, f.handler, url.Values{"delimiter": {"/"}, "max-keys": {limit}})
			sort.Strings(keys)
			if !slices.Equal(keys, delimited) {
				t.Fatalf("page size %d delimited max-keys=%s listing = %v, want %v", pageSize, limit, keys, delimited)
			}
		}
		if keys, _ := listOverlayKeys(t, f.handler, url.Values{"prefix": {"dir/"}}); !slices.Equal(keys, []string{"dir/e.txt"}) {
			t.Fatalf("page size %d prefix listing = %v", pageSize, keys)
		}
	}
}

func TestGatewayAsOfOverlayWithoutVersionReadsIsUnavailable(t *testing.T) {
	f := newOverlayFixture(t, asOfSourceLive(), map[string]string{}, overlayFixtureOptions{consistency: state.ObjectBucketOverlayAsOf, asOf: asOfBranchPoint})
	if got := serveOverlay(t, f.handler, http.MethodGet, "https://s3.gregale.dev/br-preview/a.txt"); got.Code != http.StatusServiceUnavailable {
		t.Fatalf("an as_of read without version support = %d %q, want 503 (never the live object)", got.Code, got.Body.String())
	}
}

// overlayCopyStore grants the fixture's branch credential copies from the
// parent.
type overlayCopyStore struct {
	*overlayGatewayStore
	state.ObjectS3CopySourceStore // only ResolveObjectS3CopySource is called
	grant                         state.ObjectS3CopySource
	source                        state.ObjectBucket
}

func (s *overlayCopyStore) ResolveObjectS3CopySource(_ context.Context, account, credential, source, _ string) (state.ObjectS3CopySource, state.ObjectBucket, error) {
	if account != s.grant.AccountID || credential != s.grant.CredentialID || source != s.source.ID {
		return state.ObjectS3CopySource{}, state.ObjectBucket{}, state.ErrNotFound
	}
	return s.grant, s.source, nil
}

// overlayCopyProvider has the cross-bucket copy capability. Its copy methods
// are never called: the tests stop at the copy source.
type overlayCopyProvider struct {
	*overlayVersionProvider
	objectstorage.CrossBucketTrackedObjectCopier
}

type overlayLiveCopyProvider struct {
	*overlayTestProvider
	objectstorage.CrossBucketTrackedObjectCopier
}

func TestGatewayOverlayCopyThroughSource(t *testing.T) {
	branch := map[string]string{"own.txt": "branch-own"}
	var copyStore *overlayCopyStore
	withGrant := func(s *overlayGatewayStore) Store {
		copyStore = &overlayCopyStore{overlayGatewayStore: s}
		return copyStore
	}
	live := newOverlayFixture(t, asOfSourceLive(), branch, overlayFixtureOptions{
		provider: func(p *overlayTestProvider) objectstorage.Provider { return &overlayLiveCopyProvider{overlayTestProvider: p} },
		store:    withGrant,
	})
	liveStore := copyStore
	var versioned *overlayVersionProvider
	asOf := newOverlayFixture(t, asOfSourceLive(), branch, overlayFixtureOptions{
		consistency: state.ObjectBucketOverlayAsOf, asOf: asOfBranchPoint,
		provider: func(p *overlayTestProvider) objectstorage.Provider {
			versioned = newOverlayVersionProvider(p, 1000, asOfSourceVersions)
			return &overlayCopyProvider{overlayVersionProvider: versioned}
		},
		store: withGrant,
	})
	asOfStore := copyStore
	for _, f := range []struct {
		fixture overlayFixture
		store   *overlayCopyStore
	}{{live, liveStore}, {asOf, asOfStore}} {
		f.store.source = f.fixture.parent
		f.store.grant = state.ObjectS3CopySource{ID: uuid.NewString(), AccountID: f.fixture.branch.AccountID, CredentialID: f.fixture.store.credential.ID}
		if err := f.fixture.store.mem.AddObjectBucketOverlayTombstones(t.Context(), f.fixture.branch.ID, []string{"d.txt", "dir/e.txt"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name      string
		fixture   overlayFixture
		key       string
		versionID string
		noGrant   bool
		through   bool
		version   string
	}{
		{name: "live: parent-only key", fixture: live, key: "a.txt", through: true},
		{name: "live: the branch's own key", fixture: live, key: "own.txt"},
		{name: "live: a key the branch deleted", fixture: live, key: "dir/e.txt"},
		{name: "live: missing everywhere", fixture: live, key: "zzz.txt", through: true},
		{name: "live: no grant", fixture: live, key: "a.txt", noGrant: true},
		{name: "live: a branch version", fixture: live, key: "a.txt", versionID: "v"},
		{name: "as_of: pins the branch-point version", fixture: asOf, key: "a.txt", through: true, version: "a1"},
		{name: "as_of: written after the branch point", fixture: asOf, key: "b.txt"},
		{name: "as_of: deleted at the branch point", fixture: asOf, key: "c.txt"},
		{name: "as_of: a key the branch deleted", fixture: asOf, key: "d.txt"},
		{name: "as_of: the branch's own key", fixture: asOf, key: "own.txt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			credential := tc.fixture.store.credential
			if tc.noGrant {
				credential.ID = "another-credential"
			}
			req := requestContext{requestID: "r", bucket: tc.fixture.branch, credential: credential, provider: tc.fixture.served}
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPut, "/br-preview/copy", nil)
			got, ok := tc.fixture.handler.authorizeGatewayCopySource(w, r, req, gatewayCopySource{Bucket: "br-preview", Key: tc.key, VersionID: tc.versionID}, false)
			if !ok {
				t.Fatalf("copy source refused: %d %s", w.Code, w.Body.String())
			}
			if (got.copySource != nil) != tc.through {
				t.Fatalf("copy through = %v, want %v", got.copySource != nil, tc.through)
			}
			if tc.through && (got.copySource.ID != tc.fixture.parent.ID || got.copyGrantID == "" || got.overlayCopyVersion != tc.version) {
				t.Fatalf("copy through source = %s grant %q version %q, want %s version %q", got.copySource.ID, got.copyGrantID, got.overlayCopyVersion, tc.fixture.parent.ID, tc.version)
			}
		})
	}
}
