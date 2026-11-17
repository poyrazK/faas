package reposcan

import (
	"fmt"
	"io/fs"

	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

// RepositoryFunctionsCapability is the product capability that releases
// ScanOptions.Functions (pkg/productcap/catalog.json).
const RepositoryFunctionsCapability = "repository-functions"

// FunctionWorkload marks a workload that deploys as a function app
// (ADR-20261108160000): one entry of a functions project.
type FunctionWorkload struct {
	Runtime string `json:"runtime"`
	// Handler is the wire handler: a file inside the shared source tree,
	// or gregalemanifest.DefaultFunctionHandler for a root's conventional
	// handler.* file.
	Handler string `json:"handler"`
}

// detectFunctionsProject reads a functions project: gregale.yaml
// `functions:` merged with config in code, or functions/<name>/ handlers
// that declare a config. Each function is one workload of the function
// kind, rooted where `gregale deploy` would upload it from. ok is false
// when the repository is not a functions project.
func detectFunctionsProject(fsys fs.FS) ([]Workload, bool, error) {
	m, found, err := gregalemanifest.LoadFS(fsys, ".")
	if err != nil {
		return nil, false, fmt.Errorf("reposcan: %w", err)
	}
	if !found {
		m = nil
	}
	m, _, err = gregalemanifest.ApplyInlineConfigFS(m, fsys)
	if err != nil {
		return nil, false, fmt.Errorf("reposcan: functions config in code: %w", err)
	}
	if m == nil || len(m.Functions) == 0 {
		return nil, false, nil
	}
	plans, err := m.PlanFunctionsFS(fsys, "")
	if err != nil {
		return nil, false, fmt.Errorf("reposcan: gregale.yaml functions: %w", err)
	}
	out := make([]Workload, 0, len(plans))
	for _, p := range plans {
		w := Workload{
			Name:       p.Spec.Name,
			RootDir:    functionRootDir(p.Root),
			Class:      ClassHTTP,
			Source:     "gregale.yaml: functions." + p.Spec.Name,
			Tier:       TierCompose,
			DetectedBy: Detection{Detector: detFunctions.String(), Priority: detFunctions.priority()},
			Function:   &FunctionWorkload{Runtime: p.Runtime, Handler: p.Handler},
		}
		for _, trigger := range m.FunctionCronTriggers(p).Triggers {
			if trigger.Kind != gregalemanifest.TriggerKindCron {
				continue
			}
			w.Schedules = append(w.Schedules, CronSchedule{Expression: trigger.Schedule, Enabled: true})
		}
		if len(w.Schedules) > 0 {
			w.Schedule = w.Schedules[0].Expression
		}
		out = append(out, w)
	}
	return out, true, nil
}

// functionRootDir maps a project-relative function root onto RootDir,
// where "" is the repository root.
func functionRootDir(root string) string {
	if root == "." {
		return ""
	}
	return root
}

// scanFunctionsProject is the whole scan of a functions project: its
// function workloads, hashed and sorted like any scan. ok is false when the
// repository is not one, and the regular detectors run instead.
func scanFunctionsProject(fsys fs.FS) (Result, bool, error) {
	workloads, ok, err := detectFunctionsProject(fsys)
	if err != nil || !ok {
		return Result{}, ok, err
	}
	sourceTrees := make(map[string]string)
	for i := range workloads {
		digest, err := hashWorkloadSourceWithCache(fsys, workloads[i], sourceTrees)
		if err != nil {
			return Result{}, false, fmt.Errorf("reposcan: hash function %q source: %w", workloads[i].Name, err)
		}
		workloads[i].SourceSHA256 = digest
	}
	sortStableByName(workloads)
	return Result{Workloads: workloads, Tier: TierCompose}, true, nil
}
