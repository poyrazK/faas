package api

import "time"

const ExecutionFilesDocsURL = "https://gregale.dev/docs/executions#larger-data-files-and-standalone-contracts"

type ExecutionArtifactStorage string

const (
	ExecutionArtifactInline ExecutionArtifactStorage = "inline"
	ExecutionArtifactStored ExecutionArtifactStorage = "stored"
)

type ExecutionFileInput struct {
	FileID     string `json:"file_id,omitempty"`
	GrantToken string `json:"grant_token,omitempty"`
	Path       string `json:"path"`
}
type CreateExecutionFileRequest struct {
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}
type ExecutionFileResponse struct {
	ID             string     `json:"id"`
	Status         string     `json:"status"`
	SizeBytes      int64      `json:"size_bytes"`
	SHA256         string     `json:"sha256,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	UploadRequired bool       `json:"upload_required,omitempty"`
	ContentURL     string     `json:"content_url"`
}
type ExecutionFileLimits struct {
	MaxFileBytes   int64 `json:"max_file_bytes"`
	MaxInputBytes  int64 `json:"max_input_bytes"`
	MaxOutputBytes int64 `json:"max_output_bytes"`
	MaxStoredBytes int64 `json:"max_stored_bytes"`
	MaxFiles       int   `json:"max_files"`
}

func (p Plan) ExecutionFileLimits() ExecutionFileLimits {
	i := p.PlanIndex()
	return ExecutionFileLimits{MaxFileBytes: ExecutionFileBytesByPlan[i], MaxInputBytes: ExecutionFileBytesByPlan[i], MaxOutputBytes: ExecutionFileBytesByPlan[i], MaxStoredBytes: ExecutionFileStorageByPlan[i], MaxFiles: ExecutionFileCountByPlan[i]}
}
func (r CreateExecutionRequest) UsesExtendedExecution() bool {
	return r.EnvironmentID != "" || r.Limits != nil && r.Limits.Class == ExecutionClassExtended || len(r.InputSchema) > 0 || len(r.ResultSchema) > 0 || len(r.InputFiles) > 0 || r.ArtifactStorage == ExecutionArtifactStored
}
