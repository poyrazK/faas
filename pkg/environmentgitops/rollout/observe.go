// Package rollout waits for recorded serving proof without performing work.
// adr: 1552
package rollout

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops/diagnostics"
)

type Result struct {
	ReadOnly           bool                              `json:"read_only"`
	Status             string                            `json:"status"`
	Reason             string                            `json:"reason"`
	RequestedCommitSHA string                            `json:"requested_commit_sha"`
	ObservedAt         time.Time                         `json:"observed_at,omitempty"`
	SourceID           string                            `json:"source_id,omitempty"`
	RevisionID         string                            `json:"revision_id,omitempty"`
	GraphID            string                            `json:"graph_id,omitempty"`
	CommitSHA          string                            `json:"commit_sha,omitempty"`
	DefinitionDigest   string                            `json:"definition_digest,omitempty"`
	Generation         int64                             `json:"generation"`
	IntentVersion      int64                             `json:"intent_version"`
	Explanation        *api.EnvironmentGitOpsExplanation `json:"explanation,omitempty"`
}

func (r Result) ExitCode() int {
	switch r.Status {
	case "serving":
		return 0
	case "failed", "blocked":
		return 2
	case "timed_out":
		return 3
	case "superseded":
		return 4
	case "interrupted":
		return 130
	default:
		return 1
	}
}

type Tracker struct {
	commit, environment string
	source              *api.EnvironmentGitSource
	revision, digest    string
	generation          int64
}

func NewTracker(commit, environment string) (*Tracker, error) {
	if !validHex(commit, 40) || !api.ValidProjectEnvironmentSlug(environment) {
		return nil, fmt.Errorf("wait requires an environment and full lowercase Git commit SHA")
	}
	return &Tracker{commit: commit, environment: environment}, nil
}

func validHex(value string, length int) bool {
	_, err := hex.DecodeString(value)
	return err == nil && len(value) == length && strings.ToLower(value) == value
}

// Observe never uses source_commit_sha, applied_revision_id, or a supplied
// explanation as proof. Only the approved revision in the current graph counts.
func (t *Tracker) Observe(status api.EnvironmentGitOpsStatusResponse, now time.Time) (Result, error) {
	s := status.Source
	out := Result{ReadOnly: true, Status: "waiting", Reason: "requested_revision_unproven", RequestedCommitSHA: t.commit, ObservedAt: now.UTC(),
		SourceID: s.ID, Generation: s.Generation, IntentVersion: s.IntentVersion}
	if s.ID == "" || s.AccountID == "" || s.ProjectID == "" || s.EnvironmentID == "" || s.EnvironmentSlug != t.environment || s.Generation < 0 || s.IntentVersion < 0 || s.Spec.Validate() != nil {
		return out, fmt.Errorf("GitOps status scope or source binding is invalid")
	}
	if t.source == nil {
		copy := s
		t.source = &copy
	} else if s.ID != t.source.ID || s.AccountID != t.source.AccountID || s.ProjectID != t.source.ProjectID || s.EnvironmentID != t.source.EnvironmentID || s.Spec != t.source.Spec {
		out.Status, out.Reason = "superseded", "source_binding_changed"
		return out, nil
	}
	if t.revision != "" && (s.ApprovedRevisionID != t.revision || s.Generation != t.generation) {
		out.Status, out.Reason = "superseded", "approved_revision_changed"
		return out, nil
	}
	explanation := diagnostics.Assess(status, now)
	out.Explanation = &explanation
	if s.Detached || s.Suspended || s.Spec.Mode != "enforce" {
		out.Status, out.Reason = "blocked", "reconciliation_inactive"
		return out, nil
	}
	e := status.WorkloadEvidence
	if e == nil || e.GraphID == "" || e.SourceID != s.ID || e.EnvironmentID != s.EnvironmentID || e.RevisionID == "" || e.RevisionID != s.ApprovedRevisionID ||
		e.Generation != s.Generation || e.IntentVersion != s.IntentVersion || !validHex(e.PlanHash, 64) || !validHex(e.DefinitionDigest, 64) || !validHex(e.CommitSHA, 40) {
		return out, nil
	}
	if e.CommitSHA != t.commit {
		return out, nil
	}
	if t.revision == "" {
		t.revision, t.generation, t.digest = e.RevisionID, e.Generation, e.DefinitionDigest
	} else if e.DefinitionDigest != t.digest {
		return out, fmt.Errorf("approved revision definition identity changed")
	}
	out.Status, out.Reason = "progressing", "serving_evidence_pending"
	out.RevisionID, out.GraphID, out.CommitSHA, out.DefinitionDigest = e.RevisionID, e.GraphID, e.CommitSHA, e.DefinitionDigest
	if e.GraphPhase == "failed" || e.GraphErrorCode != "" || failedWorkload(e.Workloads) {
		out.Status, out.Reason = "failed", "current_workload_failed"
		return out, nil
	}
	if e.GraphPhase == "prepared" && e.ArtifactsPrepared && e.Qualified && e.Activated && e.Serving && len(e.BlockingReasons) == 0 &&
		e.Candidates >= 0 && e.RetainedWorkloadsRecorded >= 0 && e.Candidates+e.RetainedWorkloadsRecorded == len(e.Workloads) &&
		explanation.ServingRevisionID == t.revision && explanation.SourceFreshness == "fresh" && s.SourceErrorCode == "" && completeWorkloads(e.Workloads) {
		out.Status, out.Reason = "serving", "requested_commit_serving"
	}
	return out, nil
}

func failedWorkload(workloads []api.EnvironmentWorkloadProgress) bool {
	for _, w := range workloads {
		if w.Preparation == "failed" || w.Qualification == "failed" || w.Activation == "failed" || w.Serving == "failed" || w.BuildStatus == "failed" || w.BuildStatus == "cancelled" {
			return true
		}
	}
	return false
}

func completeWorkloads(workloads []api.EnvironmentWorkloadProgress) bool {
	if len(workloads) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, w := range workloads {
		name, ok := strings.CutPrefix(w.Resource, "workload/")
		if !ok || !api.ValidAppSlug(name) || seen[w.Resource] || w.AppID == "" || len(w.BlockingReasons) != 0 ||
			w.Preparation != "complete" || w.Qualification != "complete" || w.Activation != "complete" || w.Serving != "complete" {
			return false
		}
		switch w.ExecutionMode {
		case api.ExecutionModeRequest, api.ExecutionModeService, api.ExecutionModeWorker, api.ExecutionModeJob:
		default:
			return false
		}
		seen[w.Resource] = true
	}
	return true
}
