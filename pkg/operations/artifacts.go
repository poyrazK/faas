package operations

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var artifactDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func ValidateArtifact(req api.OperationArtifactRequest, limits api.OperationPlanLimits) error {
	if err := ValidateArtifactUpload(api.OperationArtifactUploadRequest{ReportID: req.ReportID, Name: req.Name, SizeBytes: req.SizeBytes, SHA256: req.SHA256}, limits); err != nil {
		return err
	}
	_, _, _, err := ParseArtifactURI(req.URI)
	return err
}

func ValidateArtifactUpload(req api.OperationArtifactUploadRequest, limits api.OperationPlanLimits) error {
	if req.ReportID == "" || len(req.ReportID) > api.OperationReportIDMaxBytes || !validArtifactText(req.ReportID) ||
		req.Name == "" || len(req.Name) > api.OperationArtifactNameMaxBytes || !validArtifactText(req.Name) || strings.ContainsAny(req.Name, "/\\") ||
		!artifactDigest.MatchString(req.SHA256) || req.SizeBytes < 0 {
		return fmt.Errorf("invalid artifact declaration")
	}
	if req.SizeBytes > limits.ArtifactMaxBytes || limits.ArtifactsPerOperation <= 0 {
		return fmt.Errorf("artifact exceeds plan limit")
	}
	return nil
}

// JobUploadArtifactDeclaration derives an opaque reference, never a provider
// URL or physical storage key. Each native execution has its own identity.
func JobUploadArtifactDeclaration(id, run string, attempt int, req api.OperationArtifactUploadRequest) api.OperationArtifactRequest {
	return UploadArtifactDeclaration(id, run, attempt, req)
}

// UploadArtifactDeclaration binds an opaque file identity to its execution.
func UploadArtifactDeclaration(id, execution string, attempt int, req api.OperationArtifactUploadRequest) api.OperationArtifactRequest {
	return api.OperationArtifactRequest{ReportID: req.ReportID, Name: req.Name, SizeBytes: req.SizeBytes, SHA256: req.SHA256,
		URI: fmt.Sprintf("operation://%s/artifacts/%s", id, ArtifactIdentity(id, execution, attempt, req.ReportID))}
}

// WorkflowArtifactIdentity is stable across approved resumes of the same run.
func WorkflowArtifactIdentity(operation, run, step, reportID string) string {
	return uuid.NewSHA1(uuid.Nil, []byte(operation+"/"+run+"/"+step+"/"+reportID)).String()
}

// WorkflowUploadArtifactDeclaration keeps the final step's logical file identity
// independent of generation/attempt; native authority is always checked separately.
func WorkflowUploadArtifactDeclaration(id, run, step string, req api.OperationArtifactUploadRequest) api.OperationArtifactRequest {
	return api.OperationArtifactRequest{ReportID: req.ReportID, Name: req.Name, SizeBytes: req.SizeBytes, SHA256: req.SHA256,
		URI: fmt.Sprintf("operation://%s/artifacts/%s", id, WorkflowArtifactIdentity(id, run, step, req.ReportID))}
}

// Object keys remain opaque: parsing never cleans or rewrites them.
func ParseArtifactURI(raw string) (app, bucket, key string, err error) {
	if len(raw) > api.OperationArtifactURIMaxBytes || strings.Contains(raw, "%") {
		return "", "", "", fmt.Errorf("invalid artifact reference")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "obj" || u.User != nil || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", "", "", fmt.Errorf("invalid artifact reference")
	}
	app = u.Hostname()
	parts := strings.SplitN(strings.TrimPrefix(u.Path, "/"), "/", 2)
	if len(parts) != 2 || !validArtifactKey(parts[1]) {
		return "", "", "", fmt.Errorf("invalid artifact reference")
	}
	if _, err := uuid.Parse(app); err != nil {
		return "", "", "", fmt.Errorf("invalid artifact app")
	}
	if _, err := uuid.Parse(parts[0]); err != nil {
		return "", "", "", fmt.Errorf("invalid artifact bucket")
	}
	return app, parts[0], parts[1], nil
}

func validArtifactKey(key string) bool {
	return len(key) <= api.OperationArtifactKeyMaxBytes && validArtifactText(key)
}

func validArtifactText(key string) bool {
	if len(key) == 0 || !utf8.ValidString(key) {
		return false
	}
	for _, r := range key {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}

// ArtifactIdentity is an idempotent report receipt identity, not a credential.
func ArtifactIdentity(operation, invocation string, attempt int, reportID string) string {
	return uuid.NewSHA1(uuid.Nil, []byte(fmt.Sprintf("%s/%s/%d/%s", operation, invocation, attempt, reportID))).String()
}
