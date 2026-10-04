package api

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"
)

// CreateEnvironmentGitSourceRequest selects a definition in the project's
// verified repository. Repository and installation identities come from the
// server's GitHub connection, never from this request.
type CreateEnvironmentGitSourceRequest struct {
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
	Source EnvironmentGitSource   `json:"source"`
	Runs   []EnvironmentGitOpsRun `json:"runs"`
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
	Manager         string                    `json:"manager"`
	Revision        string                    `json:"revision"`
	CommitSHA       string                    `json:"commit_sha,omitempty"`
	Generation      int64                     `json:"generation"`
	DesiredDigest   string                    `json:"desired_digest"`
	ObservedVersion int64                     `json:"observed_version"`
	Hash            string                    `json:"plan_hash"`
	Changes         []EnvironmentGitOpsChange `json:"changes"`
	BlockingReasons []string                  `json:"blocking_reasons"`
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
	parts := strings.Split(s.Repository, "/")
	if s.RepositoryID <= 0 || s.InstallationID <= 0 || len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.ContainsAny(s.Repository, " \t\r\n") {
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
	return nil
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
