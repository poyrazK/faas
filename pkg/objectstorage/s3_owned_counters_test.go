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
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectinventory"
)

func ownedCountersFixture() objectinventory.Counters {
	return objectinventory.Counters{Version: 1, Kind: objectinventory.CountersKind, Bucket: "bucket", Generation: uuid.NewString(), Snapshot: uuid.NewString() + ":1", ObservedAt: time.Now().Add(-time.Second).UTC(), AuditedAt: time.Now().Add(-2 * time.Second).UTC(), Totals: objectinventory.Totals{Objects: 2, CommittedBytes: 5, MultipartUploads: 1, MultipartParts: 2, MultipartBytes: 7}}
}

func TestOwnedUsageCountersRejectIncompleteOrStaleEvidence(t *testing.T) {
	for _, fault := range []string{"valid", "zero", "missing", "null", "duplicate", "unknown", "negative", "too large", "wrong bucket", "generation", "snapshot", "kind", "future observation", "stale observation", "future audit", "stale audit", "no objects", "no uploads", "nonjson", "denied", "unavailable"} {
		t.Run(fault, func(t *testing.T) {
			counters := ownedCountersFixture()
			switch fault {
			case "zero":
				counters.Totals = objectinventory.Totals{}
			case "negative":
				counters.CommittedBytes = -1
			case "too large":
				counters.Objects = api.MaxObjectStoragePolicyValue + 1
			case "wrong bucket":
				counters.Bucket = "other"
			case "generation":
				counters.Generation = "foreign"
			case "snapshot":
				counters.Snapshot = "foreign"
			case "kind":
				counters.Kind = objectinventory.UsageKind
			case "future observation":
				counters.ObservedAt = time.Now().Add(time.Hour)
			case "stale observation":
				counters.ObservedAt = time.Now().Add(-time.Hour)
				counters.AuditedAt = counters.ObservedAt
			case "future audit":
				counters.AuditedAt = time.Now().Add(time.Hour)
			case "stale audit":
				counters.AuditedAt = time.Now().Add(-time.Hour)
			case "no objects":
				counters.Objects = 0
			case "no uploads":
				counters.MultipartUploads = 0
			}
			body, _ := json.Marshal(counters)
			fields := map[string]json.RawMessage{}
			_ = json.Unmarshal(body, &fields)
			switch fault {
			case "missing":
				delete(fields, "committed_bytes")
				body, _ = json.Marshal(fields)
			case "null":
				fields["committed_bytes"] = json.RawMessage("null")
				body, _ = json.Marshal(fields)
			case "unknown":
				fields["untrusted"] = json.RawMessage("1")
				body, _ = json.Marshal(fields)
			case "duplicate":
				body = []byte(strings.TrimSuffix(string(body), "}") + `,"objects":2}`)
			case "nonjson":
				body = []byte("not json")
			}
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != "GET" || r.URL.Path != "/bucket" || r.URL.Query().Get(objectinventory.Query) != objectinventory.CountersKind || r.Header.Get("Authorization") == "" {
					t.Error("lost private signed query contract", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				if fault == "denied" {
					w.WriteHeader(403)
					return
				}
				if fault == "unavailable" {
					w.WriteHeader(503)
					return
				}
				_, _ = w.Write(body)
			}))
			defer server.Close()
			cfg := testBackend()
			cfg.Endpoint, cfg.OwnedNodeReceipts, cfg.OwnedNodeUsageCounters = server.URL, true, true
			provider, err := NewS3(cfg, testCredentials)
			if err != nil {
				t.Fatal(err)
			}
			metered := 0
			bytes, keys, owned, err := CompleteOwnedUsageCounters(t.Context(), provider, "bucket", func(context.Context) error { metered++; return nil })
			if requests.Load() != 1 || metered != 1 || !owned {
				t.Fatal("counter request escaped metering", requests.Load(), metered, owned)
			}
			if fault == "valid" || fault == "zero" {
				if err != nil || bytes != counters.CommittedBytes || keys != counters.Objects {
					t.Fatal(bytes, keys, err)
				}
			} else {
				want := ErrUnavailable
				if fault == "denied" {
					want = ErrConfiguration
				}
				if !errors.Is(err, want) || bytes != 0 || keys != 0 {
					t.Fatal("invalid counters became accounting evidence", fault, bytes, keys, err)
				}
			}
		})
	}
}

func TestOwnedUsageCountersOptInAndRequestBudget(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	config := testBackend()
	config.Endpoint, config.OwnedNodeReceipts = server.URL, true
	provider, err := NewS3(config, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, selected, err := CompleteOwnedUsageCounters(t.Context(), provider, "bucket", nil); err != nil || selected {
		t.Fatal(selected, err)
	}
	before := fingerprint(config)
	config.OwnedNodeUsageCounters = true
	if fingerprint(config) != before {
		t.Fatal("counter rollout changed immutable placement")
	}
	provider, err = NewS3(config, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	budget := errors.New("request budget exhausted")
	if _, _, selected, err := CompleteOwnedUsageCounters(t.Context(), provider, "bucket", func(context.Context) error { return budget }); !selected || !errors.Is(err, budget) {
		t.Fatal(selected, err)
	}
	config.OwnedNodeReceipts = false
	provider, err = NewS3(config, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, selected, err := CompleteOwnedUsageCounters(t.Context(), provider, "bucket", nil); !selected || !errors.Is(err, ErrConfiguration) {
		t.Fatal(selected, err)
	}
	if requests.Load() != 0 {
		t.Fatal("disabled or rejected counter dispatch reached provider", requests.Load())
	}
}

func TestOwnedUsageCountersDoNotReplaceFencedInventory(t *testing.T) {
	var counters, inventories atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get(objectinventory.Query) == objectinventory.CountersKind {
			counters.Add(1)
			_ = json.NewEncoder(w).Encode(ownedCountersFixture())
			return
		}
		inventories.Add(1)
		_ = json.NewEncoder(w).Encode(ownedInventoryTestPage())
	}))
	defer server.Close()
	cfg := testBackend()
	cfg.Endpoint, cfg.OwnedNodeReceipts, cfg.OwnedNodeUsageCounters = server.URL, true, true
	p, err := NewS3(cfg, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	if bytes, keys, _, err := CompleteOwnedUsageCounters(t.Context(), p, "bucket", nil); err != nil || bytes != 5 || keys != 2 {
		t.Fatal(bytes, keys, err)
	}
	if bytes, keys, _, err := CompleteOwnedInventory(t.Context(), p, "bucket", nil); err != nil || bytes != 3 || keys != 1 {
		t.Fatal("fenced inventory used a derived summary", bytes, keys, err)
	}
	if counters.Load() != 1 || inventories.Load() != 1 {
		t.Fatal(counters.Load(), inventories.Load())
	}
}
