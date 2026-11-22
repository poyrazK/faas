package api

// CreateExecutionEnvironmentRequest contains dependency metadata only. It never
// accepts Dockerfiles, shell commands, credentials, local paths or base images.
type CreateExecutionEnvironmentRequest struct {
	Runtime      ExecutionRuntime `json:"runtime"`
	Requirements string           `json:"requirements,omitempty"`
	PackageJSON  string           `json:"package_json,omitempty"`
	PackageLock  string           `json:"package_lock,omitempty"`
}

type ExecutionEnvironmentResponse struct {
	ID            string           `json:"id"`
	Runtime       ExecutionRuntime `json:"runtime"`
	Status        string           `json:"status"`
	RecipeDigest  string           `json:"recipe_digest"`
	ImageDigest   string           `json:"image_digest,omitempty"`
	ArtifactBytes int64            `json:"artifact_bytes,omitempty"`
	FailureCode   string           `json:"failure_code,omitempty"`
	CreatedAt     string           `json:"created_at"`
}

type ExecutionEnvironmentLimits struct {
	MaxEnvironments  int   `json:"max_environments"`
	MaxArtifactBytes int64 `json:"max_artifact_bytes"`
	MaxRecipeBytes   int   `json:"max_recipe_bytes"`
	MaxPackages      int   `json:"max_packages"`
}
