package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"time"
)

type InstanceApplicationStandardPromotionStore interface {
	GetInstanceApplicationStandardWarmParent(context.Context, string) (runtimeadmission.Receipt, error)
	IssueInstanceApplicationStandardPromotion(context.Context, runtimeadmission.Promotion) (runtimeadmission.Promotion, error)
	PublishInstanceApplicationStandardPromotion(context.Context, runtimeadmission.Receipt) (Instance, error)
}

type instanceStandardPromotion struct {
	Grant      runtimeadmission.Promotion
	Receipt    *runtimeadmission.Receipt
	ReceivedAt time.Time
}

type nativePromotionLockedInputs struct {
	nativeBootLockedInputs
	Parent         runtimeadmission.Receipt `json:"parent"`
	State          string                   `json:"state"`
	PromotionToken *string                  `json:"promotion_token"`
}
