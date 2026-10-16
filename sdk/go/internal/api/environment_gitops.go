package api

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"
)

// CreateEnvironmentGitSourceRequest selects a definition in the project's
// verified repository. GitHub identity comes from the project installation;
// GitLab identity is verified against the account OAuth connection.
type CreateEnvironmentGitSourceRequest struct {
	Provider       string `json:"provider,omitempty"`
	Repository     string `json:"repository,omitempty"`
	Ref            string `json:"ref,omitempty"`
	ManifestPath   string `json:"manifest_path"`
	Mode           string `json:"mode,omitempty"`
	ApprovalPolicy string `json:"approval_policy,omitempty"`
	Prune          bool   `json:"prune"`
}

type PreviewEnvironmentGitRevisionRequest struct {
	CommitSHA string `json:"commit_sha"`
}

type PreviewEnvironmentGitRevisionResponse struct {
	CommitSHA        string          `json:"commit_sha"`
	DefinitionDigest string          `json:"definition_digest"`
	Definition       json.RawMessage `json:"definition"`
	Generation       int64           `json:"generation"`
}

type ApproveEnvironmentGitRevisionRequest struct {
	CommitSHA          string `json:"commit_sha"`
	DefinitionDigest   string `json:"definition_digest"`
	ExpectedGeneration int64  `json:"expected_generation"`
}

type ApproveEnvironmentGitRevisionResponse struct {
	Source   EnvironmentGitSource       `json:"source"`
	Revision EnvironmentDesiredRevision `json:"revision"`
}

type EnvironmentGitOpsStatusResponse struct {
	WorkloadRetirements []EnvironmentWorkloadRetirement `json:"workload_retirements,omitempty"`
	Source              EnvironmentGitSource            `json:"source"`
	Runs                []EnvironmentGitOpsRun          `json:"runs"`
}

type AdoptEnvironmentGitOpsRequest struct {
	PlanHash string `json:"plan_hash"`
}

type AdoptEnvironmentGitOpsResponse struct {
	Status string `json:"status"`
}

type RemoveEnvironmentGitOpsOverrideRequest struct {
	Resource string `json:"resource"`
	Path     string `json:"path"`
}

type EnvironmentGitSourceSpec struct {
	Provider       string `json:"provider,omitempty"`
	RepositoryID   int64  `json:"repository_id"`
	InstallationID int64  `json:"installation_id"`
	Repository     string `json:"repository"`
	Ref            string `json:"ref"`
	ManifestPath   string `json:"manifest_path"`
	Mode           string `json:"mode"`
	ApprovalPolicy string `json:"approval_policy"`
	Prune          bool   `json:"prune"`
}

type EnvironmentGitSource struct {
	ID                     string                   `json:"id"`
	AccountID              string                   `json:"account_id"`
	ProjectID              string                   `json:"project_id"`
	EnvironmentID          string                   `json:"environment_id"`
	EnvironmentSlug        string                   `json:"environment"`
	Spec                   EnvironmentGitSourceSpec `json:"source"`
	Detached               bool                     `json:"detached"`
	Suspended              bool                     `json:"suspended"`
	Generation             int64                    `json:"generation"`
	IntentVersion          int64                    `json:"intent_version"`
	ApprovedRevisionID     string                   `json:"approved_revision_id,omitempty"`
	AppliedRevisionID      string                   `json:"applied_revision_id,omitempty"`
	SourceCheckedAt        *time.Time               `json:"source_checked_at,omitempty"`
	SourceErrorCode        string                   `json:"source_error_code,omitempty"`
	SourceCommitSHA        string                   `json:"source_commit_sha,omitempty"`
	SourceDefinitionDigest string                   `json:"source_definition_digest,omitempty"`
	SourceVerifiedAt       *time.Time               `json:"source_verified_at,omitempty"`
	CreatedAt              time.Time                `json:"created_at"`
	UpdatedAt              time.Time                `json:"updated_at"`
}

type EnvironmentDesiredRevision struct {
	ID         string          `json:"id"`
	SourceID   string          `json:"source_id"`
	CommitSHA  string          `json:"commit_sha"`
	Digest     string          `json:"definition_digest"`
	Definition json.RawMessage `json:"definition"`
	ApprovedBy string          `json:"approved_by"`
	ApprovedAt time.Time       `json:"approved_at"`
}

type EnvironmentGitOpsRun struct {
	ID          string          `json:"id"`
	SourceID    string          `json:"source_id"`
	RevisionID  string          `json:"revision_id"`
	Generation  int64           `json:"generation"`
	Status      string          `json:"status"`
	Plan        json.RawMessage `json:"plan"`
	Steps       json.RawMessage `json:"steps"`
	ErrorCode   string          `json:"error_code,omitempty"`
	StartedAt   time.Time       `json:"started_at"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
}

type EnvironmentGitSourceUpdate struct {
	ExpectedGeneration int64  `json:"expected_generation"`
	Mode               string `json:"mode,omitempty"`
	Prune              *bool  `json:"prune,omitempty"`
	Suspended          *bool  `json:"suspended,omitempty"`
}

type EnvironmentGitOpsOverrideRequest struct {
	Resource  string    `json:"resource"`
	Path      string    `json:"path"`
	Reason    string    `json:"reason"`
	ExpiresAt time.Time `json:"expires_at"`
}

type EnvironmentGitOpsChange struct {
	Resource string          `json:"resource"`
	Path     string          `json:"path"`
	Action   string          `json:"action"`
	Before   json.RawMessage `json:"before,omitempty"`
	After    json.RawMessage `json:"after,omitempty"`
	Reason   string          `json:"reason,omitempty"`
}

type EnvironmentGitOpsPlan struct {
	RetirementImpacts []EnvironmentWorkloadRetirementImpact `json:"retirement_impacts,omitempty"`
	Manager           string                                `json:"manager"`
	Revision          string                                `json:"revision"`
	CommitSHA         string                                `json:"commit_sha,omitempty"`
	Generation        int64                                 `json:"generation"`
	DesiredDigest     string                                `json:"desired_digest"`
	ObservedVersion   int64                                 `json:"observed_version"`
	Hash              string                                `json:"plan_hash"`
	Changes           []EnvironmentGitOpsChange             `json:"changes"`
	BlockingReasons   []string                              `json:"blocking_reasons"`
}

func (p EnvironmentGitOpsPlan) CanApply() bool { return len(p.BlockingReasons) == 0 }

// HasDrift includes deferred removals and temporary overrides. An environment
// under override must not be described as fully converged to its Git revision.
func (p EnvironmentGitOpsPlan) HasDrift() bool {
	for _, change := range p.Changes {
		if change.Action != "keep" && change.Action != "retain_unmanaged" {
			return true
		}
	}
	return false
}

func (s EnvironmentGitSourceSpec) Validate() error {
	provider := s.Provider
	if provider == "" {
		provider = "github"
	}
	parts := strings.Split(s.Repository, "/")
	if s.RepositoryID <= 0 || provider == "github" && (s.InstallationID <= 0 || len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.ContainsAny(s.Repository, " \t\r\n")) ||
		provider == "gitlab" && (s.InstallationID != 0 || !validGitLabRepositoryPath(s.Repository)) || provider != "github" && provider != "gitlab" {
		return fmt.Errorf("Git source requires a verified repository and installation identity")
	}
	if !validEnvironmentGitRef(s.Ref) || s.ApprovalPolicy == "protected_branch" && !strings.HasPrefix(s.Ref, "refs/heads/") {
		return fmt.Errorf("Git source requires a valid ref")
	}
	if s.ManifestPath == "" || path.IsAbs(s.ManifestPath) || path.Clean(s.ManifestPath) != s.ManifestPath || s.ManifestPath == "." || strings.HasPrefix(s.ManifestPath, "../") || s.ManifestPath == ".." || strings.ContainsAny(s.ManifestPath, "\\\x00") {
		return fmt.Errorf("manifest_path must be a normalized file path within the Git tree")
	}
	if s.Mode != "report" && s.Mode != "enforce" || s.ApprovalPolicy != "manual" && s.ApprovalPolicy != "protected_branch" {
		return fmt.Errorf("Git source needs report/enforce mode and manual/protected_branch approval_policy")
	}
	if provider == "gitlab" && (s.Mode != "report" || s.ApprovalPolicy != "manual" || s.Prune) {
		return fmt.Errorf("GitLab source currently supports report mode, manual approval, and retained resources")
	}
	return nil
}

func validGitLabRepositoryPath(repository string) bool {
	if len(repository) > 512 || len(repository) == 0 || strings.TrimSpace(repository) != repository {
		return false
	}
	parts := strings.Split(repository, "/")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".git") {
			return false
		}
		for _, char := range part {
			if char != '-' && char != '_' && char != '.' && (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') {
				return false
			}
		}
	}
	return true
}

func validEnvironmentGitRef(ref string) bool {
	if (!strings.HasPrefix(ref, "refs/heads/") && !strings.HasPrefix(ref, "refs/tags/")) || strings.ContainsAny(ref, " \t\r\n\x00~^:?*[\\") || strings.Contains(ref, "..") || strings.Contains(ref, "@{") {
		return false
	}
	for _, component := range strings.Split(ref, "/") {
		if component == "" || strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".") || strings.HasSuffix(component, ".lock") {
			return false
		}
	}
	for _, char := range ref {
		if char < 0x20 || char == 0x7f {
			return false
		}
	}
	return true
}

// Rebinding retires the old immutable binding and releases its field ownership.
// Existing values and execution evidence are retained; fresh approval is required.
type RebindEnvironmentGitSourceRequest struct {
	ExpectedGeneration int64  `json:"expected_generation"`
	Ref                string `json:"ref"`
	ManifestPath       string `json:"manifest_path"`
	ApprovalPolicy     string `json:"approval_policy"`
}
type DetachEnvironmentGitSourceRequest struct {
	ExpectedGeneration int64 `json:"expected_generation"`
}
type EnvironmentFieldOwnershipRequest struct {
	App         string   `json:"app,omitempty"`
	Project     string   `json:"project,omitempty"`
	Environment string   `json:"environment"`
	Paths       []string `json:"paths"`
	Manager     string   `json:"manager"`
}
type EnvironmentFieldOwnershipResponse struct {
	Status string `json:"status"`
}

// EnvironmentWorkloadRetirementImpact is a review-time inventory. Work counts
// are informational and can grow before cutover; identities and retained
// configuration remain bound by the plan's normal freshness hash.
type EnvironmentWorkloadRetirementImpact struct {
	Resource            string          `json:"resource"`
	AppID               string          `json:"app_id"`
	Routes              []DeclaredRoute `json:"routes,omitempty"`
	QueueBindingIDs     []string        `json:"queue_binding_ids,omitempty"`
	ConsumerIDs         []string        `json:"consumer_ids,omitempty"`
	JobID               string          `json:"job_id,omitempty"`
	PendingInvocations  int64           `json:"pending_invocations"`
	InFlightInvocations int64           `json:"in_flight_invocations"`
	QueuedTasks         int64           `json:"queued_tasks"`
	InFlightTasks       int64           `json:"in_flight_tasks"`
}
