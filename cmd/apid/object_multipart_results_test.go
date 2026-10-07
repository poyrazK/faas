//go:build !no_pg

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type multipartResultAPIProvider struct {
	*fakeObjectProvider
	calls  int
	result objectstorage.MultipartCompletionResult
}

func (p *multipartResultAPIProvider) CompleteMultipartWithResult(ctx context.Context, _ string, r objectstorage.MultipartCompleteRequest, _ objectstorage.ObjectWriteConditions) (objectstorage.MultipartCompletionResult, error) {
	if err := r.BeforeRequest(ctx); err != nil {
		return objectstorage.MultipartCompletionResult{}, err
	}
	p.calls++
	return p.result, nil
}

// adr: 544
func TestMultipartResultsControlClientMem(t *testing.T) {
	e := setup(t, api.PlanPro)
	multipartResultsControlClient(t, e.h, e.s, e.store, e.key, e.acct)
}
func TestMultipartResultsControlClientPG(t *testing.T) {
	e := setupPGHandler(t, api.PlanPro)
	multipartResultsControlClient(t, e.h, e.s, e.store, e.key, e.acct)
}

func multipartResultsControlClient(t *testing.T, h http.Handler, s *server, st state.Store, key string, acct state.Account) {
	t.Helper()
	ctx := t.Context()
	if err := s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	app, err := st.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "multipart-result", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	p := &multipartResultAPIProvider{fakeObjectProvider: &fakeObjectProvider{}, result: objectstorage.MultipartCompletionResult{UploadResult: objectstorage.UploadResult{ETag: `"actual"`, ProviderVersionID: "private-result"}}}
	s.WithObjectStorage(objectRegistry(t, p, &fakeObjectProvider{}, "external"))
	local := httptest.NewServer(h)
	defer local.Close()
	c := api.NewClient(local.URL, key)
	c.SetCompletionCache(nil)
	b, err := c.CreateObjectBucket(ctx, "multipart-result", api.CreateObjectBucketRequest{Name: "assets"})
	if err != nil {
		t.Fatal(err)
	}
	accounting := st.(state.ObjectStorageAccountingStore)
	if err = accounting.ClaimObjectInventory(ctx, b.ID, "baseline"); err != nil {
		t.Fatal(err)
	}
	if err = accounting.FinishObjectInventory(ctx, b.ID, "baseline", 0, 0); err != nil {
		t.Fatal(err)
	}
	owned, err := st.(state.ObjectBucketStore).GetObjectBucket(ctx, acct.ID, app.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = accounting.RecordObjectUsageReport(ctx, api.ObjectStorageUsageReport{AccountID: acct.ID, BackendID: owned.BackendID, BackendFingerprint: owned.BackendFingerprint, Source: "fixture", PeriodStart: state.ObjectStoragePeriod(time.Now()), ObservedAt: time.Now().Add(-time.Second)}); err != nil {
		t.Fatal(err)
	}
	u, err := c.CreateObjectMultipartUpload(ctx, "multipart-result", b.ID, api.CreateObjectMultipartUploadRequest{Key: "result", SizeBytes: 10})
	if err != nil {
		t.Fatal(err)
	}
	fences := st.(state.ObjectBucketWriteFenceStore)
	if _, err := fences.BeginObjectBucketMutation(ctx, owned, state.ObjectBucketMutationRequest); err != nil {
		t.Fatal(err)
	}
	hold, err := fences.AcquireObjectBucketWriteFence(ctx, owned, uuid.NewString())
	if err != nil || hold.Requests != 2 || hold.Multipart != 1 {
		t.Fatal("session lost original receipt", hold, err)
	}
	request := api.CompleteObjectMultipartUploadRequest{Parts: []api.ObjectMultipartCompletedPart{{PartNumber: 1, ETag: `"part"`}}}
	completed, err := c.CompleteObjectMultipartUpload(ctx, "multipart-result", b.ID, u.ID, request)
	if err != nil || completed.State != state.ObjectMultipartCompleted || completed.ETag != `"actual"` || !state.ValidObjectVersionID(completed.VersionID) || p.calls != 1 {
		t.Fatal(completed, err, p.calls)
	}
	drained, err := fences.ReadObjectBucketWriteFence(ctx, owned, hold.Token)
	if err != nil || drained.Requests != 1 || drained.Multipart != 0 {
		t.Fatal("completion erased unrelated writer or retained original", drained, err)
	}
	public := completed.VersionID
	p.result.ETag, p.result.ProviderVersionID = `"overwritten"`, "private-later"
	for _, read := range []func() (api.ObjectMultipartUpload, error){
		func() (api.ObjectMultipartUpload, error) {
			return c.CompleteObjectMultipartUpload(ctx, "multipart-result", b.ID, u.ID, request)
		},
		func() (api.ObjectMultipartUpload, error) {
			return c.GetObjectMultipartUpload(ctx, "multipart-result", b.ID, u.ID)
		},
	} {
		got, e := read()
		if e != nil || got.ETag != `"actual"` || got.VersionID != public || p.calls != 1 {
			t.Fatal("client lost durable completion result", got, e, p.calls)
		}
		raw, e := json.Marshal(got)
		if e != nil || strings.Contains(string(raw), "private-") {
			t.Fatal("private result leaked", string(raw), e)
		}
	}
	list, err := c.ListObjectMultipartUploads(ctx, "multipart-result", b.ID, 10, "")
	if err != nil || len(list.Items) != 1 || list.Items[0].ETag != `"actual"` || list.Items[0].VersionID != public {
		t.Fatal(list, err)
	}
	request.Parts[0].ETag = `"changed"`
	if _, err = c.CompleteObjectMultipartUpload(ctx, "multipart-result", b.ID, u.ID, request); err == nil || p.calls != 1 {
		t.Fatal("changed completion replay accepted", err, p.calls)
	}
}
