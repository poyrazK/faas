package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestObjectStorageAccountingAPIGates(t *testing.T) {
	e := setup(t, api.PlanPro)
	createApp(t, e, "accounting")
	a, b := &fakeObjectProvider{}, &fakeObjectProvider{}
	e.s.WithObjectStorage(objectRegistry(t, a, b, "external"))
	setS3Flag(t, e, true)
	path := "/v1/apps/accounting/buckets"
	bucket := bucketResponse(t, e.do(t, "POST", path, map[string]any{"name": "assets"}, nil), 201)
	sign := path + "/" + bucket.ID + "/signed-url"
	body := map[string]any{"method": "PUT", "key": "file", "size_bytes": 100}
	if r := e.do(t, "POST", sign, body, nil); r.Code != 503 {
		t.Fatal("unconfigured accounting", r.Code)
	}
	qualifyObjectAccounting(t, e, bucket.ID)
	e.s.objectStorage.Accounting.MaxBucketBytes = 100
	if r := e.do(t, "POST", sign, body, nil); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	before := len(a.accessed)
	body["key"] = "another"
	r := e.do(t, "POST", sign, body, nil)
	if r.Code != 409 || len(a.accessed) != before {
		t.Fatal("capacity guard reached signer", r.Code)
	}
	var problem api.Problem
	if err := json.Unmarshal(r.Body.Bytes(), &problem); err != nil || problem.Limit == nil || *problem.Limit != 100 || problem.Observed == nil || *problem.Observed != 200 {
		t.Fatal(problem, err)
	}
	var usage api.ObjectStorageUsageResponse
	r = e.do(t, "GET", "/v1/account/object-storage-usage", nil, nil)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	if err := json.Unmarshal(r.Body.Bytes(), &usage); err != nil || usage.Usage.CapacityBytes != 100 || usage.Usage.ObservedBytes != 0 {
		t.Fatal(usage, err)
	}
	e.s.objectStorage.Accounting.MaxMonthlyAuthorizations = 1
	if r := e.do(t, "POST", sign, map[string]any{"method": "GET", "key": "file"}, nil); r.Code != 402 {
		t.Fatal("budgeted GET", r.Code)
	}
	if r := e.do(t, "DELETE", path+"/"+bucket.ID+"/objects?key=file", nil, nil); r.Code != 204 {
		t.Fatal("cleanup blocked", r.Code)
	}
}

func TestObjectStorageUsageReportOperatorBoundary(t *testing.T) {
	e := newObsEnv(t, []string{"admin"}, "ops@faas.dev", "ops@faas.dev")
	createApp(t, e, "reports")
	e.s.WithObjectStorage(objectRegistry(t, &fakeObjectProvider{}, &fakeObjectProvider{}, "external"))
	setS3Flag(t, e, true)
	bucket := bucketResponse(t, e.do(t, "POST", "/v1/apps/reports/buckets", map[string]any{"name": "assets"}, nil), 201)
	qualifyObjectAccounting(t, e, bucket.ID)
	backend, err := e.s.objectStorage.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	report := api.ObjectStorageUsageReport{AccountID: e.acct.ID, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Source: "fixture", PeriodStart: state.ObjectStoragePeriod(time.Now()), ObservedAt: time.Now().UTC(), CostMillicents: 12}
	path := "/v1/admin/object-storage/usage-reports"
	if r := e.do(t, "POST", path, report, nil); r.Code != 403 {
		t.Fatal("bearer imported usage", r.Code, r.Body.String())
	}
	for range 2 {
		if r := e.doAdmin(t, "POST", path, report, nil); r.Code != 204 {
			t.Fatal("operator report", r.Code, r.Body.String())
		}
	}
	report.CostMillicents = 0
	if r := e.doAdmin(t, "POST", path, report, nil); r.Code != 409 {
		t.Fatal("conflicting report", r.Code)
	}
	if r := e.doAdmin(t, "POST", path, map[string]any{"account_id": e.acct.ID}, nil); r.Code != 400 {
		t.Fatal("missing counters treated as zero", r.Code)
	}
}

type inventoryProvider struct {
	fakeObjectProvider
	fail  bool
	cycle bool
}

func (p *inventoryProvider) ListObjects(_ context.Context, _, _, cursor string, _ int32) (objectstorage.ObjectPage, error) {
	if cursor == "" {
		return objectstorage.ObjectPage{Items: []objectstorage.Object{{Key: "a", Size: 3}}, NextCursor: "page2"}, nil
	}
	if p.fail {
		return objectstorage.ObjectPage{}, errors.New("do not log upstream secrets")
	}
	if p.cycle {
		return objectstorage.ObjectPage{NextCursor: "page2"}, nil
	}
	return objectstorage.ObjectPage{Items: []objectstorage.Object{{Key: "b", Size: 4}}}, nil
}

func TestObjectStorageInventoryPublishesOnlyCompleteScans(t *testing.T) {
	for _, mode := range []string{"complete", "failed", "cycle"} {
		t.Run(mode, func(t *testing.T) {
			e := setup(t, api.PlanPro)
			createApp(t, e, "inventory")
			p := &inventoryProvider{fail: mode == "failed", cycle: mode == "cycle"}
			registry, err := objectstorage.NewRegistry(objectstorage.Config{DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "external"}, Backends: []objectstorage.BackendConfig{{ID: "external", Driver: "fake", Region: "us-east-1", Namespace: "isolated", Endpoint: "https://s3.example.test", S3Region: "us-east-1"}}}, func(string) string { return "" }, map[string]objectstorage.Factory{"fake": func(objectstorage.BackendConfig, func(string) string) (objectstorage.Provider, error) { return p, nil }})
			if err != nil {
				t.Fatal(err)
			}
			e.s.WithObjectStorage(registry)
			setS3Flag(t, e, true)
			bucketResponse(t, e.do(t, "POST", "/v1/apps/inventory/buckets", map[string]any{"name": "assets"}, nil), 201)
			outcome := ""
			if err := e.s.reconcileObjectInventories(context.Background(), func(v string) { outcome = v }); err != nil {
				t.Fatal(err)
			}
			snapshot, err := e.store.ObjectUsage(context.Background(), e.acct.ID, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			u := snapshot.Buckets[0]
			if mode == "complete" {
				if u.ObservedBytes != 7 || u.ObservedKeys != 2 || u.ObservedAt.IsZero() || outcome != "success" {
					t.Fatal(u, outcome)
				}
			} else if !u.ObservedAt.IsZero() || outcome != "failed" {
				t.Fatal("partial scan published", u, outcome)
			}
		})
	}
}
