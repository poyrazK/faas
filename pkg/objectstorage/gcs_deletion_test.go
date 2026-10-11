package objectstorage

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 628
func TestGCSDeletionRecoveryPreservesReplacement(t *testing.T) {
	for _, pg := range []bool{false, true} {
		name := "memory"
		if pg {
			name = "postgres"
		}
		t.Run(name, func(t *testing.T) {
			f := newLifecycleServiceFixture(t, pg)
			f.enableVersioning(t)
			store := &fakeGCSStore{bucketState: gcsBucketState{VersioningEnabled: true}, object: gcsObjectState{Key: "key", Version: 123}}
			deletes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				deletes++
				if r.Method != "DELETE" || r.URL.Query().Has("generation") || r.Header.Get("X-Goog-If-Generation-Match") != "123" {
					t.Error("ordinary deletion lost captured generation fence")
				}
				if deletes == 1 {
					w.WriteHeader(500)
					return
				}
				// The first request archived 123; 456 is a concurrent replacement.
				if store.object.Version != 456 {
					t.Error("replacement fixture absent")
				}
				w.WriteHeader(412)
			}))
			defer server.Close()
			p := testGCS(server.URL, store)
			service := DeletionService{Store: f.st, Provider: p}
			j, err := service.Start(t.Context(), f.bucket, "key", "", uuid.NewString(), f.policy)
			if err == nil || j.State != "dispatched" || j.ProviderStatus != "GCS_Enabled" || j.ReservedBytes != 0 || deletes != 1 || len(j.Baseline) != 1 {
				t.Fatal(j, err, deletes)
			}
			store.object.Version = 456
			f.expire(j.ID)
			service.Store = f.reopen()
			out, err := service.Recover(t.Context(), f.bucket, j.ID)
			if err != nil || out.State != "completed" || out.DeleteMarker || out.VersionID != "" || deletes != 2 {
				t.Fatal(out, err, deletes)
			}
			usage, err := f.st.ObjectUsage(t.Context(), f.bucket.AccountID, time.Now())
			if err != nil || len(usage.Buckets) != 1 || usage.Buckets[0].BaselineBytes < 1 {
				t.Fatal("deletion refunded unreconciled capacity", usage, err)
			}
		})
	}
}

// adr: 628
func TestGCSDeletionJournalRejectsMalformedFence(t *testing.T) {
	f := newLifecycleServiceFixture(t, false)
	f.enableVersioning(t)
	b := f.bucket
	j, _, err := f.st.BeginObjectDeletion(t.Context(), state.ObjectDeletion{ObjectDeletion: api.ObjectDeletion{ID: uuid.NewString(), BucketID: b.ID, Key: "key"}, AccountID: b.AccountID, AppID: b.AppID, Token: "request", NativeGCS: true}, f.policy)
	if err != nil {
		t.Fatal(err)
	}
	for _, baseline := range [][]string{nil, {strings.Repeat("a", 64)}, {strings.Repeat("0", 48) + "8000000000000000"}, {strings.Repeat("0", 64), strings.Repeat("0", 63) + "1"}} {
		if _, err = f.st.DispatchObjectDeletion(t.Context(), j.ID, j.Token, j.ProviderStatus, baseline); !errors.Is(err, state.ErrConflict) {
			t.Fatal("invalid native fence accepted", baseline, err)
		}
	}
	if _, err = f.st.DispatchObjectDeletion(t.Context(), j.ID, j.Token, j.ProviderStatus, []string{strings.Repeat("0", 63) + "1"}); err != nil {
		t.Fatal(err)
	}
}

// adr: 628
func TestGCSMissingCurrentDeletionIsNoOp(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer server.Close()
	p := testGCS(server.URL, &fakeGCSStore{})
	if err := p.deleteCapturedCurrent(t.Context(), "physical", "key", []string{strings.Repeat("0", 64)}); err != nil || calls != 0 {
		t.Fatal(err, calls)
	}
}

// adr: 628
func TestGCSDeletionSQLFenceConstraints(t *testing.T) {
	f := newLifecycleServiceFixture(t, true)
	f.enableVersioning(t)
	b := f.bucket
	j, _, err := f.st.BeginObjectDeletion(t.Context(), state.ObjectDeletion{ObjectDeletion: api.ObjectDeletion{ID: uuid.NewString(), BucketID: b.ID, Key: "key"}, AccountID: b.AccountID, AppID: b.AppID, Token: "request", NativeGCS: true}, f.policy)
	if err != nil {
		t.Fatal(err)
	}
	for name, query := range map[string]string{
		"noninteger generation":    `UPDATE object_deletions SET baseline='["ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"]' WHERE id=$1`,
		"missing dispatched fence": `UPDATE object_deletions SET state='dispatched' WHERE id=$1`,
		"S3 marker on GCS":         `UPDATE object_deletions SET delete_marker=true WHERE id=$1`,
	} {
		if _, err := f.pool.Exec(t.Context(), query, j.ID); err == nil {
			t.Fatalf("%s passed native SQL constraints", name)
		}
	}
	j, err = f.st.DispatchObjectDeletion(t.Context(), j.ID, j.Token, j.ProviderStatus, []string{strings.Repeat("0", 63) + "1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE object_deletions SET baseline=$2::jsonb WHERE id=$1`, j.ID, `["`+strings.Repeat("0", 63)+`2"]`); err == nil {
		t.Fatal("dispatched generation fence changed")
	}
}
