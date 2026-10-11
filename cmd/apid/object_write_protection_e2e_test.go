//go:build !no_pg

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

type writeProtectionControlNative struct {
	mu                         sync.Mutex
	objects                    map[string]http.Header
	event                      bool
	initiated                  http.Header
	puts, creates, completions int
}

func (f *writeProtectionControlNative) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := strings.TrimPrefix(r.URL.Path, "/physical/")
	q := r.URL.Query()
	w.Header().Set("Content-Type", "application/xml")
	switch {
	case q.Has("uploads") && r.Method == http.MethodGet:
		_, _ = io.WriteString(w, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>`)
	case q.Has("uploads") && r.Method == http.MethodPost:
		f.creates++
		f.initiated = r.Header.Clone()
		_, _ = io.WriteString(w, `<InitiateMultipartUploadResult><UploadId>private-upload</UploadId></InitiateMultipartUploadResult>`)
	case q.Has("uploadId") && r.Method == http.MethodPut:
		if r.Header.Get("Content-Md5") == "" {
			t.Error("protected part omitted signed checksum")
		}
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("ETag", `"part"`)
	case q.Has("uploadId") && r.Method == http.MethodGet:
		_, _ = io.WriteString(w, `<ListPartsResult><IsTruncated>false</IsTruncated><Part><PartNumber>1</PartNumber><ETag>&quot;part&quot;</ETag><Size>3</Size></Part></ListPartsResult>`)
	case q.Has("uploadId") && r.Method == http.MethodPost:
		f.completions++
		if f.objects[key] != nil {
			w.WriteHeader(404)
			_, _ = io.WriteString(w, `<Error><Code>NoSuchUpload</Code></Error>`)
			return
		}
		f.commit(key, f.initiated)
		// Commit, but lose the completion acknowledgment.
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close()
	case r.Method == http.MethodPut:
		f.puts++
		if r.Header.Get("Content-Md5") == "" {
			t.Error("unsigned native checksum")
		}
		_, _ = io.Copy(io.Discard, r.Body)
		f.commit(key, r.Header.Clone())
		if key != "lost" {
			w.Header().Set("ETag", `"stored"`)
			w.Header().Set("X-Amz-Version-Id", "private-"+key)
		}
	case r.Method == http.MethodHead:
		h := f.objects[key]
		if h == nil {
			w.WriteHeader(404)
			return
		}
		if v := q.Get("versionId"); v != "" && v != "private-"+key {
			t.Error("wrong exact selector", r.URL)
		}
		for name, values := range h {
			if strings.HasPrefix(strings.ToLower(name), "x-amz-meta-") || strings.HasPrefix(strings.ToLower(name), "x-amz-object-lock-") {
				w.Header()[name] = values
			}
		}
		w.Header().Set("Content-Length", strconv.Itoa(3))
		w.Header().Set("ETag", `"stored"`)
		w.Header().Set("X-Amz-Version-Id", "private-"+key)
	default:
		t.Error("unexpected native request", r.Method, r.URL)
		w.WriteHeader(500)
	}
}
func (f *writeProtectionControlNative) commit(key string, h http.Header) {
	if h.Get("X-Amz-Object-Lock-Mode") == "" {
		h.Set("X-Amz-Object-Lock-Mode", "COMPLIANCE")
		h.Set("X-Amz-Object-Lock-Retain-Until-Date", time.Now().UTC().AddDate(0, 0, 3).Format(time.RFC3339Nano))
		if f.event {
			h.Set("X-Amz-Object-Lock-Event-Hold", "ON")
			h.Set("X-Amz-Object-Lock-Event-Hold-Duration-Years", "1")
		}
	}
	if h.Get("X-Amz-Object-Lock-Event-Hold") == "ON" {
		days, _ := strconv.Atoi(h.Get("X-Amz-Object-Lock-Event-Hold-Duration-Days"))
		if years, _ := strconv.Atoi(h.Get("X-Amz-Object-Lock-Event-Hold-Duration-Years")); years != 0 {
			days = years * 365
		}
		minimum := time.Now().UTC().AddDate(0, 0, days)
		existing, e := time.Parse(time.RFC3339Nano, h.Get("X-Amz-Object-Lock-Retain-Until-Date"))
		if e != nil || minimum.After(existing) {
			h.Set("X-Amz-Object-Lock-Retain-Until-Date", minimum.Format(time.RFC3339Nano))
		}
	}
	f.objects[key] = h
}

// adr: 619
func TestWriteProtectionControlE2EMem(t *testing.T) {
	e := setup(t, api.PlanPro)
	now := time.Now().UTC().Add(-17 * time.Minute)
	e.store.SetClockForTest(func() time.Time { return now })
	writeProtectionControlE2E(t, e.s, e.store, e.acct, e.key, nil, func(reset bool) {
		if reset {
			now = time.Now().UTC()
			// Signed URL admission must follow its wall-clock expiry source.
			e.store.SetClockForTest(nil)
		} else {
			now = now.Add(16 * time.Minute)
			e.store.SetClockForTest(func() time.Time { return now })
		}
	})
}
func TestWriteProtectionControlE2EPG(t *testing.T) {
	e := setupPGHandler(t, api.PlanPro)
	writeProtectionControlE2E(t, e.s, e.store, e.acct, e.key, e.pool, func(reset bool) {
		if reset {
			return
		}
		for _, q := range []string{`UPDATE object_bucket_versioning SET retry_at=clock_timestamp(),propagation_until=CASE WHEN state IN ('waiting','propagating') THEN clock_timestamp()-interval '1 second' ELSE propagation_until END`, `UPDATE object_upload_completions SET recovery_retry_at=clock_timestamp() WHERE status='pending'`, `UPDATE object_storage_multipart_uploads SET retry_at=clock_timestamp(),lease_until=CASE WHEN lease_token IS NULL THEN NULL ELSE clock_timestamp()-interval '1 second' END WHERE state='completing'`} {
			if _, err := e.pool.Exec(t.Context(), q); err != nil {
				t.Fatal(err)
			}
		}
	})
}

// adr: 622
func TestEventWriteProtectionControlE2EMem(t *testing.T) {
	e := setup(t, api.PlanPro)
	now := time.Now().UTC().Add(-17 * time.Minute)
	e.store.SetClockForTest(func() time.Time { return now })
	writeProtectionControlE2E(t, e.s, e.store, e.acct, e.key, nil, func(reset bool) {
		if reset {
			now = time.Now().UTC()
			// Signed URL admission must follow its wall-clock expiry source.
			e.store.SetClockForTest(nil)
		} else {
			now = now.Add(16 * time.Minute)
			e.store.SetClockForTest(func() time.Time { return now })
		}
	}, true)
}
func TestEventWriteProtectionControlE2EPG(t *testing.T) {
	e := setupPGHandler(t, api.PlanPro)
	writeProtectionControlE2E(t, e.s, e.store, e.acct, e.key, e.pool, func(reset bool) {
		if reset {
			return
		}
		for _, q := range []string{`UPDATE object_bucket_versioning SET retry_at=clock_timestamp(),propagation_until=CASE WHEN state IN ('waiting','propagating') THEN clock_timestamp()-interval '1 second' ELSE propagation_until END`, `UPDATE object_upload_completions SET recovery_retry_at=clock_timestamp() WHERE status='pending'`, `UPDATE object_storage_multipart_uploads SET retry_at=clock_timestamp(),lease_until=CASE WHEN lease_token IS NULL THEN NULL ELSE clock_timestamp()-interval '1 second' END WHERE state='completing'`} {
			if _, err := e.pool.Exec(t.Context(), q); err != nil {
				t.Fatal(err)
			}
		}
	}, true)
}

func prepareWriteProtectionControl(t *testing.T, st state.Store, b state.ObjectBucket, advance func(bool), event ...bool) {
	t.Helper()
	days := int32(3)
	lock := st.(state.ObjectBucketObjectLockStore)
	cfg := api.ObjectBucketObjectLockConfiguration{Enabled: true, DefaultRetention: &api.ObjectLockDefaultRetention{Mode: "COMPLIANCE", Days: &days}}
	if len(event) != 0 && event[0] {
		years := int32(1)
		cfg.DefaultRetention.DefaultEventHold = &api.ObjectRetentionPeriod{Years: &years}
	}
	if _, err := lock.RequestObjectBucketObjectLock(t.Context(), b.AccountID, b.AppID, b.ID, cfg); err != nil {
		t.Fatal(err)
	}
	v := st.(state.ObjectBucketVersioningStore)
	if _, err := v.ObserveObjectBucketVersioning(t.Context(), b.AccountID, b.AppID, b.ID, "Enabled"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		j, err := v.ClaimObjectBucketVersioning(t.Context(), b.ID, fmt.Sprint("v", i))
		if err != nil {
			t.Fatal(err)
		}
		j, err = v.AdvanceObjectBucketVersioning(t.Context(), b.ID, j.Token, "Enabled")
		if err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			c, err := st.(state.ObjectCapacityStore).ClaimObjectCapacityReconciliation(t.Context(), j.CapacityJobID, "inventory")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = st.(state.ObjectVersionInventoryStore).StageObjectVersionInventoryPage(t.Context(), c.ID, c.Token, "", nil); err != nil {
				t.Fatal(err)
			}
		}
		if i < 2 {
			advance(false)
		}
	}
	j, err := lock.ClaimObjectBucketObjectLock(t.Context(), b.ID, "lock")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lock.FinishObjectBucketObjectLock(t.Context(), b.ID, j.Token, cfg); err != nil {
		t.Fatal(err)
	}
	advance(true)
}

func writeProtectionControlE2E(t *testing.T, s *server, st state.Store, acct state.Account, bearer string, pool *pgxpool.Pool, advance func(bool), event ...bool) {
	held := len(event) != 0 && event[0]
	identity, teardown := withTestIdentities(t)
	defer teardown()
	native := &writeProtectionControlNative{objects: map[string]http.Header{}, event: held}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { native.serve(t, w, r) }))
	defer upstream.Close()
	_, b, _, policy := seedEncryptionJournalStorage(t, s, st, acct, upstream.URL)
	s.WithObjectStorage(objectLockTestRegistry(t, upstream.URL, policy, objectstorage.ObjectLockConfig{Enabled: true, EventHolds: held}))
	prepareWriteProtectionControl(t, st, b, advance, held)
	if err := s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	var gateway atomic.Pointer[s3gateway.Handler]
	public := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { gateway.Load().ServeHTTP(w, r) }))
	defer public.Close()
	s.objectStorage.PublicEndpoint = public.URL
	gatewayConfig := s3gateway.Config{Registry: s.objectStorage, Store: st.(s3gateway.Store), RequestMetrics: st.(state.ObjectStorageProviderUsageStore), SpoolDir: t.TempDir(), OpenSecret: func(blob []byte) (string, error) {
		ns, plain, e := secretbox.OpenBytes(identity, blob)
		if e != nil || ns != s3gateway.CredentialSecretNamespace {
			return "", objectstorage.ErrConfiguration
		}
		return string(plain), nil
	}}
	h, err := s3gateway.New(gatewayConfig)
	if err != nil {
		t.Fatal(err)
	}
	gateway.Store(h)
	management := httptest.NewServer(s.handler())
	defer management.Close()
	client := api.NewClient(management.URL, bearer)
	until := time.Now().UTC().AddDate(0, 0, 7)
	p := &api.ObjectWriteProtection{Retention: &api.ObjectVersionRetention{Mode: "COMPLIANCE", RetainUntilDate: &until}, LegalHold: &api.ObjectVersionLegalHold{Status: "ON"}}
	if held {
		days := int32(30)
		p.Retention.EventHold = "ON"
		p.Retention.EventHoldDuration = &api.ObjectRetentionPeriod{Days: &days}
		p.Retention.RetainUntilDate = nil
	}
	size := int64(3)
	signed, err := client.SignBucketObject(t.Context(), "encrypted-journal", b.ID, api.ObjectSignRequest{Method: "PUT", Key: "explicit", SizeBytes: &size, ExpiresIn: 60, Protection: p})
	if err != nil || signed.Headers["X-Amz-Object-Lock-Legal-Hold"] != "ON" {
		t.Fatal(signed, err)
	}
	lost, err := client.SignBucketObject(t.Context(), "encrypted-journal", b.ID, api.ObjectSignRequest{Method: "PUT", Key: "lost", SizeBytes: &size, ExpiresIn: 60})
	if err != nil {
		t.Fatal(err)
	}
	lostSubject := signedCredentialSubject(t, st, lost.URL)
	u, err := client.CreateObjectMultipartUpload(t.Context(), "encrypted-journal", b.ID, api.CreateObjectMultipartUploadRequest{Key: "multipart", SizeBytes: 3, Protection: &api.ObjectWriteProtection{LegalHold: &api.ObjectVersionLegalHold{Status: "ON"}}})
	if err != nil {
		t.Fatal(err)
	}
	part, err := client.SignObjectMultipartPart(t.Context(), "encrypted-journal", b.ID, u.ID, 1, api.ObjectMultipartPartSignRequest{ExpiresIn: 60})
	if err != nil {
		t.Fatal(err)
	}
	send := func(out api.ObjectSignedRequest, tamper bool) int {
		r, e := http.NewRequestWithContext(t.Context(), out.Method, out.URL, strings.NewReader("abc"))
		if e != nil {
			t.Fatal(e)
		}
		for k, v := range out.Headers {
			r.Header.Set(k, v)
		}
		if tamper {
			r.Header.Set("X-Amz-Object-Lock-Legal-Hold", "OFF")
		}
		res, e := public.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		return res.StatusCode
	}
	if got := send(signed, true); got != 403 {
		t.Fatal("modified protection accepted", got)
	}
	if got := send(part, false); got != 200 {
		t.Fatal("part", got)
	}
	// Queue a default replacement after admission, then disable new enrollment.
	days := int32(1)
	lock := st.(state.ObjectBucketObjectLockStore)
	if _, err = lock.RequestObjectBucketObjectLock(t.Context(), acct.ID, b.AppID, b.ID, api.ObjectBucketObjectLockConfiguration{Enabled: true, DefaultRetention: &api.ObjectLockDefaultRetention{Mode: "COMPLIANCE", Days: &days}}); err != nil {
		t.Fatal(err)
	}
	s.WithObjectStorage(objectLockTestRegistry(t, upstream.URL, policy, objectstorage.ObjectLockConfig{}))
	s.objectStorage.PublicEndpoint = public.URL
	gatewayConfig.Registry = s.objectStorage
	h, err = s3gateway.New(gatewayConfig)
	if err != nil {
		t.Fatal(err)
	}
	gateway.Store(h)
	if got := send(signed, false); got != 200 {
		t.Fatal("accepted URL under disabled enrollment", got)
	}
	if got := send(lost, false); got != 503 {
		t.Fatal("lost PUT acknowledged", got)
	}
	if _, err = client.CompleteObjectMultipartUpload(t.Context(), "encrypted-journal", b.ID, u.ID, api.CompleteObjectMultipartUploadRequest{Parts: []api.ObjectMultipartCompletedPart{{PartNumber: 1, ETag: `"part"`}}}); err == nil {
		t.Fatal("lost completion acknowledged")
	}
	advance(false)
	if pool != nil {
		st = state.NewPgStore(pool)
	}
	fresh := newServer(st, s.log, "gregale.dev", noopNotifier{}).WithObjectStorage(s.objectStorage)
	if err = fresh.reconcileObjectUploads(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if err = fresh.reconcileObjectMultipartUploads(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	c, err := st.(state.ObjectTrackedGatewayUploadStore).GetObjectUploadReceipt(t.Context(), acct.ID, b.AppID, "", lostSubject, lost.UploadID)
	if err != nil || c.Status != "completed" || !c.Protection.Enabled || *c.Protection.DefaultRetention.Days != 3 {
		t.Fatal(c, err)
	}
	done, err := st.(state.ObjectMultipartUploadStore).GetObjectMultipartUpload(t.Context(), acct.ID, b.AppID, b.ID, u.ID)
	if err != nil || done.State != state.ObjectMultipartCompleted || done.Protection.Requested.LegalHold.Status != "ON" || *done.Protection.DefaultRetention.Days != 3 {
		t.Fatal(done, err)
	}
	native.mu.Lock()
	defer native.mu.Unlock()
	if native.puts != 2 || native.creates != 1 {
		t.Fatal("replayed native write", native.puts, native.creates)
	}
}
