package apidserver_test

import (
	"archive/tar"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/eventfilter"
	"github.com/onebox-faas/faas/pkg/state"
)

// Events hunt #9: every path that applies event_triggers refuses a filter
// over the size or depth cap with a limit problem naming the limit and the
// observed value, before it writes anything.
func TestEventTriggerFilterCapsOnEveryWritePath(t *testing.T) {
	maxBytes, maxDepth := api.EventSubscriptionFilterMaxBytes, api.EventSubscriptionFilterMaxDepth
	large := `{"data":{"id":{"$eq":"` + strings.Repeat("x", maxBytes) + `"}}}`
	deep := strings.Repeat(`{"a":`, maxDepth) + `{"$eq":1}` + strings.Repeat("}", maxDepth)
	filters := []struct {
		name     string
		filter   string
		code     string
		limit    int
		observed int
	}{
		{name: "too large", filter: large, code: api.CodeEventSubscriptionFilterTooLarge, limit: maxBytes, observed: len(large)},
		{name: "too deep", filter: deep, code: api.CodeEventSubscriptionFilterTooDeep, limit: maxDepth, observed: maxDepth + 1},
	}
	paths := []struct {
		name  string
		apply func(t *testing.T, filter string) *api.Problem
	}{
		// Source deploys: CLI, upload sessions, GitHub, project and stage
		// deploys all load the archive manifest through loadSourceRefManifest.
		{name: "source deploy gregale.yaml", apply: func(t *testing.T, filter string) *api.Problem {
			manifest := "event_triggers:\n  - source: billing.*\n    type: invoice.paid\n    filter: '" + filter + "'\n"
			archive := operationSchemaArchive(t, []operationSchemaEntry{{"gregale.yaml", manifest, tar.TypeReg}})
			_, problem := apidserver.LoadSourceRefManifest(archive, state.App{Slug: "shop", Type: state.AppTypeApp}, api.PlanPro, false)
			return problem
		}},
		{name: "source deploy gregale.toml", apply: func(t *testing.T, filter string) *api.Problem {
			manifest := "[[triggers.event]]\nsource = \"billing.*\"\ntype = \"invoice.paid\"\nfilter = '" + filter + "'\n"
			archive := operationSchemaArchive(t, []operationSchemaEntry{{"gregale.toml", manifest, tar.TypeReg}})
			_, problem := apidserver.LoadSourceRefManifest(archive, state.App{Slug: "shop", Type: state.AppTypeApp}, api.PlanPro, false)
			return problem
		}},
		// Batch trigger create: the dashboard and functions deploys.
		{name: "batch trigger create", apply: func(t *testing.T, filter string) *api.Problem {
			e := apidserver.Setup(t, api.PlanPro)
			app, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, Slug: "shop-fn", Type: state.AppTypeFunction})
			if err != nil {
				t.Fatalf("CreateApp: %v", err)
			}
			manifest := "event_triggers:\n  - source: billing.*\n    type: invoice.paid\n    filter: '" + filter + "'\n"
			rec := e.Do(t, http.MethodPost, "/v1/triggers:batch_create", api.CreateTriggerBatchRequest{AppID: app.ID, ManifestYAML: manifest}, nil)
			subs, err := (*e.Store()).ListEventSubscriptionsForApp(t.Context(), app.ID)
			if err != nil || len(subs) != 0 {
				t.Fatalf("subscriptions = %+v (%v), want none", subs, err)
			}
			var problem api.Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
				t.Fatalf("decode %d %s: %v", rec.Code, rec.Body.String(), err)
			}
			return &problem
		}},
	}
	for _, path := range paths {
		for _, tc := range filters {
			t.Run(path.name+"/"+tc.name, func(t *testing.T) {
				problem := path.apply(t, tc.filter)
				if problem == nil || problem.Status != http.StatusUnprocessableEntity || problem.Code != tc.code ||
					problem.Limit == nil || *problem.Limit != int64(tc.limit) ||
					problem.Observed == nil || *problem.Observed != int64(tc.observed) || problem.DocsURL == "" {
					t.Fatalf("problem = %+v, want 422 %s limit %d observed %d with docs", problem, tc.code, tc.limit, tc.observed)
				}
			})
		}
	}
}

// The store's own cap check maps to the same limit problem.
func TestSourceRefEventSubscriptionProblemMapsFilterBound(t *testing.T) {
	problem := apidserver.SourceRefEventSubscriptionProblem(&eventfilter.BoundError{Bound: eventfilter.BoundBytes, Limit: 8192, Observed: 9000})
	if problem.Code != api.CodeEventSubscriptionFilterTooLarge || *problem.Limit != 8192 || *problem.Observed != 9000 {
		t.Fatalf("problem = %+v, want event_subscription_filter_too_large 8192/9000", problem)
	}
}
