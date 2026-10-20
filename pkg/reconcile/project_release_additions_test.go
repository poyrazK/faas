package reconcile

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSourceAdditionPlanIsReadOnlyAndRejectsRemovals(t *testing.T) {
	project := state.Project{ID: "project", AccountID: "account"}
	web := reposcan.Workload{Name: "web", RootDir: "web", Class: reposcan.ClassHTTP}
	app := workloadToDraftApp(project, web, "", api.PlanPro, workloadNameSet([]reposcan.Workload{web}))
	app.ID = "web-id"
	newService := reposcan.Workload{Name: "metrics", RootDir: "metrics", Class: reposcan.ClassHTTP}
	for _, scenario := range []string{"valid", "remove", "command", "worker", "image", "env", "binding", "cron", "existing_dependency"} {
		t.Run(scenario, func(t *testing.T) {
			w, addition := web, newService
			switch scenario {
			case "command":
				w.Command = []string{"changed"}
			case "worker":
				addition.Class = reposcan.ClassWorker
			case "image":
				addition.Image = "registry/image"
			case "env":
				addition.EnvKeys = []string{"TOKEN"}
			case "binding":
				addition.DependsOn = []string{"web"}
			case "existing_dependency":
				w.DependsOn = []string{"metrics"}
			case "cron":
				addition.Schedule = "* * * * *"
			}
			workloads := []reposcan.Workload{w, addition}
			if scenario == "remove" {
				workloads = workloads[1:]
			}
			selected, drafts, policy, err := ProjectSourceAdditions(project, api.PlanPro, 1800, []state.App{app}, nil, reposcan.Result{Workloads: workloads})
			if scenario != "valid" {
				if err == nil {
					t.Fatal("unsupported addition plan passed")
				}
				return
			}
			if err != nil || len(selected) != 1 || len(drafts) != 1 || drafts[0].ID != "" || len(policy) != 1 || policy[0].AppID != "" || len(policy[0].Fingerprint) != 64 {
				t.Fatalf("selected=%+v drafts=%+v policy=%+v error=%v", selected, drafts, policy, err)
			}
			reserved := drafts[0]
			reserved.ID = "reserved-id"
			_, retry, _, err := ProjectSourceAdditions(project, api.PlanPro, 1800, []state.App{app, reserved}, map[string]bool{reserved.ID: true}, reposcan.Result{Workloads: workloads})
			if err != nil || len(retry) != 1 || retry[0].ID != reserved.ID {
				t.Fatalf("reservation not reused: %v %+v", err, retry)
			}
			if app.Manifest.RevisionPinTTLSeconds != 0 {
				t.Fatal("planning mutated the serving app")
			}
		})
	}
}
