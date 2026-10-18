// adr: 025
package storage

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOCIListReadsAllRegistryPages(t *testing.T) {
	for _, reference := range []string{"absolute", "path", "query"} {
		t.Run(reference, func(t *testing.T) {
			const endpoint = "/v2/faas/apps/tags/list"
			const query = "cursor=one,two;three&last=first__dep1"
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != endpoint {
					t.Errorf("unexpected list path %q", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				if r.URL.RawQuery != "" {
					if r.URL.RawQuery != query {
						t.Errorf("opaque pagination query changed to %q", r.URL.RawQuery)
					}
					_, _ = io.WriteString(w, `{"name":"faas/apps","tags":["second__dep2"]}`)
					return
				}
				next := "?" + query
				if reference != "query" {
					next = endpoint + next
				}
				if reference == "absolute" {
					next = "http://" + r.Host + next
				}
				// Commas inside URI references and quoted parameters are not
				// link separators. Both repeated and combined fields are valid.
				w.Header().Add("Link", `<`+endpoint+`>; rel="first"; title="first, page"`)
				w.Header().Add("Link", `<`+next+`>; rel="next last"; title="next, page", <`+endpoint+`>; rel="prev"`)
				_, _ = io.WriteString(w, `{"name":"faas/apps","tags":["first__dep1"]}`)
			}))
			t.Cleanup(server.Close)
			backend, err := NewOCIRegistryStorageBackend(WithRegistry(server.URL), WithHTTPClient(server.Client()))
			if err != nil {
				t.Fatal(err)
			}
			keys, err := backend.List(t.Context(), "apps/")
			want := []string{"apps/first/dep1.ext4", "apps/second/dep2.ext4"}
			if err != nil || !slices.Equal(keys, want) {
				t.Errorf("List = %v, %v; want %v", keys, err, want)
			}
			if calls.Load() != 2 {
				t.Errorf("page requests = %d, want 2", calls.Load())
			}
		})
	}
}

func TestOCIListRejectsIncompletePagination(t *testing.T) {
	for _, failure := range []string{"unavailable", "missing page", "invalid JSON", "repeated page", "invalid link"} {
		t.Run(failure, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) > 2 {
					http.Error(w, "pagination loop", http.StatusInternalServerError)
					return
				}
				if r.URL.Query().Has("last") {
					switch failure {
					case "unavailable":
						w.WriteHeader(http.StatusServiceUnavailable)
					case "missing page":
						w.WriteHeader(http.StatusNotFound)
					case "invalid JSON":
						_, _ = io.WriteString(w, `{`)
					case "repeated page":
						w.Header().Set("Link", `<?last=first__dep1>; rel="next"`)
						_, _ = io.WriteString(w, `{"tags":["second__dep2"]}`)
					}
					return
				}
				link := `<?last=first__dep1>; rel="next"`
				if failure == "invalid link" {
					link = `<missing-close; rel="next"`
				}
				w.Header().Set("Link", link)
				_, _ = io.WriteString(w, `{"tags":["first__dep1"]}`)
			}))
			t.Cleanup(server.Close)
			backend, err := NewOCIRegistryStorageBackend(WithRegistry(server.URL), WithHTTPClient(server.Client()))
			if err != nil {
				t.Fatal(err)
			}
			keys, err := backend.List(t.Context(), "apps/")
			if err == nil || keys != nil {
				t.Errorf("incomplete inventory returned keys=%v, err=%v", keys, err)
			}
			if calls.Load() > 2 {
				t.Errorf("pagination repeated an already visited page (%d requests)", calls.Load())
			}
		})
	}
}

func TestOCIListRejectsCrossOriginPagination(t *testing.T) {
	var foreignCalls atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		foreignCalls.Add(1)
		_, _ = io.WriteString(w, `{"tags":[]}`)
	}))
	t.Cleanup(foreign.Close)
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Link", fmt.Sprintf(`<%s/v2/faas/apps/tags/list>; rel="next"`, foreign.URL))
		_, _ = io.WriteString(w, `{"tags":["first__dep1"]}`)
	}))
	t.Cleanup(registry.Close)
	backend, err := NewOCIRegistryStorageBackend(WithRegistry(registry.URL), WithHTTPClient(registry.Client()))
	if err != nil {
		t.Fatal(err)
	}
	keys, err := backend.List(t.Context(), "apps/")
	if err == nil || keys != nil {
		t.Errorf("foreign pagination returned keys=%v, err=%v", keys, err)
	}
	if foreignCalls.Load() != 0 {
		t.Errorf("registry pagination contacted another origin %d times", foreignCalls.Load())
	}
}

func TestOCIListBoundsCombinedPageBytes(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Link", `<?last=first__dep1>; rel="next"`)
			const body = `{"tags":["first__dep1"]}`
			_, _ = io.WriteString(w, body+strings.Repeat(" ", maxOCITagListBytes-len(body)-8))
			return
		}
		_, _ = io.WriteString(w, `{"tags":["second__dep2"]}`)
	}))
	t.Cleanup(server.Close)
	backend, err := NewOCIRegistryStorageBackend(WithRegistry(server.URL), WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	keys, err := backend.List(t.Context(), "apps/")
	if err == nil || keys != nil {
		t.Errorf("oversized combined list returned keys=%v, err=%v", keys, err)
	}
	if calls.Load() != 2 {
		t.Errorf("page requests = %d, want 2", calls.Load())
	}
}

func TestRegistryTagsPaginationOrigin(t *testing.T) {
	base, err := url.Parse("https://registry.example/v2/faas/apps/tags/list")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		reference string
		allowed   bool
	}{
		{"https://REGISTRY.example:443/v2/faas/apps/tags/list?last=tag", true},
		{"https://registry.example:8443/v2/faas/apps/tags/list", false},
		{"http://registry.example/v2/faas/apps/tags/list", false},
		{"https://user:password@registry.example/v2/faas/apps/tags/list", false},
		{"/v2/another/repository/tags/list", false},
	} {
		t.Run(tc.reference, func(t *testing.T) {
			_, err := registryTagsPageURL(base, tc.reference)
			if (err == nil) != tc.allowed {
				t.Errorf("pagination URL allowed=%v, want %v (error=%v)", err == nil, tc.allowed, err)
			}
		})
	}
}
