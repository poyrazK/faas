package objectstorage

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// adr: 627, 628 -- one caller reservation covers exactly one native page.
func TestGCSListingHasOneNativeAttemptPerPage(t *testing.T) {
	for _, variant := range []string{"current", "delimiter", "start-after"} {
		for _, mode := range []string{"short page", "transient error", "empty continuation", "repeated cursor"} {
			t.Run(variant+"/"+mode, func(t *testing.T) {
				requests, reservations := 0, 0
				native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests++
					q := r.URL.Query()
					if requests != reservations || r.Header.Get("Authorization") != "Bearer listing-fixture" || r.Method != http.MethodGet || r.URL.Path != "/b/physical/o" || q.Get("maxResults") != "10" || q.Get("projection") != "noAcl" || q.Get("versions") == "true" {
						t.Error("listing lost its reservation, identity, bounds or selector", r.URL, requests, reservations)
					}
					if variant == "delimiter" && (q.Get("prefix") != "dir/" || q.Get("delimiter") != "/") || variant == "start-after" && q.Get("startOffset") != "anchor" {
						t.Error("listing lost its filter", r.URL)
					}
					w.Header().Set("Content-Type", "application/json")
					if mode == "transient error" {
						w.WriteHeader(http.StatusServiceUnavailable)
						_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": 503, "message": "transient"}})
						return
					}
					key := "key-a"
					if variant == "delimiter" {
						key = "dir/key-a"
					}
					if requests > 1 {
						key += "-next"
					}
					page := map[string]any{"items": []any{map[string]any{"name": key, "size": "4", "generation": "42", "metageneration": "3", "etag": "native-etag",
						"timeCreated": "2026-11-01T00:00:00.123456789Z", "updated": "2026-11-05T00:00:00Z", "timeDeleted": "2026-11-06T00:00:00Z", "metadata": map[string]string{"owner": "customer"}}}}
					if requests == 1 || mode == "repeated cursor" {
						page["nextPageToken"] = "native-next"
					}
					if mode == "empty continuation" && requests == 1 {
						delete(page, "items")
					}
					if variant == "delimiter" {
						page["prefixes"] = []string{"dir/nested/"}
					}
					_ = json.NewEncoder(w).Encode(page)
				}))
				t.Cleanup(native.Close)
				client := newGCSHTTPClient(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "listing-fixture"}))
				objects, err := newGCSObjectListingClient(t.Context(), client, native.URL+"/")
				if err != nil {
					t.Fatal(err)
				}
				store := &googleGCSStore{objects: objects}
				list := func(cursor string) ([]gcsObjectState, []string, string, error) {
					reservations++
					r := ObjectListRequest{Cursor: cursor, Limit: 10}
					if variant == "delimiter" {
						r.Prefix, r.Delimiter = "dir/", "/"
					}
					if variant == "start-after" {
						r.StartAfter = "anchor"
					}
					return store.ListObjectsStartingAfter(t.Context(), "physical", r)
				}
				items, prefixes, next, err := list("")
				if requests != 1 {
					t.Fatal("one reservation dispatched multiple native requests", requests)
				}
				if mode == "transient error" {
					if !errors.Is(normalizeGCS(err), ErrUnavailable) {
						t.Fatal("transient error was hidden", err)
					}
					return
				}
				if err != nil || next != "native-next" {
					t.Fatal(items, next, err)
				}
				if variant == "delimiter" && len(prefixes) != 1 {
					t.Fatal("common prefixes lost", prefixes)
				}
				if mode != "empty continuation" {
					if len(items) != 1 || items[0].Size != 4 || items[0].Version != 42 || items[0].MetaVersion != 3 || items[0].Metadata["owner"] != "customer" {
						t.Fatal("object metadata changed", items)
					}
					date := "2026-11-05T00:00:00Z"
					if items[0].LastModified.Format(time.RFC3339Nano) != date || items[0].ValidUntil.Format(time.RFC3339Nano) != "2026-11-06T00:00:00Z" {
						t.Fatal("native timestamps changed", items[0])
					}
				} else if len(items) != 0 {
					t.Fatal("empty native page was filled automatically", items)
				}
				items, _, next, err = list(next)
				if mode == "repeated cursor" {
					if !errors.Is(err, ErrUnavailable) || requests != 2 {
						t.Fatal("cursor did not advance", err, requests)
					}
					return
				}
				if err != nil || requests != 2 || reservations != 2 || len(items) != 1 || next != "" {
					t.Fatal("caller could not resume native continuation", items, next, err, requests)
				}
			})
		}
	}
}
