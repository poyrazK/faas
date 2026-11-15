package toolpublication

import (
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func VerificationDigest(in api.ToolPublicationVerification, intent api.ToolPublicationIntent) string {
	data, _ := json.Marshal(struct {
		Publication, Key, App, Deployment, Source, Catalog string
		Intent                                             api.ToolPublicationIntent
		ReadinessArtifact                                  string `json:",omitempty"`
	}{in.PublicationID, in.KeyID, in.AppID, in.DeploymentID, in.DeploymentSourceSHA256, in.CatalogSHA256, intent, in.ReadinessArtifactSHA256})
	return Digest(data)
}

func VerificationView(in api.ToolPublicationVerification, now time.Time) api.ToolPublicationVerification {
	if (in.Status == "pending" || in.Status == "approved" || in.Status == "running") && !now.Before(in.Deadline) {
		in.Status = "outcome_unknown"
		in.Outcome = nil
	}
	return in
}

func VerifiedOutcome(record api.ToolPublicationRecord, out api.ToolPublicationOutcome) bool {
	if !record.Intent.PlatformVerification || out.Status != "completed" {
		return true
	}
	v := record.Verification
	if v == nil || v.Status != "completed" || v.Outcome == nil || !v.Outcome.OK || !v.Outcome.ProtocolVerified || v.DeploymentID != out.DeploymentID || v.CatalogSHA256 != out.CatalogSHA256 || Digest(v.Outcome.Catalog) != Digest(out.Catalog) {
		return false
	}
	if !ReadinessResultsMatch(record.Intent.Readiness, v.Outcome.Readiness) {
		return false
	}
	a, _ := json.Marshal(v.Outcome.Checks)
	b, _ := json.Marshal(out.Checks)
	return string(a) == string(b)
}
