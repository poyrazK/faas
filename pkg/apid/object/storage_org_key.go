package object

import (
	"context"

	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) objectGrantKey(ctx context.Context, accountID, keyID string) (state.APIKey, error) {
	if grant, ok := appaccess.FromContext(ctx); ok {
		return s.Host.Store().GetOrgAPIKey(ctx, grant.App.OrgID, keyID)
	}
	return s.Host.Store().GetAPIKey(ctx, accountID, keyID)
}
