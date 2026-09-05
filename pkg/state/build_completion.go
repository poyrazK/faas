package state

// BuildImageWork is persisted by the existing deployment/build/provenance
// rows. The producer identity confines local OCI consumption to its owner.
type BuildImageWork struct {
	AppID        string
	DeploymentID string
	NodeID       string
}
