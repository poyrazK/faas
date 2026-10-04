package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 537
func TestObjectWriteReceiptAPI(t *testing.T) {
	e := setup(t, api.PlanHobby)
	provider := &fakeObjectProvider{}
	e.s.WithObjectStorage(objectRegistry(t, provider, &fakeObjectProvider{}, "external"))
	setS3Flag(t, e, true)
	createApp(t, e, "receipts")
	b := bucketResponse(t, e.do(t, "POST", "/v1/apps/receipts/buckets", map[string]any{"name": "assets"}, nil), 201)
	app, err := e.store.AppBySlug(t.Context(), "receipts")
	if err != nil {
		t.Fatal(err)
	}
	bucket, err := e.store.GetObjectBucket(t.Context(), e.acct.ID, app.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.store.ClaimObjectInventory(t.Context(), b.ID, "initial"); err != nil {
		t.Fatal(err)
	}
	if err = e.store.FinishObjectInventory(t.Context(), b.ID, "initial", 0, 0); err != nil {
		t.Fatal(err)
	}
	report := api.ObjectStorageUsageReport{AccountID: e.acct.ID, BackendID: bucket.BackendID, BackendFingerprint: bucket.BackendFingerprint, Source: "provider", PeriodStart: state.ObjectStoragePeriod(time.Now()), ObservedAt: time.Now()}
	if err = e.store.RecordObjectUsageReport(t.Context(), report); err != nil {
		t.Fatal(err)
	}
	c, err := e.store.BeginTrackedGatewayUpload(t.Context(), state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: e.acct.ID, AppID: app.ID, BucketID: b.ID, SubjectID: uuid.NewString(), Key: "key", Bytes: 4, Status: "pending"}, e.s.objectStorage.Accounting)
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/apps/receipts/buckets/" + b.ID + "/write-receipts"
	before, err := e.store.ObjectUsage(t.Context(), e.acct.ID, report.PeriodStart)
	if err != nil {
		t.Fatal(err)
	}
	providerCalls := len(provider.accessed)
	setS3Flag(t, e, false)
	e.s.objectStorage.Accounting.MaxMonthlyAuthorizations = 1
	for _, endpoint := range []string{path, path + "/" + c.ID} {
		r := e.do(t, "GET", endpoint, nil, nil)
		if r.Code != 200 || r.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(r.Code, r.Body.String())
		}
		if endpoint != path && r.Header().Get("Retry-After") != "30" {
			t.Fatal(r.Header())
		}
		for _, private := range []string{"subject_id", "account_id", "source_key", "recovery", "backend", "request_fingerprint"} {
			if strings.Contains(r.Body.String(), private) {
				t.Fatal("private API projection", r.Body.String())
			}
		}
	}
	for _, tc := range []struct {
		scope, permission string
		code              int
	}{{api.ScopeStorageRead, "read", 403}, {api.ScopeStorageWrite, "", 403}, {api.ScopeStorageWrite, "read", 403}, {api.ScopeStorageWrite, "write", 200}} {
		plaintext, hash, keyErr := api.GenerateAPIKey()
		if keyErr != nil {
			t.Fatal(keyErr)
		}
		scopes := []string{tc.scope}
		if tc.scope == api.ScopeStorageWrite && tc.permission == "read" {
			scopes = append(scopes, api.ScopeStorageRead)
		}
		key, keyErr := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, uuid.NewString(), scopes)
		if keyErr != nil {
			t.Fatal(keyErr)
		}
		if tc.permission != "" {
			if _, keyErr = e.store.SetObjectBucketAccessGrant(t.Context(), e.acct.ID, b.ID, key.ID, tc.permission); keyErr != nil {
				t.Fatal(keyErr)
			}
		}
		for _, endpoint := range []string{path, path + "/" + c.ID} {
			r := e.do(t, "GET", endpoint, nil, map[string]string{"Authorization": "Bearer " + plaintext})
			if r.Code != tc.code {
				t.Fatal(tc, r.Code, r.Body.String())
			}
		}
		if tc.code == 200 {
			if keyErr = e.store.DeleteObjectBucketAccessGrant(t.Context(), e.acct.ID, b.ID, key.ID); keyErr != nil {
				t.Fatal(keyErr)
			}
			r := e.do(t, "GET", path+"/"+c.ID, nil, map[string]string{"Authorization": "Bearer " + plaintext})
			if r.Code != 403 {
				t.Fatal("removed grant still allowed", r.Code)
			}
		}
	}
	for _, query := range []string{"?limit=0", "?limit=-1", "?limit=101", "?status=invalid", "?limit=2&limit=3", "?cursor=invalid", "?unexpected=1"} {
		if r := e.do(t, "GET", path+query, nil, nil); r.Code != 400 {
			t.Fatal(query, r.Code, r.Body.String())
		}
	}
	for _, id := range []string{"invalid", uuid.NewString()} {
		if r := e.do(t, "GET", path+"/"+id, nil, nil); r.Code != 404 {
			t.Fatal(id, r.Code)
		}
	}
	r := e.do(t, "GET", path, nil, nil)
	var page api.ObjectWriteReceiptList
	if err = json.Unmarshal(r.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Items[0].ID != c.ID {
		t.Fatal(r.Body.String(), err)
	}
	after, err := e.store.ObjectUsage(t.Context(), e.acct.ID, report.PeriodStart)
	if err != nil || !reflect.DeepEqual(before, after) || len(provider.accessed) != providerCalls {
		t.Fatal("receipt API admitted or contacted provider", err)
	}
}
