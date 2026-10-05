//go:build !no_pg

package main

// adr: 604

import (
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"strings"
	"testing"
)

func TestCellImageReleasePGCollection(t *testing.T) {
	e := cellDeploymentPGHarness(t)
	app := seedCellDeploymentApp(t, e.store, e.acct, "receipt-shop", uuid.NewString(), "eu-fsn")
	path := cellImageReleaseURL(e.s.controlPlaneCell.ID, app.ID, uuid.NewString())
	body := api.PutCellImageReleaseRequest{Image: cellImageRequest().Image}
	first := decodeCellImageRelease(t, e.do(t, http.MethodPut, path, body, nil))
	if _, err := e.pool.Exec(t.Context(), `DELETE FROM deployments WHERE id=$1`, *first.DeploymentID); err != nil {
		t.Fatal(err)
	}
	receipt := decodeCellImageRelease(t, e.do(t, http.MethodGet, path, nil, nil))
	if receipt.DeploymentID != nil || receipt.DeploymentStatus != "expired" {
		t.Fatalf("collected receipt: %+v", receipt)
	}
	rec := e.do(t, http.MethodPut, path, body, nil)
	if rec.Code != http.StatusGone || !strings.Contains(rec.Body.String(), api.CodeRegionalImageReleaseExpired) {
		t.Fatalf("collected release retry: %d %s", rec.Code, rec.Body)
	}
	rows, err := e.store.ListDeploymentsForApp(t.Context(), app.ID, 0, 0)
	if err != nil || len(rows) != 0 {
		t.Fatalf("collected release recreated: %d %v", len(rows), err)
	}
}

func TestCellImageReleasePGLookupRaces(t *testing.T) {
	for _, deleteApp := range []bool{false, true} {
		name := "rename"
		if deleteApp {
			name = "delete"
		}
		t.Run(name, func(t *testing.T) {
			e := cellDeploymentPGHarness(t)
			runCellImageReleaseLookupRace(t, e.store, e.s, e.acct, e.key, deleteApp)
		})
	}
}
