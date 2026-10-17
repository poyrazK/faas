package api

import "time"

// Manifest shape matches the Node build command; generatedFile is intentionally
// retained as the portable manifest's field name rather than an API identity.
type IssueSourceMapArtifact struct {
	GeneratedFile string `json:"generatedFile"`
	SHA256        string `json:"sha256"`
	Payload       string `json:"payload"`
}
type IssueSourceMapManifest struct {
	Version   int                      `json:"version"`
	Artifacts []IssueSourceMapArtifact `json:"artifacts"`
}
type PublishIssueSourceMapsRequest struct {
	Directory string                 `json:"directory"`
	Manifest  IssueSourceMapManifest `json:"manifest"`
}
type IssueSourceMapsMetadata struct {
	DeploymentID   string     `json:"deployment_id"`
	ManifestSHA256 string     `json:"manifest_sha256"`
	MapCount       int        `json:"map_count"`
	Ready          bool       `json:"ready"`
	CreatedAt      time.Time  `json:"created_at"`
	RetiredAt      *time.Time `json:"retired_at,omitempty"`
}
type IssueSymbolicationResult struct {
	ManifestSHA256 string `json:"manifest_sha256"`
	MappedFrames   int    `json:"mapped_frames"`
	UnmappedFrames int    `json:"unmapped_frames"`
}
