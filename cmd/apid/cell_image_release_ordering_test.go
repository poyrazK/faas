package main

// adr: 605

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCellImageReleaseOrderingSDKAndDelayedRequest(t *testing.T) {
	globalAppID := uuid.NewString()
	for _, region := range []string{"eu-fsn", "eu-hel"} {
		t.Run(region, func(t *testing.T) {
			e := setup(t, api.PlanPro)
			configureCellDeploymentTest(t, e.store, e.s, region)
			app := seedCellDeploymentApp(t, e.store, e.acct, "ordered-shop", uuid.NewString(), region)
			cell := httptest.NewTLSServer(e.h)
			defer cell.Close()
			client := api.NewClient(cell.URL, e.key).SetCompletionCache(nil)
			client.HTTPClient().Transport = cell.Client().Transport
			releaseID := uuid.NewString()
			request := api.PutCellImageReleaseRequest{Image: cellImageRequest().Image,
				Ordering: &api.CellImageReleaseOrdering{GlobalAppID: globalAppID, Generation: 2}}
			first, err := client.PutCellImageRelease(t.Context(), e.s.controlPlaneCell.ID, app.ID, releaseID, request)
			if err != nil || first.Ordering == nil || *first.Ordering != *request.Ordering || first.Region != region {
				t.Fatalf("ordered acceptance: %+v %v", first, err)
			}
			read, err := client.GetCellImageRelease(t.Context(), e.s.controlPlaneCell.ID, app.ID, releaseID)
			if err != nil || read.Ordering == nil || *read.Ordering != *request.Ordering {
				t.Fatalf("ordering receipt: %+v %v", read, err)
			}
			stale := api.PutCellImageReleaseRequest{Image: request.Image,
				Ordering: &api.CellImageReleaseOrdering{GlobalAppID: globalAppID, Generation: 1}}
			path := cellImageReleaseURL(e.s.controlPlaneCell.ID, app.ID, uuid.NewString())
			rec := e.do(t, http.MethodPut, path, stale, nil)
			if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), api.CodeRegionalImageReleaseOrder) {
				t.Fatalf("delayed older request: %d %s", rec.Code, rec.Body)
			}
			rows, err := e.store.ListDeploymentsForApp(t.Context(), app.ID, 0, 0)
			if err != nil || len(rows) != 1 || rows[0].Status != state.DeployPending {
				t.Fatalf("stale request superseded accepted deployment: %+v %v", rows, err)
			}
			request.Ordering = &api.CellImageReleaseOrdering{GlobalAppID: globalAppID, Generation: 3}
			if _, err := client.PutCellImageRelease(t.Context(), e.s.controlPlaneCell.ID, app.ID, uuid.NewString(), request); err != nil {
				t.Fatal(err)
			}
			request.Ordering = &api.CellImageReleaseOrdering{GlobalAppID: globalAppID, Generation: 2}
			retry, err := client.PutCellImageRelease(t.Context(), e.s.controlPlaneCell.ID, app.ID, releaseID, request)
			if err != nil || !retry.Replayed || retry.DeploymentID == nil || *retry.DeploymentID != *first.DeploymentID {
				t.Fatalf("old receipt retry: %+v %v", retry, err)
			}
			request.Ordering = nil
			rec = e.do(t, http.MethodPut, cellImageReleaseURL(e.s.controlPlaneCell.ID, app.ID, releaseID), request, nil)
			if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), api.CodeRegionalImageReleaseConflict) {
				t.Fatalf("omitted immutable ordering: %d %s", rec.Code, rec.Body)
			}
		})
	}
}

func TestCellImageReleaseOrderingValidation(t *testing.T) {
	e, app := cellDeploymentHarness(t)
	path := cellImageReleaseURL(e.s.controlPlaneCell.ID, app.ID, uuid.NewString())
	for _, ordering := range []string{`{}`, `{"global_app_id":"bad","generation":1}`, `{"global_app_id":"` + uuid.NewString() + `","generation":0}`, `{"global_app_id":"` + uuid.NewString() + `","generation":9007199254740992}`, `{"global_app_id":"` + uuid.NewString() + `","generation":1.5}`, `{"global_app_id":"` + uuid.NewString() + `","generation":1,"extra":true}`} {
		r := httptest.NewRequest(http.MethodPut, path, strings.NewReader(`{"image":"`+cellImageRequest().Image+`","ordering":`+ordering+`}`))
		r.Header.Set("Authorization", "Bearer "+e.key)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid ordering: %d %s", rec.Code, rec.Body)
		}
	}
}
