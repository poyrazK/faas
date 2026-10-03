package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type blockedMultipartSigner struct {
	objectstorage.Provider
	entered chan struct{}
	release chan struct{}
}

func (p blockedMultipartSigner) PresignMultipartPart(ctx context.Context, bucket string, req objectstorage.MultipartPartRequest) (objectstorage.SignedRequest, error) {
	close(p.entered)
	select {
	case <-p.release:
		return p.Provider.PresignMultipartPart(ctx, bucket, req)
	case <-ctx.Done():
		return objectstorage.SignedRequest{}, ctx.Err()
	}
}

// adr: 408
func TestObjectMultipartSignedURLWithheldAfterMutation(t *testing.T) {
	for _, operation := range []string{state.ObjectMultipartAborting, state.ObjectMultipartCompleting} {
		t.Run(operation, func(t *testing.T) {
			e := setup(t, api.PlanHobby)
			if err := e.s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("true")); err != nil {
				t.Fatal(err)
			}
			createApp(t, e, "sign-race")
			p := blockedMultipartSigner{Provider: &fakeObjectProvider{}, entered: make(chan struct{}), release: make(chan struct{})}
			var release sync.Once
			defer release.Do(func() { close(p.release) })
			e.s.WithObjectStorage(objectRegistry(t, p, &fakeObjectProvider{}, "external"))
			b := bucketResponse(t, e.do(t, "POST", "/v1/apps/sign-race/buckets", map[string]any{"name": "assets"}, nil), 201)
			qualifyObjectAccounting(t, e, b.ID)
			base := "/v1/apps/sign-race/buckets/" + b.ID + "/multipart-uploads"
			response := e.do(t, "POST", base, api.CreateObjectMultipartUploadRequest{Key: "race", SizeBytes: 10}, nil)
			var u api.ObjectMultipartUpload
			if response.Code != 201 || json.Unmarshal(response.Body.Bytes(), &u) != nil {
				t.Fatal(response.Code, response.Body.String())
			}
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				done <- e.do(t, "POST", base+"/"+u.ID+"/parts/1/signed-url", api.ObjectMultipartPartSignRequest{ExpiresIn: 60}, nil)
			}()
			select {
			case <-p.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("signing did not reach the provider")
			}
			app, err := e.store.AppBySlug(t.Context(), "sign-race")
			if err != nil {
				t.Fatal(err)
			}
			_, err = e.store.ClaimObjectMultipartUpload(t.Context(), e.acct.ID, app.ID, b.ID, u.ID, "mutation", operation, []api.ObjectMultipartCompletedPart{{PartNumber: 1, ETag: `"part"`}}, false)
			release.Do(func() { close(p.release) })
			if err != nil {
				t.Fatal(err)
			}
			select {
			case result := <-done:
				if result.Code != http.StatusConflict || strings.Contains(result.Body.String(), "upstream.test") {
					t.Fatal("signed URL escaped after cutoff", result.Code, result.Body.String())
				}
			case <-time.After(5 * time.Second):
				t.Fatal("signing did not return")
			}
		})
	}
}

// adr: 408
func TestObjectMultipartSignedURLAbortEndToEndPG(t *testing.T) {
	for _, lostACK := range []bool{false, true} {
		name := "acknowledged"
		if lostACK {
			name = "lost-acknowledgment"
		}
		t.Run(name, func(t *testing.T) { objectMultipartSignedURLAbortEndToEnd(t, lostACK) })
	}
}

func objectMultipartSignedURLAbortEndToEnd(t *testing.T, lostACK bool) {
	t.Helper()
	const key = "目录 /+%.bin"
	const native = "private-upload/+%?"
	initialAborts := int32(1)
	if lostACK {
		// The immutable abort adapter permits two SDK attempts. Lose both
		// responses so the durable journal, rather than an SDK retry, recovers.
		initialAborts = 2
	}
	var aborts, lists atomic.Int32
	var gone atomic.Bool
	f := newGatewayRecoveryFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Method == http.MethodGet && q.Has("uploads") && q.Get("prefix") == key {
			_, _ = io.WriteString(w, `<ListMultipartUploadsResult><Bucket>physical</Bucket><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>`)
			return
		}
		if r.URL.Path != "/physical/"+key {
			t.Error("wrong provider key", r.URL)
			w.WriteHeader(500)
			return
		}
		if r.Method == http.MethodPost && q.Has("uploads") && r.Header.Get("X-Amz-Meta-"+objectstorage.ReservedMultipartSessionMetadataKey) != "" {
			_, _ = io.WriteString(w, `<InitiateMultipartUploadResult><Bucket>physical</Bucket><UploadId>`+native+`</UploadId></InitiateMultipartUploadResult>`)
			return
		}
		if q.Get("uploadId") != native {
			t.Error("wrong provider upload identity", r.URL)
			w.WriteHeader(500)
			return
		}
		if r.Method == http.MethodPut && q.Get("partNumber") == "1" && q.Get("X-Amz-Signature") != "" {
			body, err := io.ReadAll(r.Body)
			if err != nil || string(body) != "0123456789" {
				t.Error("wrong signed part body", string(body), err)
				w.WriteHeader(500)
				return
			}
			w.Header().Set("ETag", `"part"`)
			return
		}
		if r.Header.Get("Authorization") == "" {
			t.Error("unsigned provider cleanup", r.URL)
			w.WriteHeader(500)
			return
		}
		switch r.Method {
		case http.MethodDelete:
			n := aborts.Add(1)
			if n <= initialAborts && lostACK {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = conn.Close()
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet:
			lists.Add(1)
			if q.Get("max-parts") != "1" {
				t.Error("unbounded cleanup verification", r.URL)
			}
			if gone.Load() {
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `<Error><Code>NoSuchUpload</Code></Error>`)
				return
			}
			_, _ = io.WriteString(w, `<ListPartsResult><IsTruncated>false</IsTruncated><Part><PartNumber>1</PartNumber><ETag>"part"</ETag><Size>10</Size></Part></ListPartsResult>`)
		default:
			t.Error("unexpected provider request", r.Method, r.URL)
			w.WriteHeader(500)
		}
	}), 7)
	ctx := t.Context()
	token, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.st.CreateAPIKey(ctx, f.account.ID, hash, "multipart", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	management := func() (*server, *httptest.Server, *api.Client) {
		s := newServer(state.NewPgStore(f.pool), slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).WithObjectStorage(f.registry)
		if e := s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("true")); e != nil {
			t.Fatal(e)
		}
		srv := httptest.NewServer(s.handler())
		t.Cleanup(srv.Close)
		return s, srv, api.NewClient(srv.URL, token)
	}
	_, srv, client := management()
	u, err := client.CreateObjectMultipartUpload(ctx, f.app.Slug, f.bucket.ID, api.CreateObjectMultipartUploadRequest{Key: key, SizeBytes: 10})
	if err != nil || u.State != state.ObjectMultipartActive {
		t.Fatal("create", u, err)
	}
	signed, err := client.SignObjectMultipartPart(ctx, f.app.Slug, f.bucket.ID, u.ID, 1, api.ObjectMultipartPartSignRequest{ExpiresIn: 60})
	if err != nil {
		t.Fatal("sign", err)
	}
	req, err := http.NewRequestWithContext(ctx, signed.Method, signed.URL, strings.NewReader("0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range signed.Headers {
		req.Header.Set(k, v)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal("execute signed capability", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatal("signed part", response.StatusCode)
	}
	if err = client.AbortObjectMultipartUpload(ctx, f.app.Slug, f.bucket.ID, u.ID); err == nil || aborts.Load() != initialAborts || lists.Load() != 0 {
		t.Fatal("abort refunded a live signed URL", err, aborts.Load(), lists.Load())
	}
	srv.Close()
	recovery, _, client := management()
	stored, err := state.NewPgStore(f.pool).GetObjectMultipartUpload(ctx, f.account.ID, f.app.ID, f.bucket.ID, u.ID)
	if err != nil || stored.State != state.ObjectMultipartAborting || stored.ProviderUploadID != native || !stored.PartURLUnsafeUntil.After(signed.ExpiresAt) || stored.LeaseToken != "" {
		t.Fatal("restart lost cleanup authority", stored, err)
	}
	if lostACK && stored.LastErrorCode != "temporary" {
		t.Fatal("lost acknowledgment did not retain an uncertain abort", stored)
	}
	assertReservation := func() {
		t.Helper()
		usage, e := state.NewPgStore(f.pool).ObjectUsage(ctx, f.account.ID, time.Now())
		if e != nil || len(usage.Buckets) != 1 || usage.Buckets[0].MultipartBytes != 0 || usage.Buckets[0].GrantedBytes != 10 || usage.Buckets[0].BaselineBytes != 7 {
			t.Fatal("cleanup accounting", usage, e)
		}
	}
	assertReservation()
	if _, err = client.CreateObjectCapacityReconciliation(ctx, f.app.Slug, f.bucket.ID); err == nil {
		t.Fatal("live signed upload allowed capacity reconciliation")
	}
	// Simulate time passing in this isolated database; the migration test
	// separately proves that application callers cannot shorten this fence.
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err = tx.Exec(ctx, `ALTER TABLE object_storage_multipart_uploads DISABLE TRIGGER object_multipart_part_url_deadline_protected`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE object_storage_multipart_uploads SET created_at=created_at-interval '1 hour',part_url_unsafe_until=clock_timestamp()-interval '1 second',retry_at=now() WHERE id=$1`, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `ALTER TABLE object_storage_multipart_uploads ENABLE TRIGGER object_multipart_part_url_deadline_protected`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = recovery.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("false")); err != nil {
		t.Fatal(err)
	}
	if err = recovery.reconcileObjectMultipartUploads(ctx, nil); err != nil || aborts.Load() != initialAborts+1 || lists.Load() != 1 {
		t.Fatal("restart cleanup of remaining parts", err, aborts.Load(), lists.Load())
	}
	assertReservation()
	gone.Store(true)
	if _, err = f.pool.Exec(ctx, `UPDATE object_storage_multipart_uploads SET retry_at=now() WHERE id=$1`, u.ID); err != nil {
		t.Fatal(err)
	}
	if err = recovery.reconcileObjectMultipartUploads(ctx, nil); err != nil || aborts.Load() != initialAborts+2 || lists.Load() != 2 {
		t.Fatal("verified cleanup", err, aborts.Load(), lists.Load())
	}
	// Legacy fixed-size uploads keep their untracked key grant even after
	// verified abort; cleanup cannot rewrite the provider inventory baseline.
	assertReservation()
	if err = recovery.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	if err = client.AbortObjectMultipartUpload(ctx, f.app.Slug, f.bucket.ID, u.ID); err != nil || aborts.Load() != initialAborts+2 || lists.Load() != 2 {
		t.Fatal("terminal abort replay dispatched again", err, aborts.Load(), lists.Load())
	}
	if _, err = client.CreateObjectCapacityReconciliation(ctx, f.app.Slug, f.bucket.ID); err != nil {
		t.Fatal("verified abort retained its live-session fence", err)
	}
}
