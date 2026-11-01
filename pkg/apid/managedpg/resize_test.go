package managedpg

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestAdmitResize(t *testing.T) {
	for _, tc := range []struct {
		name    string
		plan    api.Plan
		class   string
		storage int64
		want    error
	}{
		{"class", api.PlanPro, "burstable", 0, nil},
		{"storage", api.PlanPro, "", 5 << 30, nil},
		{"both", api.PlanPro, "burstable", 2 << 30, nil},
		{"nothing", api.PlanPro, "", 0, managedpostgres.ErrInvalid},
		{"negative storage", api.PlanPro, "", -1, managedpostgres.ErrInvalid},
		{"unknown class", api.PlanPro, "huge", 0, managedpostgres.ErrInvalid},
		{"class outside plan", api.PlanHobby, "burstable", 0, managedpostgres.ErrNotInPlan},
		{"storage above plan", api.PlanPro, "", 5<<30 + 1, managedpostgres.ErrQuotaExceeded},
		{"no databases in plan", api.Plan("unlisted"), "", 1 << 30, managedpostgres.ErrNotInPlan},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := AdmitResize(tc.plan, api.ResizeManagedPostgresDatabaseRequest{RequestID: "r", ServiceClass: tc.class, StorageLimitBytes: tc.storage})
			if tc.want == nil && err != nil || tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("AdmitResize = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestLimitProblemCarriesLimitAndObserved(t *testing.T) {
	err := AdmitResize(api.PlanPro, api.ResizeManagedPostgresDatabaseRequest{RequestID: "r", StorageLimitBytes: 6 << 30})
	problem := LimitProblem(err)
	if problem == nil || problem.Status != http.StatusForbidden || *problem.Limit != 5<<30 || *problem.Observed != 6<<30 ||
		*problem.LimitBytes != 5<<30 || problem.DocsURL != StorageDocsURL {
		t.Fatalf("problem = %+v", problem)
	}
	if LimitProblem(managedpostgres.ErrQuotaExceeded) != nil {
		t.Fatal("plain quota error rendered as a storage limit")
	}
}

func TestResizeViewShowsStorageTarget(t *testing.T) {
	source := managedpostgres.Spec{Class: managedpostgres.ClassDevelopment, StorageLimitBytes: 1 << 30}
	created := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name          string
		op            managedpostgres.ResizeOperation
		class         string
		before, after int64
	}{
		{"class only", managedpostgres.ResizeOperation{SourceSpec: source, TargetClass: managedpostgres.ClassBurstable}, "burstable", 1 << 30, 1 << 30},
		{"storage only", managedpostgres.ResizeOperation{SourceSpec: source, TargetClass: managedpostgres.ClassDevelopment, TargetStorageLimitBytes: 4 << 30}, "development", 1 << 30, 4 << 30},
	} {
		tc.op.CreatedAt = created
		view := ResizeView(tc.op)
		if view.FromClass != "development" || view.TargetClass != tc.class || view.FromStorageLimitBytes != tc.before || view.TargetStorageLimitBytes != tc.after ||
			view.CompletedAt != "" || view.CreatedAt != "2026-10-13T12:00:00Z" {
			t.Errorf("%s: view = %+v", tc.name, view)
		}
	}
}
