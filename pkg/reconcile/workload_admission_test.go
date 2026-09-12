package reconcile

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkloadAdmissionReasons(t *testing.T) {
	tests := []struct {
		name      string
		workloads []reposcan.Workload
		apps      []state.App
		projectID string
		want      []string
	}{
		{
			name:      "valid create",
			workloads: []reposcan.Workload{{Name: "valid-service", RootDir: "services/api"}},
		},
		{
			name: "serverless requires explicit function deploy",
			workloads: []reposcan.Workload{{
				Name:       "api",
				DetectedBy: reposcan.Detection{Detector: "serverless"},
			}},
			want: []string{"without an execution adapter"},
		},
		{
			name:      "intended member update",
			workloads: []reposcan.Workload{{Name: "member-api", RootDir: "services/api"}},
			apps:      []state.App{{Slug: "member-api", WorkloadName: "member-api", RootDir: "services/api", ProjectID: "project-1"}},
			projectID: "project-1",
		},
		{
			name: "invalid compose names",
			workloads: []reposcan.Workload{
				{Name: "API_service"},
				{Name: "api.service"},
				{Name: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
			},
			want: []string{"API_service", "api.service", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		},
		{
			name:      "unrelated account slug collision",
			workloads: []reposcan.Workload{{Name: "existing-api", RootDir: "services/new"}},
			apps:      []state.App{{Slug: "existing-api", WorkloadName: "standalone", RootDir: "", ProjectID: "project-2"}},
			projectID: "project-1",
			want:      []string{"outside this project member"},
		},
		{
			name:      "same workload key in another project still conflicts",
			workloads: []reposcan.Workload{{Name: "existing-api", RootDir: "services/api"}},
			apps:      []state.App{{Slug: "existing-api", WorkloadName: "existing-api", RootDir: "services/api", ProjectID: "project-2"}},
			projectID: "project-1",
			want:      []string{"outside this project member"},
		},
		{
			name: "empty plan",
			want: []string{EmptyWorkloadPlanReason},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reasons := WorkloadAdmissionReasons(tt.workloads, tt.apps, tt.projectID)
			if len(reasons) != len(tt.want) {
				t.Fatalf("reasons = %#v, want %d", reasons, len(tt.want))
			}
			for i, want := range tt.want {
				if !strings.Contains(reasons[i], want) {
					t.Errorf("reason[%d] = %q, want substring %q", i, reasons[i], want)
				}
			}
		})
	}
}

func TestReconcileRejectsInvalidWorkloadBeforeWrites(t *testing.T) {
	store := newFakeStore()
	_, project := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	createCalls := 0
	store.createAppIfUnderQuotaHook = func(app state.App) (state.App, error) {
		createCalls++
		return app, nil
	}

	result, err := freshService(store, newFakeAuditor(store)).Reconcile(
		context.Background(),
		project,
		reposcan.Result{
			Tier: reposcan.TierCompose,
			Workloads: []reposcan.Workload{
				{Name: "valid-api", RootDir: "valid", Source: "compose.yaml: valid-api", Tier: reposcan.TierCompose},
				{Name: "API_service", RootDir: "invalid", Source: "compose.yaml: API_service", Tier: reposcan.TierCompose},
			},
		},
		"sha-invalid", "main", nil,
	)
	if !errors.Is(err, ErrInvalidWorkloadPlan) {
		t.Fatalf("Reconcile error = %v, want ErrInvalidWorkloadPlan", err)
	}
	if createCalls != 0 || len(result.Added) != 0 {
		t.Fatalf("partial apply: create calls=%d added=%d", createCalls, len(result.Added))
	}
	if events := store.snapshotEvents(); len(events) != 0 {
		t.Fatalf("invalid plan emitted mutation audit rows: %+v", events)
	}
}
