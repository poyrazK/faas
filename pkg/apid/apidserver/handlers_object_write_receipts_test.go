package apidserver_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 537
func TestObjectWriteReceiptAPI(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	provider := &apidserver.FakeObjectProvider{}
	(*e.S()).WithObjectStorage(apidserver.ObjectRegistry(t, provider, &apidserver.FakeObjectProvider{}, "external"))
	apidserver.SetS3Flag(t, e, true)
	apidserver.CreateApp(t, e, "receipts")
	b := apidserver.BucketResponse(t, e.Do(t, "POST", "/v1/apps/receipts/buckets", map[string]any{"name": "assets"}, nil), 201)
	app, err := (*e.Store()).AppBySlug(t.Context(), "receipts")
	if err != nil {
		t.Fatal(err)
	}
	bucket, err := (*e.Store()).GetObjectBucket(t.Context(), (*e.Acct()).ID, app.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = (*e.Store()).ClaimObjectInventory(t.Context(), b.ID, "initial"); err != nil {
		t.Fatal(err)
	}
	if err = (*e.Store()).FinishObjectInventory(t.Context(), b.ID, "initial", 0, 0); err != nil {
		t.Fatal(err)
	}
	report := api.ObjectStorageUsageReport{AccountID: (*e.Acct()).ID, BackendID: bucket.BackendID, BackendFingerprint: bucket.BackendFingerprint, Source: "provider", PeriodStart: state.ObjectStoragePeriod(time.Now()), ObservedAt: time.Now()}
	if err = (*e.Store()).RecordObjectUsageReport(t.Context(), report); err != nil {
		t.Fatal(err)
	}
	c, err := (*e.Store()).BeginTrackedGatewayUpload(t.Context(), state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: (*e.Acct()).ID, AppID: app.ID, BucketID: b.ID, SubjectID: uuid.NewString(), Key: "key", Bytes: 4, Status: "pending"}, (*(*e.S()).ObjectStorage()).Accounting)
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/apps/receipts/buckets/" + b.ID + "/write-receipts"
	before, err := (*e.Store()).ObjectUsage(t.Context(), (*e.Acct()).ID, report.PeriodStart)
	if err != nil {
		t.Fatal(err)
	}
	providerCalls := len((*provider.Accessed()))
	apidserver.SetS3Flag(t, e, false)
	(*(*e.S()).ObjectStorage()).Accounting.MaxMonthlyAuthorizations = 1
	for _, endpoint := range []string{path, path + "/" + c.ID} {
		r := e.Do(t, "GET", endpoint, nil, nil)
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
		key, keyErr := (*e.Store()).CreateAPIKey(t.Context(), (*e.Acct()).ID, hash, uuid.NewString(), scopes)
		if keyErr != nil {
			t.Fatal(keyErr)
		}
		if tc.permission != "" {
			if _, keyErr = (*e.Store()).SetObjectBucketAccessGrant(t.Context(), (*e.Acct()).ID, b.ID, key.ID, tc.permission); keyErr != nil {
				t.Fatal(keyErr)
			}
		}
		for _, endpoint := range []string{path, path + "/" + c.ID} {
			r := e.Do(t, "GET", endpoint, nil, map[string]string{"Authorization": "Bearer " + plaintext})
			if r.Code != tc.code {
				t.Fatal(tc, r.Code, r.Body.String())
			}
		}
		if tc.code == 200 {
			if keyErr = (*e.Store()).DeleteObjectBucketAccessGrant(t.Context(), (*e.Acct()).ID, b.ID, key.ID); keyErr != nil {
				t.Fatal(keyErr)
			}
			r := e.Do(t, "GET", path+"/"+c.ID, nil, map[string]string{"Authorization": "Bearer " + plaintext})
			if r.Code != 403 {
				t.Fatal("removed grant still allowed", r.Code)
			}
		}
	}
	for _, query := range []string{"?limit=0", "?limit=-1", "?limit=101", "?status=invalid", "?limit=2&limit=3", "?cursor=invalid", "?unexpected=1"} {
		if r := e.Do(t, "GET", path+query, nil, nil); r.Code != 400 {
			t.Fatal(query, r.Code, r.Body.String())
		}
	}
	for _, id := range []string{"invalid", uuid.NewString()} {
		if r := e.Do(t, "GET", path+"/"+id, nil, nil); r.Code != 404 {
			t.Fatal(id, r.Code)
		}
	}
	r := e.Do(t, "GET", path, nil, nil)
	var page api.ObjectWriteReceiptList
	if err = json.Unmarshal(r.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Items[0].ID != c.ID {
		t.Fatal(r.Body.String(), err)
	}
	after, err := (*e.Store()).ObjectUsage(t.Context(), (*e.Acct()).ID, report.PeriodStart)
	if err != nil || !reflect.DeepEqual(before, after) || len((*provider.Accessed())) != providerCalls {
		t.Fatal("receipt API admitted or contacted provider", err)
	}
}
