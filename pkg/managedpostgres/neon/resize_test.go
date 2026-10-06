// adr: 623 — pinned compute convergence and ambiguous PATCH recovery.
package neon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

type resizeHTTPFixture struct {
	mu                                                                      sync.Mutex
	patches                                                                 int
	maximum                                                                 float64
	pending, changedDefault, duplicatePrimary, drift, lostResponse, delayed bool
}

func resizeHTTPProvider(t *testing.T, f *resizeHTTPFixture) *Provider {
	t.Helper()
	if f.maximum == 0 {
		f.maximum = 2
	}
	return testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		const root = "/api/v2/projects/quiet-river-12345678"
		switch r.URL.Path {
		case root:
			storage := int64(10 << 30)
			if f.drift {
				storage++
			}
			writeResponse(t, w, 200, map[string]any{"project": map[string]any{"id": "quiet-river-12345678", "region_id": "aws-eu-central-1", "pg_version": 17, "history_retention_seconds": 86400, "settings": map[string]any{"quota": map[string]any{"logical_size_bytes": storage}}}})
		case root + "/branches":
			branches := []map[string]any{{"id": "br-main-123", "default": !f.changedDefault, "current_state": "ready"}}
			if f.changedDefault {
				branches = append(branches, map[string]any{"id": "br-other-123", "default": true, "current_state": "ready"})
			}
			writeResponse(t, w, 200, map[string]any{"branches": branches})
		case root + "/endpoints":
			endpoints := []map[string]any{{"id": "ep-main-123", "branch_id": "br-main-123", "type": "read_write", "current_state": "idle", "autoscaling_limit_min_cu": 0.25, "autoscaling_limit_max_cu": f.maximum, "suspend_timeout_seconds": 300}}
			if f.duplicatePrimary {
				endpoints = append(endpoints, endpoints[0])
			}
			writeResponse(t, w, 200, map[string]any{"endpoints": endpoints})
		case root + "/operations":
			status := "finished"
			if f.pending {
				status = "running"
			}
			writeResponse(t, w, 200, map[string]any{"operations": []map[string]any{{"id": "op-resize", "status": status, "endpoint_id": "ep-main-123", "branch_id": "br-main-123"}}, "pagination": map[string]any{}})
		case root + "/endpoints/ep-main-123":
			if r.Method != http.MethodPatch {
				t.Error("unexpected mutation method", r.Method)
			}
			var payload map[string]map[string]float64
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			compute := payload["endpoint"]
			if len(payload) != 1 || len(compute) != 2 || compute["autoscaling_limit_min_cu"] != 0.25 || compute["autoscaling_limit_max_cu"] != 1 {
				t.Error("resize changed fields outside compute", payload)
			}
			f.patches++
			if !f.delayed {
				f.maximum = compute["autoscaling_limit_max_cu"]
			}
			if f.lostResponse {
				f.lostResponse = false
				writeResponse(t, w, 503, map[string]any{"message": "private provider diagnostic"})
				return
			}
			writeResponse(t, w, 200, map[string]any{})
		default:
			t.Error("resize touched unexpected API", r.Method, r.URL.Path)
			writeResponse(t, w, 404, nil)
		}
	}))
}
func resizeHTTPrequest() managedpostgres.UpdateRequest {
	previous := testDatabaseSpec()
	target := previous
	target.Class = managedpostgres.ClassDevelopment
	return managedpostgres.UpdateRequest{ResourceID: "quiet-river-12345678", DataResourceID: "quiet-river-12345678/br-main-123", PreviousSpec: previous, Spec: target, Generation: 2, IdempotencyKey: "resize-request"}
}
func TestResizeAdoptsAppliedConfigurationAfterLostResponse(t *testing.T) {
	f := &resizeHTTPFixture{lostResponse: true}
	p := resizeHTTPProvider(t, f)
	r := resizeHTTPrequest()
	if _, err := p.Update(t.Context(), r); !errors.Is(err, managedpostgres.ErrUnavailable) {
		t.Fatal("uncertain response", err)
	}
	observed, err := p.Update(t.Context(), r)
	if err != nil || observed.Status != managedpostgres.ProviderStatusReady || observed.Spec != r.Spec || observed.ProviderResourceID != r.ResourceID || observed.DataResourceID != r.DataResourceID || f.patches != 1 {
		t.Fatal("recovery replaced/repatched resource", observed, err, f.patches)
	}
	if _, err = p.Update(t.Context(), r); err != nil || f.patches != 1 {
		t.Fatal("replay not stable", err)
	}
}
func TestResizeFailsClosedBeforeMutation(t *testing.T) {
	for _, kind := range []string{"changed default", "duplicate primary", "configuration drift", "pending operation", "different project", "different branch", "nonclass mutation"} {
		t.Run(kind, func(t *testing.T) {
			f := &resizeHTTPFixture{}
			p := resizeHTTPProvider(t, f)
			r := resizeHTTPrequest()
			switch kind {
			case "changed default":
				f.changedDefault = true
			case "duplicate primary":
				f.duplicatePrimary = true
			case "configuration drift":
				f.drift = true
			case "pending operation":
				f.pending = true
			case "different project":
				r.DataResourceID = "other-project-123/br-main-123"
			case "different branch":
				r.ResourceID = "quiet-river-12345678/br-other-123"
			case "nonclass mutation":
				r.Spec.ScaleToZero = false
			}
			observed, err := p.Update(context.Background(), r)
			if kind == "pending operation" {
				if err != nil || observed.Status != managedpostgres.ProviderStatusPending {
					t.Fatal("did not wait", observed, err)
				}
			} else if err == nil {
				t.Fatal("unsafe change accepted", observed)
			}
			if f.patches != 0 {
				t.Fatal("mutated while unsafe")
			}
			if err != nil && strings.Contains(err.Error(), "private provider") {
				t.Fatal("secret error leaked")
			}
		})
	}
}
func TestResizeAcceptedPatchWaitsForVisibleConfiguration(t *testing.T) {
	f := &resizeHTTPFixture{delayed: true}
	p := resizeHTTPProvider(t, f)
	observed, err := p.Update(t.Context(), resizeHTTPrequest())
	if err != nil || observed.Status != managedpostgres.ProviderStatusPending || f.patches != 1 {
		t.Fatal("accepted stale observation became ready", observed, err)
	}
}
