package state

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// PRPreviewBatchStore is the atomic reservation capability required by
// githubd's source-backed PR preview path. It is kept separate from Store so
// other Store embedders need not implement a preview-only mutation.
type PRPreviewBatchStore interface {
	CreatePRPreviewAppsIfUnderQuota(context.Context, []App, api.Limits) ([]App, error)
}

var (
	_ PRPreviewBatchStore = (*PgStore)(nil)
	_ PRPreviewBatchStore = (*MemStore)(nil)
)

// validatePRPreviewBatch rejects malformed or mixed-account batches before any
// store mutation. A batch is one project and one pull request.
func validatePRPreviewBatch(apps []App) error {
	if len(apps) == 0 {
		return nil
	}
	accountID, projectID, prNumber := apps[0].AccountID, apps[0].ProjectID, apps[0].PreviewPrNumber
	seen := make(map[string]struct{}, len(apps))
	for _, app := range apps {
		if accountID == "" || app.AccountID != accountID || app.ProjectID != projectID ||
			prNumber <= 0 || app.PreviewPrNumber != prNumber || app.PreviewOfSlug == "" || app.Slug == "" || app.ID != "" {
			return fmt.Errorf("state: invalid PR preview batch: %w", ErrConflict)
		}
		if _, duplicate := seen[app.Slug]; duplicate {
			return fmt.Errorf("state: duplicate PR preview slug %q: %w", app.Slug, ErrConflict)
		}
		seen[app.Slug] = struct{}{}
	}
	return nil
}

func samePRPreview(existing, desired App) bool {
	return existing.AccountID == desired.AccountID && existing.OrgID == desired.OrgID && existing.ProjectID == desired.ProjectID &&
		existing.Slug == desired.Slug && existing.PreviewOfSlug == desired.PreviewOfSlug &&
		existing.PreviewPrNumber == desired.PreviewPrNumber
}
