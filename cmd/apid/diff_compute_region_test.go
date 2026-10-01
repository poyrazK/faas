package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDiffComputeRegionAdmissionAndImmutability(t *testing.T) {
	for _, tc := range []struct {
		name, requested, existing, code string
		enabled, paused, outage, exists bool
		omit, null                      bool
	}{
		{name: "fresh enabled", requested: "local", enabled: true},
		{name: "fresh gate off", requested: "local", code: api.CodeCapacity},
		{name: "fresh unknown", requested: "eu-unknown", enabled: true, code: api.CodeComputeRegionUnknown},
		{name: "fresh paused", requested: "local", enabled: true, paused: true, code: api.CodeComputeRegionAdmissionPaused},
		{name: "fresh outage", requested: "local", enabled: true, outage: true, code: api.CodeComputeRegionUnavailable},
		{name: "fresh invalid", requested: "EU", enabled: true, code: api.CodeValidation},
		{name: "existing same paused gate off", requested: "local", existing: "local", exists: true, paused: true},
		{name: "existing omission", existing: "local", exists: true, paused: true, omit: true},
		{name: "existing null", existing: "local", exists: true, paused: true, null: true},
		{name: "existing repin", requested: "eu-unknown", existing: "local", exists: true, code: api.CodeConflict},
		{name: "existing clear", existing: "local", exists: true, code: api.CodeConflict},
		{name: "legacy repin", requested: "local", exists: true, enabled: true, code: api.CodeConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newDiffTestEnv(t, api.PlanPro)
			e.s.regionalComputeEnabled = tc.enabled
			if tc.exists {
				if _, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "regional", Type: state.AppTypeApp, RAMMB: 128, ComputeRegion: tc.existing}); err != nil {
					t.Fatal(err)
				}
			}
			if tc.paused {
				if _, err := e.store.UpsertComputeRegion(t.Context(), state.ComputeRegion{ID: "local", DisplayName: "Local", AcceptingNewApps: false}); err != nil {
					t.Fatal(err)
				}
			}
			if tc.outage {
				node, err := e.store.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
				if err != nil {
					t.Fatal(err)
				}
				if err := e.store.SetComputeNodeActive(t.Context(), node.ID, false); err != nil {
					t.Fatal(err)
				}
			}
			requested := &tc.requested
			if tc.omit {
				requested = nil
			}
			body, err := json.Marshal(api.DiffRequest{ComputeRegion: requested})
			if err != nil {
				t.Fatal(err)
			}
			if tc.null {
				body = []byte(`{"compute_region":null}`)
			}
			rec := postDiffReq(t, e, "regional", body)
			var response api.DiffResponse
			if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &response) != nil || response.Blocking != (tc.code != "") {
				t.Fatalf("preview=%d %s", rec.Code, rec.Body.String())
			}
			if tc.code != "" {
				found := false
				for _, b := range response.Diff.Breaks {
					found = found || b.Field == "compute_region" && b.Code == tc.code
				}
				if !found {
					t.Fatalf("missing placement refusal: %+v", response)
				}
			}
			if tc.exists {
				app, err := e.store.AppBySlug(t.Context(), "regional")
				if err != nil || app.ComputeRegion != tc.existing || response.Diff.ComputeRegion != tc.existing {
					t.Fatalf("existing identity changed: %+v %+v %v", app, response, err)
				}
			} else if _, err := e.store.AppBySlug(t.Context(), "regional"); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("preview created app: %v", err)
			}
		})
	}
}
