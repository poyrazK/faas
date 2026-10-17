package api

import (
	"context"
	"encoding/json"
	"net/url"
)

// Values are staged separately; preview and approval carry immutable IDs only.
type ProjectEnvironmentPromotionSecretPreviewRequest struct {
	FromEnvironment string                       `json:"from_environment"`
	SyncConfig      bool                         `json:"sync_config"`
	Verify          bool                         `json:"verify"`
	Secrets         map[string]map[string]string `json:"secrets"`
}

func ValidatePromotionSecretReferences(refs map[string]map[string]string) error {
	raw, err := json.Marshal(ProjectReleaseConfigRequest{Secrets: refs})
	if err != nil {
		return err
	}
	_, _, err = NormalizeProjectReleaseConfig(raw)
	return err
}

func (c *Client) PreviewProjectEnvironmentPromotionSecrets(ctx context.Context, project, target string, req ProjectEnvironmentPromotionSecretPreviewRequest) (ProjectEnvironmentPromotionPreviewResponse, error) {
	var out ProjectEnvironmentPromotionPreviewResponse
	if err := ValidatePromotionSecretReferences(req.Secrets); err != nil {
		return out, err
	}
	path := "/v1/projects/" + url.PathEscape(project) + "/environments/" + url.PathEscape(target) + "/promotion-preview"
	return out, c.do(ctx, "POST", path, req, &out)
}
