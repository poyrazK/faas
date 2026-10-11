package api

// RuntimeReleaseResponse reports actual published base bytes. It does not
// claim an interpreter patch number, kernel pin or successful upgrade test.
type RuntimeReleaseResponse struct {
	ID              string `json:"id"`
	Runtime         string `json:"runtime"`
	Architecture    string `json:"architecture"`
	SourceDigest    string `json:"source_digest"`
	GuestInitDigest string `json:"guest_init_digest"`
	BaseDigest      string `json:"base_digest"`
	LayoutVersion   string `json:"layout_version"`
	PublishedAt     string `json:"published_at"`
	Qualification   string `json:"qualification"`
}
type DeploymentRuntimeResponse struct {
	DeploymentID string                   `json:"deployment_id"`
	Status       string                   `json:"status"`
	Reason       string                   `json:"reason"`
	Current      *RuntimeReleaseResponse  `json:"current"`
	Releases     []RuntimeReleaseResponse `json:"releases"`
}
type RuntimeUpgradePreviewResponse struct {
	DeploymentID       string                  `json:"deployment_id"`
	Current            *RuntimeReleaseResponse `json:"current"`
	Target             RuntimeReleaseResponse  `json:"target"`
	Disposition        string                  `json:"disposition"`
	Changes            []string                `json:"changes"`
	Blockers           []string                `json:"blockers"`
	RequiredSteps      []string                `json:"required_steps"`
	RebuildRequired    bool                    `json:"rebuild_required"`
	ColdStartRequired  bool                    `json:"cold_start_required"`
	ExecutionAvailable bool                    `json:"execution_available"`
}
