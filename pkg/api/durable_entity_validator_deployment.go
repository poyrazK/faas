// adr: 948
package api

// DurableEntityValidatorDeploymentInfo is an observational readiness sample,
// not a code-purity attestation or reservation for a later release/restore.
type DurableEntityValidatorDeploymentInfo struct {
	Status string `json:"status"`
	Source string `json:"source,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}
