package dashboard

// ReleaseApprovalPageData is the /dashboard/releases/{id}/approval page
// (ADR-20261020085706). It carries only the release ID and the decision the
// link suggested; the page loads everything else through the API.
type ReleaseApprovalPageData struct {
	DeploymentID string
	// Intent is "approve", "reject" or empty; it only preselects a button.
	Intent string
}

// ReleasePageData is the /dashboard/releases/{id} page
// (ADR-20261022120744). Like the approval page it carries only the release
// ID; the page reads the timeline and the diff through the API.
type ReleasePageData struct {
	DeploymentID string
}
