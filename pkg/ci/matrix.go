package ci

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// MatrixVariant is a named, literal override. Expansion happens during planning;
// no matrix expressions, axes or unresolved declarations enter a guest command.
type MatrixVariant struct {
	Image string            `json:"image,omitempty" yaml:"image,omitempty"`
	Env   map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
}

func cloneJob(job Job) Job {
	job.Needs, job.Steps, job.Artifacts = slices.Clone(job.Needs), slices.Clone(job.Steps), slices.Clone(job.Artifacts)
	job.JUnit, job.Inputs, job.Env = slices.Clone(job.JUnit), slices.Clone(job.Inputs), maps.Clone(job.Env)
	for i := range job.Steps {
		job.Steps[i].Env = maps.Clone(job.Steps[i].Env)
	}
	job.Matrix = maps.Clone(job.Matrix)
	for name, variant := range job.Matrix {
		variant.Env = maps.Clone(variant.Env)
		job.Matrix[name] = variant
	}
	return job
}

func expandJobs(jobs map[string]Job) (map[string]Job, error) {
	targets, err := matrixTargets(jobs)
	if err != nil {
		return nil, err
	}
	expanded := make(map[string]Job)
	for name, job := range jobs {
		for _, target := range targets[name] {
			child := cloneJob(job)
			child.Matrix = nil
			if job.Matrix != nil {
				variant := job.Matrix[strings.TrimPrefix(target, name+"-")]
				if variant.Image != "" {
					child.Image = variant.Image
				}
				if !validEnvironment(variant.Env) {
					return nil, fmt.Errorf("matrix job %q has an invalid variant environment", name)
				}
				if len(variant.Env) > 0 {
					if child.Env == nil {
						child.Env = make(map[string]string)
					}
					maps.Copy(child.Env, variant.Env)
				}
			}
			child.Needs, err = expandNeeds(job.Needs, targets)
			if err != nil {
				return nil, fmt.Errorf("job %q: %w", name, err)
			}
			expanded[target] = child
		}
	}
	for name, job := range expanded {
		if err := normalizeJob(name, &job, expanded); err != nil {
			return nil, err
		}
		expanded[name] = job
	}
	if _, err := jobOrder(expanded); err != nil {
		return nil, err
	}
	return expanded, nil
}

func matrixTargets(jobs map[string]Job) (map[string][]string, error) {
	targets := make(map[string][]string)
	seen := make(map[string]bool)
	for name, job := range jobs {
		if job.Matrix == nil {
			targets[name] = []string{name}
			seen[name] = true
			continue
		}
		if len(job.Matrix) == 0 {
			return nil, fmt.Errorf("job %q requires 1-%d matrix variants", name, api.CIMaxMatrixVariantsPerJob)
		}
		if len(job.Matrix) > api.CIMaxMatrixVariantsPerJob {
			return nil, matrixLimit("matrix_variants", api.CIMaxMatrixVariantsPerJob, len(job.Matrix))
		}
		for variant := range job.Matrix {
			target := name + "-" + variant
			_, declared := jobs[target]
			if !ValidName(variant) || !ValidName(target) || declared || seen[target] {
				return nil, fmt.Errorf("job %q has an invalid or colliding matrix variant", name)
			}
			seen[target] = true
			targets[name] = append(targets[name], target)
		}
		sort.Strings(targets[name])
	}
	if len(seen) > api.CIMaxJobs {
		return nil, matrixLimit("expanded_jobs", api.CIMaxJobs, len(seen))
	}
	return targets, nil
}

func matrixLimit(resource string, limit, observed int) error {
	return fmt.Errorf("%w: limit=%d, observed=%d; see /docs/ops/internal-ci",
		&LimitError{Resource: resource, Limit: limit, Observed: observed}, limit, observed)
}

func expandNeeds(needs []string, targets map[string][]string) ([]string, error) {
	var out []string
	seen := make(map[string]bool)
	for _, dependency := range needs {
		children := targets[dependency]
		if children == nil {
			for _, names := range targets {
				if slices.Contains(names, dependency) {
					children = []string{dependency}
					break
				}
			}
		}
		if len(children) == 0 {
			return nil, fmt.Errorf("unknown dependency %q", dependency)
		}
		for _, child := range children {
			if seen[child] {
				return nil, fmt.Errorf("overlapping or duplicate dependency %q", dependency)
			}
			seen[child] = true
			out = append(out, child)
		}
	}
	sort.Strings(out)
	return out, nil
}
