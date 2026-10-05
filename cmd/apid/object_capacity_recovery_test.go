package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type capacityInventoryProvider struct {
	*fakeObjectProvider
	pages  []objectstorage.ObjectPage
	failAt int
	calls  int
	onPage func()
}

func (p *capacityInventoryProvider) ListObjects(context.Context, string, string, string, int32) (objectstorage.ObjectPage, error) {
	p.calls++
	if p.onPage != nil {
		p.onPage()
	}
	if p.calls == p.failAt {
		return objectstorage.ObjectPage{}, objectstorage.ErrUnavailable
	}
	return p.pages[min(p.calls-1, len(p.pages)-1)], nil
}
func TestCompleteCapacityInventory(t *testing.T) {
	for _, tc := range []struct {
		name        string
		pages       []objectstorage.ObjectPage
		failAt      int
		ok          bool
		bytes, keys int64
	}{
		{name: "complete empty", pages: []objectstorage.ObjectPage{{}}, ok: true},
		{name: "complete pages", pages: []objectstorage.ObjectPage{{Items: []objectstorage.Object{{Key: "a", Size: 3}}, NextCursor: "next"}, {Items: []objectstorage.Object{{Key: "b", Size: 4}}}}, ok: true, bytes: 7, keys: 2},
		{name: "partial failure", pages: []objectstorage.ObjectPage{{Items: []objectstorage.Object{{Key: "a", Size: 3}}, NextCursor: "next"}}, failAt: 2},
		{name: "duplicate key", pages: []objectstorage.ObjectPage{{Items: []objectstorage.Object{{Key: "a", Size: 3}, {Key: "a", Size: 4}}}}},
		{name: "negative size", pages: []objectstorage.ObjectPage{{Items: []objectstorage.Object{{Key: "a", Size: -1}}}}},
		{name: "empty truncated page", pages: []objectstorage.ObjectPage{{NextCursor: "next"}}},
		{name: "unexpected grouped keys", pages: []objectstorage.ObjectPage{{CommonPrefixes: []string{"folder/"}}}},
		{name: "cursor cycle", pages: []objectstorage.ObjectPage{{Items: []objectstorage.Object{{Key: "a", Size: 3}}, NextCursor: "next"}, {Items: []objectstorage.Object{{Key: "b", Size: 4}}, NextCursor: "next"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &capacityInventoryProvider{pages: tc.pages, failAt: tc.failAt}
			bytes, keys, err := completeObjectInventory(context.Background(), p, "physical")
			if tc.ok && (err != nil || bytes != tc.bytes || keys != tc.keys) || !tc.ok && err == nil {
				t.Fatal(bytes, keys, err)
			}
		})
	}
}
func TestObjectCapacityAPIAndDisabledRecovery(t *testing.T) {
	e := setup(t, api.PlanHobby)
	a, b := &fakeObjectProvider{}, &fakeObjectProvider{}
	e.s.WithObjectStorage(objectRegistry(t, a, b, "external"))
	setS3Flag(t, e, true)
	createApp(t, e, "capacity")
	bucket := bucketResponse(t, e.do(t, "POST", "/v1/apps/capacity/buckets", map[string]any{"name": "assets"}, nil), 201)
	ctx := context.Background()
	token := uuid.NewString()
	if err := e.store.ClaimObjectInventory(ctx, bucket.ID, "inventory"); err != nil {
		t.Fatal(err)
	}
	if err := e.store.FinishObjectInventory(ctx, bucket.ID, "inventory", 0, 0); err != nil {
		t.Fatal(err)
	}
	// Resolve the app ID through its durable app catalog.
	app, appErr := e.store.AppBySlug(ctx, "capacity")
	if appErr != nil {
		t.Fatal(appErr)
	}
	row, err := e.store.GetObjectBucket(ctx, e.acct.ID, app.ID, bucket.ID)
	if err != nil {
		t.Fatal(err)
	}
	report := api.ObjectStorageUsageReport{AccountID: e.acct.ID, BackendID: row.BackendID, BackendFingerprint: row.BackendFingerprint, Source: "provider", PeriodStart: state.ObjectStoragePeriod(time.Now()), ObservedAt: time.Now().Add(-time.Minute)}
	if err = e.store.RecordObjectUsageReport(ctx, report); err != nil {
		t.Fatal(err)
	}
	if err = e.store.BeginObjectWrite(ctx, e.acct.ID, bucket.ID, token, "key", 10, e.s.objectStorage.Accounting); err != nil {
		t.Fatal(err)
	}
	if err = e.store.SettleObjectWrite(ctx, e.acct.ID, bucket.ID, token); err != nil {
		t.Fatal(err)
	}
	path := "/v1/apps/capacity/buckets/" + bucket.ID + "/capacity-reconciliations"
	setS3Flag(t, e, false)

	for _, scope := range []string{api.ScopeStorageRead, api.ScopeStorageWrite} {
		plaintext, hash, keyErr := api.GenerateAPIKey()
		if keyErr != nil {
			t.Fatal(keyErr)
		}
		if _, keyErr = e.store.CreateAPIKey(ctx, e.acct.ID, hash, "ungranted", []string{scope}); keyErr != nil {
			t.Fatal(keyErr)
		}
		for _, method := range []string{"POST", "GET", "DELETE"} {
			deniedPath := path
			if method != "POST" {
				deniedPath += "/" + uuid.NewString()
			}
			if denied := e.do(t, method, deniedPath, nil, map[string]string{"Authorization": "Bearer " + plaintext}); denied.Code != 403 {
				t.Fatal("scope/grant bypass", scope, method, denied.Code, denied.Body.String())
			}
		}
	}
	// Test provider already returns a deliberately truncated inventory. Failure
	// must preserve reservations and a cancellable durable intent.
	r := e.do(t, "POST", path, nil, nil)
	if r.Code != 202 {
		t.Fatal(r.Code, r.Body.String())
	}

	for _, private := range []string{"lease_token", "lease_until", "physical_name", "account_id"} {
		if strings.Contains(r.Body.String(), private) {
			t.Fatal("private reconciliation metadata exposed", private)
		}
	}
	var j api.ObjectCapacityReconciliation
	if err = json.Unmarshal(r.Body.Bytes(), &j); err != nil {
		t.Fatal(err)
	}
	if err = e.s.reconcileObjectCapacity(ctx, nil); err != nil {
		t.Fatal(err)
	}
	r = e.do(t, "GET", path+"/"+j.ID, nil, nil)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	if err = json.Unmarshal(r.Body.Bytes(), &j); err != nil || j.State != "waiting" || j.LastErrorCode != "inventory_failed" || j.ReclaimedBytes != 0 {
		t.Fatal(j, err)
	}
	if r = e.do(t, "DELETE", path+"/"+j.ID, nil, nil); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r = e.do(t, "GET", path+"/"+uuid.NewString(), nil, nil); r.Code != 404 {
		t.Fatal(r.Code)
	}

	// A new intent resumes independently of the hot flag and succeeds once the
	// provider returns a complete inventory.
	a.objects = map[string][]byte{}
	r = e.do(t, "POST", path, nil, nil)
	if r.Code != 202 {
		t.Fatal(r.Code, r.Body.String())
	}
	if err = json.Unmarshal(r.Body.Bytes(), &j); err != nil {
		t.Fatal(err)
	}
	if err = e.s.reconcileObjectCapacity(ctx, nil); err != nil {
		t.Fatal(err)
	}
	r = e.do(t, "GET", path+"/"+j.ID, nil, nil)
	if err = json.Unmarshal(r.Body.Bytes(), &j); err != nil || j.State != "completed" || j.ReclaimedBytes != 10 || j.ReclaimedKeys != 1 {
		t.Fatal(j, err)
	}

	// Removing provider configuration cannot make a durable fence permanent.
	e.s.WithObjectStorage(nil)
	r = e.do(t, "POST", path, nil, nil)
	if r.Code != 202 {
		t.Fatal(r.Code, r.Body.String())
	}
	if err = json.Unmarshal(r.Body.Bytes(), &j); err != nil {
		t.Fatal(err)
	}
	if err = e.s.reconcileObjectCapacity(ctx, nil); err != nil {
		t.Fatal(err)
	}
	r = e.do(t, "GET", path+"/"+j.ID, nil, nil)
	if err = json.Unmarshal(r.Body.Bytes(), &j); err != nil || j.State != "waiting" || j.LastErrorCode != "inventory_failed" {
		t.Fatal(j, err)
	}
	if r = e.do(t, "DELETE", path+"/"+j.ID, nil, nil); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
}
