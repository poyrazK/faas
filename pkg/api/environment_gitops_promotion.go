package api

import "time"

// A promotion plan is a read-only proposal. Its hash is never approval or
// execution authority. ExpectedPlanHash rechecks an earlier observation.
type PreviewEnvironmentGitPromotionRequest struct {
	FromEnvironment  string `json:"from_environment"`
	TargetCommitSHA  string `json:"target_commit_sha"`
	ExpectedPlanHash string `json:"expected_plan_hash,omitempty"`
}

type EnvironmentGitPromotionOrigin struct {
	SourceID         string `json:"source_id"`
	RevisionID       string `json:"revision_id"`
	CommitSHA        string `json:"commit_sha"`
	DefinitionDigest string `json:"definition_digest"`
	Generation       int64  `json:"generation"`
	IntentVersion    int64  `json:"intent_version"`
	GraphID          string `json:"graph_id"`
	ReleaseSetID     string `json:"release_set_id"`
	Provider         string `json:"provider"`
	RepositoryID     int64  `json:"repository_id"`
}

type EnvironmentGitPromotionTarget struct {
	SourceID         string `json:"source_id"`
	Generation       int64  `json:"generation"`
	IntentVersion    int64  `json:"intent_version"`
	CommitSHA        string `json:"commit_sha"`
	DefinitionDigest string `json:"definition_digest"`
	ProposedDigest   string `json:"proposed_digest"`
	ManifestPath     string `json:"manifest_path"`
}

// Artifact identities omit host paths, storage keys, configuration and secrets.
type EnvironmentGitPromotionArtifact struct {
	Resource          string `json:"resource"`
	DeploymentID      string `json:"deployment_id"`
	BuildID           string `json:"build_id,omitempty"`
	Kind              string `json:"kind"`
	ImageDigest       string `json:"image_digest,omitempty"`
	BuildArtifactID   string `json:"build_artifact_id,omitempty"`
	BuildArtifactHash string `json:"build_artifact_hash,omitempty"`
	ArtifactDigest    string `json:"artifact_digest,omitempty"`
	BuildCommitSHA    string `json:"build_commit_sha,omitempty"`
}

type EnvironmentGitPromotionChange struct {
	Resource string                    `json:"resource"`
	Before   EnvironmentWorkloadSource `json:"before"`
	After    EnvironmentWorkloadSource `json:"after"`
}

type PreviewEnvironmentGitPromotionResponse struct {
	Project                     string                            `json:"project"`
	FromEnvironment             string                            `json:"from_environment"`
	ToEnvironment               string                            `json:"to_environment"`
	ObservedAt                  time.Time                         `json:"observed_at"`
	ReadOnly                    bool                              `json:"read_only"`
	ValuesRedacted              bool                              `json:"values_redacted"`
	RequiresTargetQualification bool                              `json:"requires_target_qualification"`
	Origin                      EnvironmentGitPromotionOrigin     `json:"origin"`
	Target                      EnvironmentGitPromotionTarget     `json:"target"`
	Artifacts                   []EnvironmentGitPromotionArtifact `json:"artifacts"`
	Changes                     []EnvironmentGitPromotionChange   `json:"changes"`
	TargetPlan                  EnvironmentGitOpsPlan             `json:"target_plan"`
	BlockingReasons             []string                          `json:"blocking_reasons"`
	PlanHash                    string                            `json:"plan_hash"`
}
