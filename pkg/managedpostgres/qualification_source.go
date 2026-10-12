package managedpostgres

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"time"
)

var qualificationSourceRevision = regexp.MustCompile(`^[0-9a-f]{40}$`)

// QualificationSourceDigest also covers durable lifecycle evidence and spec.
// Existing v8 diagnostics remain readable; rollout additionally requires this
// provenance captured by the actual qualifier executable, not its caller's CWD.
func QualificationSourceDigest(artifact QualificationArtifact) string {
	payload := struct {
		Version            int                           `json:"version"`
		SourceRevision     string                        `json:"source_revision"`
		SourceClean        bool                          `json:"source_clean"`
		BackendID          string                        `json:"backend_id"`
		BackendFingerprint string                        `json:"backend_fingerprint"`
		Spec               Spec                          `json:"spec"`
		Report             QualificationReport           `json:"report"`
		Lifecycle          *LifecycleQualificationReport `json:"lifecycle"`
		Approval           *QualificationApproval        `json:"approval"`
	}{artifact.Version, artifact.SourceRevision, artifact.SourceClean, artifact.BackendID, artifact.BackendFingerprint, artifact.Spec, artifact.Report, artifact.Lifecycle, artifact.Approval}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func qualificationSourceReasons(artifact QualificationArtifact, expectedRevision string, now time.Time) []string {
	var reasons []string
	if !qualificationSourceRevision.MatchString(artifact.SourceRevision) {
		reasons = append(reasons, "source_revision_missing")
	} else if expectedRevision != "" && artifact.SourceRevision != expectedRevision {
		reasons = append(reasons, "source_revision_mismatch")
	}
	if !artifact.SourceClean {
		reasons = append(reasons, "source_dirty")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if artifact.Report.CompletedAt.After(now) {
		reasons = append(reasons, "source_window_invalid")
	}
	if len(artifact.SourceEvidenceSHA256) != 64 || artifact.SourceEvidenceSHA256 != QualificationSourceDigest(artifact) {
		reasons = append(reasons, "source_evidence_digest_mismatch")
	}
	return reasons
}
