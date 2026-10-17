// Package ci defines Gregale's internal CI control plane, execution ledger and
// dormant Jobs intent adapter. Customer commands and VMs belong to compute.
package ci

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"gopkg.in/yaml.v3"
)

var (
	namePattern  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
	repoPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$`)
	shaPattern   = regexp.MustCompile(`^[0-9a-f]{40}$`)
	imagePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:/-]*@sha256:[0-9a-f]{64}$`)
)

// Definition is intentionally narrower than Actions YAML: no expressions,
// secrets, inherited environment, remote actions, or implicit deployment.
type Definition struct {
	Version    int            `json:"version" yaml:"version"`
	Repository string         `json:"repository" yaml:"repository"`
	Jobs       map[string]Job `json:"jobs" yaml:"jobs"`
}

type Job struct {
	Matrix           map[string]MatrixVariant `json:"matrix,omitempty" yaml:"matrix,omitempty"`
	Env              map[string]string        `json:"env,omitempty" yaml:"env,omitempty"`
	WorkingDirectory string                   `json:"working_directory,omitempty" yaml:"working_directory,omitempty"`
	Inputs           []ArtifactInput          `json:"inputs,omitempty" yaml:"inputs,omitempty"`
	Image            string                   `json:"image" yaml:"image"`
	Needs            []string                 `json:"needs,omitempty" yaml:"needs,omitempty"`
	TimeoutSeconds   int                      `json:"timeout_seconds" yaml:"timeout_seconds"`
	Steps            []Step                   `json:"steps" yaml:"steps"`
	Artifacts        []string                 `json:"artifacts,omitempty" yaml:"artifacts,omitempty"`
	JUnit            []string                 `json:"junit,omitempty" yaml:"junit,omitempty"`
}

type Step struct {
	Name             string            `json:"name" yaml:"name"`
	Run              string            `json:"run" yaml:"run"`
	Env              map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	WorkingDirectory string            `json:"working_directory,omitempty" yaml:"working_directory,omitempty"`
}

// Plan is a deterministic, immutable description of future compute work.
// Planning never fetches the repository, image, dependencies, or artifacts.
type Plan struct {
	Version          int          `json:"version"`
	Repository       string       `json:"repository"`
	DefinitionDigest string       `json:"definition_digest"`
	Jobs             []PlannedJob `json:"jobs"`
}

type PlannedJob struct {
	Name string `json:"name"`
	Job
}

// Parse accepts strict YAML (including JSON), rejects ambiguous keys and aliases,
// and returns the normalized definition used by both the CLI and API.
func Parse(data []byte) (Definition, error) {
	if len(data) == 0 || len(data) > api.CIDefinitionMaxBytes || !utf8.Valid(data) {
		return Definition{}, fmt.Errorf("CI definition must contain 1-%d UTF-8 bytes", api.CIDefinitionMaxBytes)
	}
	var node yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&node); err != nil {
		return Definition{}, fmt.Errorf("decode CI definition: %w", err)
	}
	if err := uniqueKeys(&node); err != nil {
		return Definition{}, err
	}
	if err := stringConfigurationNodes(&node); err != nil {
		return Definition{}, err
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return Definition{}, fmt.Errorf("CI definition must contain one document")
	}
	dec = yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var def Definition
	if err := dec.Decode(&def); err != nil {
		return Definition{}, fmt.Errorf("decode CI definition: %w", err)
	}
	return Normalize(def)
}

func uniqueKeys(node *yaml.Node) error {
	if node.Kind == yaml.AliasNode || node.Anchor != "" || node.Tag == "!!merge" {
		return fmt.Errorf("CI definitions cannot contain YAML anchors, aliases or merge keys")
	}
	if node.Kind == yaml.MappingNode {
		seen := make(map[string]bool)
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || seen[key.Value] {
				return fmt.Errorf("CI definition contains a duplicate or non-string key")
			}
			seen[key.Value] = true
		}
	}
	for _, child := range node.Content {
		if err := uniqueKeys(child); err != nil {
			return err
		}
	}
	return nil
}

func Normalize(def Definition) (Definition, error) {
	if def.Version != 1 || !repoPattern.MatchString(def.Repository) || len(def.Repository) > api.CIRepositoryMaxBytes {
		return Definition{}, fmt.Errorf("CI requires version 1 and a repository in OWNER/NAME form")
	}
	if len(def.Jobs) == 0 || len(def.Jobs) > api.CIMaxJobs {
		return Definition{}, fmt.Errorf("CI requires 1-%d jobs", api.CIMaxJobs)
	}
	// Copy before applying defaults or sorting so caller-owned definitions stay immutable.
	out := Definition{Version: def.Version, Repository: def.Repository, Jobs: make(map[string]Job, len(def.Jobs))}
	for name, job := range def.Jobs {
		job = cloneJob(job)
		if err := normalizeJobBody(name, &job); err != nil {
			return Definition{}, err
		}
		out.Jobs[name] = job
	}
	if _, err := expandJobs(out.Jobs); err != nil {
		return Definition{}, err
	}
	raw, err := json.Marshal(out)
	if err != nil || len(raw) > api.CIDefinitionMaxBytes {
		return Definition{}, fmt.Errorf("normalized CI definition exceeds %d bytes", api.CIDefinitionMaxBytes)
	}
	return out, nil
}

func normalizeJob(name string, job *Job, jobs map[string]Job) error {
	if err := normalizeJobBody(name, job); err != nil {
		return err
	}
	seen := make(map[string]bool)
	for _, dependency := range job.Needs {
		if _, ok := jobs[dependency]; !ok || dependency == name || seen[dependency] {
			return fmt.Errorf("job %q has invalid or duplicate dependency %q", name, dependency)
		}
		seen[dependency] = true
	}
	return normalizeArtifactInputs(name, job, jobs)
}

func normalizeJobBody(name string, job *Job) error {
	if !ValidName(name) || !imagePattern.MatchString(job.Image) || len(job.Image) > api.CIImageMaxBytes || strings.Contains(job.Image, "://") {
		return fmt.Errorf("job %q requires a valid name and a digest-pinned OCI image", name)
	}
	if job.TimeoutSeconds == 0 {
		job.TimeoutSeconds = api.CIDefaultTimeoutSeconds
	}
	if job.TimeoutSeconds < 1 || job.TimeoutSeconds > api.CIMaxTimeoutSeconds || len(job.Steps) == 0 || len(job.Steps) > api.CIMaxStepsPerJob {
		return fmt.Errorf("job %q has invalid timeout or step count", name)
	}
	if err := validateJobConfiguration(*job); err != nil {
		return fmt.Errorf("job %q: %w", name, err)
	}
	seen := make(map[string]bool)
	for _, step := range job.Steps {
		if !ValidName(step.Name) || seen[step.Name] || strings.TrimSpace(step.Run) == "" || len(step.Run) > api.CIMaxCommandBytes || !utf8.ValidString(step.Run) || strings.ContainsRune(step.Run, 0) {
			return fmt.Errorf("job %q has invalid or duplicate steps", name)
		}
		seen[step.Name] = true
	}
	if len(job.Artifacts) > api.CIMaxArtifactsPerJob {
		return fmt.Errorf("job %q has too many artifact paths", name)
	}
	seen = make(map[string]bool)
	for _, artifact := range job.Artifacts {
		_, patternErr := path.Match(artifact, "validation")
		if !validArtifact(artifact) || seen[artifact] || patternErr != nil {
			return fmt.Errorf("job %q has invalid or duplicate artifact paths", name)
		}
		seen[artifact] = true
	}
	sort.Strings(job.Needs)
	sort.Strings(job.Artifacts)
	sort.Slice(job.Inputs, func(i, j int) bool { return job.Inputs[i].Name < job.Inputs[j].Name })
	return normalizeJUnit(job)
}

func validArtifact(value string) bool {
	if value == "" || len(value) > api.CIArtifactPathMaxBytes || !utf8.ValidString(value) || path.IsAbs(value) || strings.ContainsAny(value, "\\\x00\r\n") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func ValidName(name string) bool     { return namePattern.MatchString(name) }
func ValidCommitSHA(sha string) bool { return shaPattern.MatchString(sha) }

func jobOrder(jobs map[string]Job) ([]string, error) {
	names := make([]string, 0, len(jobs))
	for name := range jobs {
		names = append(names, name)
	}
	sort.Strings(names)
	order := make([]string, 0, len(jobs))
	visited := make(map[string]int)
	var visit func(string) error
	visit = func(name string) error {
		if visited[name] == 1 {
			return fmt.Errorf("CI job dependencies contain a cycle")
		}
		if visited[name] == 2 {
			return nil
		}
		visited[name] = 1
		for _, dependency := range jobs[name].Needs {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		visited[name] = 2
		order = append(order, name)
		return nil
	}
	for _, name := range names {
		if err := visit(name); err != nil {
			return nil, err
		}
	}
	return order, nil
}

func Compile(def Definition) (Plan, []byte, error) {
	def, err := Normalize(def)
	if err != nil {
		return Plan{}, nil, err
	}
	canonical, err := json.Marshal(def)
	if err != nil {
		return Plan{}, nil, fmt.Errorf("encode CI definition: %w", err)
	}
	digest := sha256.Sum256(canonical)
	jobs, err := expandJobs(def.Jobs)
	if err != nil {
		return Plan{}, nil, err
	}
	plan := Plan{Version: 1, Repository: def.Repository, DefinitionDigest: "sha256:" + hex.EncodeToString(digest[:]), Jobs: make([]PlannedJob, 0, len(jobs))}
	order, err := jobOrder(jobs)
	if err != nil {
		return Plan{}, nil, err
	}
	for _, name := range order {
		plan.Jobs = append(plan.Jobs, PlannedJob{Name: name, Job: jobs[name]})
	}
	return plan, canonical, nil
}
