package ci

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const ArtifactInputsDirectory = ".gregale-ci-inputs"

var ErrArtifactInput = errors.New("CI dependency artifact input unavailable")

// ArtifactInput names one literal artifact from a direct dependency. Each input
// becomes a regular file at .gregale-ci-inputs/NAME in the consuming guest.
type ArtifactInput struct {
	Name string `json:"name" yaml:"name"`
	Job  string `json:"job" yaml:"job"`
	Path string `json:"path" yaml:"path"`
}

// JobsInput pins an input to a verified producer attempt, digest and byte count.
// Storage keys, credentials and provider locations never enter this descriptor.
type JobsInput struct {
	ArtifactInput
	AttemptID string `json:"attempt_id"`
	Digest    string `json:"digest"`
	Bytes     int64  `json:"bytes"`
}

func validInputDeclaration(job string, needs []string, input ArtifactInput) bool {
	return ValidName(input.Name) && ValidName(input.Job) && input.Job != job && slices.Contains(needs, input.Job) &&
		validArtifact(input.Path) && !strings.ContainsAny(input.Path, "*?[")
}

func normalizeArtifactInputs(name string, job *Job, jobs map[string]Job) error {
	if len(job.Inputs) > api.CIMaxArtifactInputsPerJob {
		return fmt.Errorf("job %q has too many artifact inputs", name)
	}
	seen := make(map[string]bool)
	for _, input := range job.Inputs {
		if !validInputDeclaration(name, job.Needs, input) || seen[input.Name] {
			return fmt.Errorf("job %q has invalid or duplicate artifact inputs", name)
		}
		selected := false
		for _, pattern := range jobs[input.Job].Artifacts {
			selected = selected || artifactMatches(pattern, input.Path)
		}
		if !selected {
			return fmt.Errorf("job %q input is outside its dependency's artifact selectors", name)
		}
		seen[input.Name] = true
	}
	sort.Slice(job.Inputs, func(i, j int) bool { return job.Inputs[i].Name < job.Inputs[j].Name })
	return nil
}

func resolveArtifactInputs(row Execution, job PlannedJob) ([]JobsInput, error) {
	var out []JobsInput
	var total int64
	for _, input := range job.Inputs {
		found := false
		for _, producer := range row.Jobs {
			if producer.Name != input.Job || producer.Status != StatusSucceeded || producer.Attempt == nil || producer.Attempt.Status != StatusSucceeded || producer.Attempt.OutputIncomplete {
				continue
			}
			for _, artifact := range producer.Attempt.Artifacts {
				if artifact.Path != input.Path {
					continue
				}
				if !artifactDigestPattern.MatchString(artifact.Digest) || artifact.Bytes < 0 || artifact.Bytes > api.CIArtifactMaxBytes || !ValidID(producer.Attempt.ID) {
					return nil, ErrArtifactInput
				}
				total += artifact.Bytes
				if total > api.CIArtifactInputsMaxBytesPerJob {
					return nil, ErrArtifactInput
				}
				out = append(out, JobsInput{ArtifactInput: input, AttemptID: producer.Attempt.ID, Digest: artifact.Digest, Bytes: artifact.Bytes})
				found = true
				break
			}
			break
		}
		if !found {
			return nil, ErrArtifactInput
		}
	}
	return out, nil
}

func validateJobsInputs(m JobsManifest) error {
	if m.Version != jobsManifestVersion(m.Job.Job) {
		return ErrBackendProtocol
	}
	if len(m.Job.Inputs) == 0 {
		if len(m.Inputs) != 0 {
			return ErrBackendProtocol
		}
		return nil
	}
	if len(m.Job.Inputs) > api.CIMaxArtifactInputsPerJob || len(m.Inputs) != len(m.Job.Inputs) {
		return ErrBackendProtocol
	}
	seen := make(map[string]bool)
	var total int64
	for i, input := range m.Inputs {
		if input.ArtifactInput != m.Job.Inputs[i] || !validInputDeclaration(m.Job.Name, m.Job.Needs, input.ArtifactInput) || seen[input.Name] || !ValidID(input.AttemptID) || input.AttemptID == m.AttemptID || !artifactDigestPattern.MatchString(input.Digest) || input.Bytes < 0 || input.Bytes > api.CIArtifactMaxBytes {
			return ErrBackendProtocol
		}
		seen[input.Name] = true
		total += input.Bytes
	}
	if total > api.CIArtifactInputsMaxBytesPerJob {
		return ErrBackendProtocol
	}
	return nil
}

// An ErrArtifactInput returned from Start certifies that preflight failed before
// inserting a native run. The controller saves this result with its own fence;
// the Jobs output journal keeps its invariant requiring a real run.
func unavailableArtifactInputs(job PlannedJob) Observation {
	code := 125
	out := Observation{Status: StatusFailed, ExitCode: &code, Logs: []byte("CI dependency artifact inputs are unavailable; no guest commands were started.\n")}
	for _, step := range job.Steps {
		out.Steps = append(out.Steps, StepResult{Name: step.Name, Status: StatusSkipped})
	}
	return out
}
