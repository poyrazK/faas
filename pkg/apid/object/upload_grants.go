package object

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) pruneObjectUploadGrants(ctx context.Context) error {
	st, ok := s.Host.Store().(state.ObjectUploadGrantStore)
	if !ok {
		return nil
	}
	callCtx, cancel := context.WithTimeout(ctx, api.ObjectMutationObservationTimeout)
	defer cancel()
	_, err := st.PruneExpiredObjectUploadGrants(callCtx, api.ObjectUploadGrantPruneBatch)
	return err
}
