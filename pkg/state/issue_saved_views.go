package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

type IssueSavedViewStore interface {
	ListIssueSavedViews(context.Context, string, string) (api.ListIssueSavedViewsResponse, error)
	GetIssueSavedView(context.Context, string, string, string) (api.IssueSavedView, error)
	PutIssueSavedView(context.Context, string, string, string, string, api.PutIssueSavedViewRequest, time.Time) (api.IssueSavedView, error)
	DeleteIssueSavedView(context.Context, string, string, string) error
}
