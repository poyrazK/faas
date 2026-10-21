package state

import (
	"context"

	"github.com/onebox-faas/faas/internal/promotionfault"
)

func promotionPublicationBarrier(ctx context.Context, p ProjectEnvironmentPromotion, point, release string) error {
	return promotionfault.Hit(ctx, p.ProjectID, p.ID, point, release)
}
