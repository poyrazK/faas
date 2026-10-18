package objectstorage

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectinventory"
)

func ownedInventoryTestPage() objectinventory.Page {
	return objectinventory.Page{Version: 1, Kind: objectinventory.Objects, Bucket: "bucket", Generation: uuid.NewString(), Snapshot: uuid.NewString() + ":1", ObservedAt: time.Now().UTC(), Buckets: []objectinventory.Bucket{}, Uploads: []objectinventory.Upload{}, Objects: []objectinventory.Object{{Key: "a", ID: uuid.NewString(), Size: 3, ETag: `"etag"`, SHA256: strings.Repeat("a", 64), Modified: time.Now().UTC()}}}
}

func TestS3OwnedInventoryRejectsIncompleteEvidence(t *testing.T) {
	for _, change := range []string{"none", "missing size", "null size", "duplicate size", "unknown field", "missing completeness", "missing array", "wrong bucket", "wrong generation", "bad snapshot", "negative size", "foreign kind", "native identity missing", "duplicate key", "unordered keys", "no revision", "empty continuation", "oversized response", "nonjson", "unavailable", "denied"} {
		t.Run(change, func(t *testing.T) {
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/bucket" || r.URL.Query().Get(objectinventory.Query) != objectinventory.Objects || !strings.Contains(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256") {
					t.Error("missing signed private inventory request", r.Method, r.URL)
				}
				page := ownedInventoryTestPage()
				switch change {
				case "wrong bucket":
					page.Bucket = "other"
				case "wrong generation":
					page.Generation = "foreign"
				case "bad snapshot":
					page.Snapshot = "unknown"
				case "negative size":
					page.Objects[0].Size = -1
				case "foreign kind":
					page.Kind = objectinventory.Uploads
				case "native identity missing":
					page.Objects[0].ID = ""
				case "duplicate key":
					page.Objects = append(page.Objects, page.Objects[0])
				case "unordered keys":
					page.Objects = append(page.Objects, page.Objects[0])
					page.Objects[0].Key = "z"
				case "no revision":
					page.Snapshot = ""
				case "empty continuation":
					page.Objects = []objectinventory.Object{}
					page.NextCursor = "next"
				case "unavailable":
					w.WriteHeader(503)
					return
				case "denied":
					w.WriteHeader(403)
					return
				}
				body, _ := json.Marshal(page)
				switch change {
				case "missing size":
					body = []byte(strings.Replace(string(body), `"size":3,`, "", 1))
				case "null size":
					body = []byte(strings.Replace(string(body), `"size":3`, `"size":null`, 1))
				case "duplicate size":
					body = []byte(strings.Replace(string(body), `"size":3`, `"size":0,"size":3`, 1))
				case "unknown field":
					body = []byte(strings.Replace(string(body), `"size":3`, `"size":3,"unknown":0`, 1))
				case "missing completeness":
					body = []byte(strings.Replace(string(body), `,"next_cursor":""`, "", 1))
				case "missing array":
					body = []byte(strings.Replace(string(body), `"uploads":[],`, "", 1))
				case "oversized response":
					body = []byte(strings.Repeat(" ", 5<<20))
				}
				w.Header().Set("Content-Type", "application/json")
				if change == "nonjson" {
					w.Header().Set("Content-Type", "application/xml")
				}
				_, _ = w.Write(body)
			}))
			defer origin.Close()
			c := testBackend()
			c.Endpoint, c.OwnedNodeReceipts = origin.URL, true
			p, err := NewS3(c, testCredentials)
			if err != nil {
				t.Fatal(err)
			}
			bytes, keys, owned, err := CompleteOwnedInventory(t.Context(), p, "bucket", nil)
			if change == "none" {
				if err != nil || !owned || bytes != 3 || keys != 1 {
					t.Fatal(bytes, keys, owned, err)
				}
				return
			}
			want := ErrUnavailable
			if change == "denied" {
				want = ErrConfiguration
			}
			if !errors.Is(err, want) || !owned || bytes != 0 || keys != 0 {
				t.Fatal("unsafe inventory became capacity evidence", bytes, keys, owned, err)
			}
		})
	}
}

func TestS3OwnedInventoryRejectsMixedScanAndMetersEveryAttempt(t *testing.T) {
	for _, scenario := range []string{"complete", "new revision", "new generation", "new observation", "duplicate boundary", "empty final page", "cursor cycle", "server failure"} {
		t.Run(scenario, func(t *testing.T) {
			first := ownedInventoryTestPage()
			var requests atomic.Int32
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := requests.Add(1)
				page := first
				page.Objects = append([]objectinventory.Object(nil), first.Objects...)
				page.NextCursor = "second"
				if n > 1 {
					if r.URL.Query().Get("cursor") != "second" {
						t.Error("lost continuation identity")
					}
					page.NextCursor, page.Objects[0].Key = "", "b"
					switch scenario {
					case "new revision":
						page.Snapshot = uuid.NewString() + ":1"
					case "new generation":
						page.Generation = uuid.NewString()
					case "new observation":
						page.ObservedAt = page.ObservedAt.Add(-time.Second)
					case "duplicate boundary":
						page.Objects[0].Key = "a"
					case "empty final page":
						page.Objects = []objectinventory.Object{}
					case "cursor cycle":
						page.NextCursor = "second"
					case "server failure":
						w.WriteHeader(503)
						return
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(page)
			}))
			defer origin.Close()
			c := testBackend()
			c.Endpoint, c.OwnedNodeReceipts = origin.URL, true
			p, err := NewS3(c, testCredentials)
			if err != nil {
				t.Fatal(err)
			}
			var metered int
			bytes, keys, owned, err := CompleteOwnedInventory(t.Context(), p, "bucket", func(_ context.Context) error { metered++; return nil })
			if metered != 2 || requests.Load() != 2 || !owned {
				t.Fatal("native inventory retry escaped metering", metered, requests.Load(), owned)
			}
			if scenario == "complete" {
				if err != nil || bytes != 6 || keys != 2 {
					t.Fatal(bytes, keys, err)
				}
			} else if !errors.Is(err, ErrUnavailable) || bytes != 0 || keys != 0 {
				t.Fatal("partial inventory changed totals", bytes, keys, err)
			}
		})
	}
}

func TestS3ManagedInventoryDoesNotSelectPrivateProtocol(t *testing.T) {
	var requests atomic.Int32
	p := listingFixture(t, func(http.ResponseWriter, *http.Request) { requests.Add(1) })
	if _, _, owned, err := CompleteOwnedInventory(t.Context(), p, "bucket", nil); owned || err != nil || requests.Load() != 0 {
		t.Fatal(owned, err, requests.Load())
	}
}
