package objectstorage

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 592
func TestProtectedUploadRouteNativeReadback(t *testing.T) {
	protectedUploadRouteNativeReadback(t, false)
}

// adr: 595
func TestProtectedEventUploadRouteNativeReadback(t *testing.T) {
	protectedUploadRouteNativeReadback(t, true)
}
func protectedUploadRouteNativeReadback(t *testing.T, event bool) {
	f, _ := newTrackedUploadFixture(t)
	b, err := f.store.GetObjectBucket(t.Context(), f.account.ID, f.app.ID, f.route.BucketID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-17 * time.Minute)
	f.store.SetClockForTest(func() time.Time { return now })
	days := int32(3)
	cfg := api.ObjectBucketObjectLockConfiguration{Enabled: true, DefaultRetention: &api.ObjectLockDefaultRetention{Mode: "COMPLIANCE", Days: &days}}
	if event {
		years := int32(1)
		cfg.DefaultRetention.DefaultEventHold = &api.ObjectRetentionPeriod{Years: &years}
	}
	if _, err = f.store.RequestObjectBucketObjectLock(t.Context(), b.AccountID, b.AppID, b.ID, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ObserveObjectBucketVersioning(t.Context(), b.AccountID, b.AppID, b.ID, "Enabled"); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"propagate", "inventory", "verify"} {
		j, e := f.store.ClaimObjectBucketVersioning(t.Context(), b.ID, token)
		if e != nil {
			t.Fatal(e)
		}
		j, e = f.store.AdvanceObjectBucketVersioning(t.Context(), b.ID, j.Token, "Enabled")
		if e != nil {
			t.Fatal(e)
		}
		if token == "inventory" {
			c, e := f.store.ClaimObjectCapacityReconciliation(t.Context(), j.CapacityJobID, "scan")
			if e != nil {
				t.Fatal(e)
			}
			if _, e = f.store.StageObjectVersionInventoryPage(t.Context(), c.ID, c.Token, "", nil); e != nil {
				t.Fatal(e)
			}
		}
		now = now.Add(16 * time.Minute)
	}
	j, err := f.store.ClaimObjectBucketObjectLock(t.Context(), b.ID, "lock")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.FinishObjectBucketObjectLock(t.Context(), b.ID, j.Token, cfg); err != nil {
		t.Fatal(err)
	}
	now = time.Now().UTC()
	var stored http.Header
	puts, heads := 0, 0
	native := protectionTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			puts++
			stored = r.Header.Clone()
			_, _ = io.Copy(io.Discard, r.Body)
			if r.Header.Get("X-Amz-Trailer") == "" {
				t.Error("protected streaming upload omitted checksum trailer")
			}
			w.Header().Set("ETag", `"route"`)
			w.Header().Set("X-Amz-Version-Id", "native-route")
			return
		}
		heads++
		if r.URL.Query().Get("versionId") != "native-route" {
			t.Error("readback used current selector")
		}
		w.Header().Set("Content-Length", "6")
		w.Header().Set("ETag", `"route"`)
		w.Header().Set("X-Amz-Version-Id", "native-route")
		w.Header().Set("X-Amz-Object-Lock-Mode", "COMPLIANCE")
		w.Header().Set("X-Amz-Object-Lock-Retain-Until-Date", time.Now().UTC().AddDate(0, 0, 3).Format(time.RFC3339Nano))
		if event {
			w.Header().Set("X-Amz-Object-Lock-Event-Hold", "ON")
			w.Header().Set("X-Amz-Object-Lock-Event-Hold-Duration-Years", "1")
			w.Header().Set("X-Amz-Object-Lock-Retain-Until-Date", time.Now().UTC().AddDate(0, 0, 365).Format(time.RFC3339Nano))
		}
		for name, values := range stored {
			if strings.HasPrefix(strings.ToLower(name), "x-amz-meta-") {
				w.Header()[name] = values
			}
		}
	}))
	backend := f.registry.backends["test"]
	backend.Provider = native.(Provider)
	backend.ObjectLock.Enabled = true
	backend.ObjectLock.EventHolds = event
	f.registry.backends["test"] = backend
	f.handler, err = NewUploadHandler(UploadConfig{Store: f.store, Routes: f.store, Buckets: f.store, Authenticator: f.store, Registry: f.registry, Accounting: f.store, RequestMetrics: f.store, AppsDomain: "apps.test", Next: http.NotFoundHandler()})
	if err != nil {
		t.Fatal(err)
	}
	res := f.requestWithIdempotency(http.MethodPost, "/uploads/avatar", "avatar", "image/png", "protected")
	if res.Code != 201 || strings.Contains(res.Body.String(), "native-route") || strings.Contains(res.Body.String(), "gregale-protection") {
		t.Fatal(res.Code, res.Body.String())
	}
	id := res.Header().Get("X-Gregale-Upload-ID")
	c, err := f.store.GetObjectUploadReceipt(t.Context(), b.AccountID, b.AppID, f.route.ID, f.key.ID, id)
	if err != nil || c.Status != "completed" || !c.Protection.Enabled {
		t.Fatal(c, err)
	}
	if event {
		backend.ObjectLock.EventHolds = false
	} else {
		backend.ObjectLock.Enabled = false
	}
	f.registry.backends["test"] = backend
	if replay := f.requestWithIdempotency(http.MethodPost, "/uploads/avatar", "avatar", "image/png", "protected"); replay.Code != 201 {
		t.Fatal(replay.Code, replay.Body.String())
	}
	if fresh := f.requestWithIdempotency(http.MethodPost, "/uploads/avatar", "avatar", "image/png", "new"); fresh.Code != http.StatusNotImplemented {
		t.Fatal("disabled enrollment admitted write", fresh.Code)
	}
	if puts != 1 || heads != 1 {
		t.Fatal("replay repeated write", puts, heads)
	}
	metrics, err := f.store.ListObjectStorageProviderRequestMetrics(t.Context(), backend.ID, backend.Fingerprint, state.ObjectStoragePeriod(time.Now()))
	if err != nil || len(metrics) != 1 || metrics[0].RequestCount != 2 {
		t.Fatal("protection read was not metered", metrics, err)
	}
}
