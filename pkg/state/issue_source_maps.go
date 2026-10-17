package state

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
)

type IssueSourceMapStore interface {
	PublishIssueSourceMaps(context.Context, string, string, string, string, api.PublishIssueSourceMapsRequest) (api.IssueSourceMapsMetadata, error)
	GetIssueSourceMaps(context.Context, string, string, string) (api.IssueSourceMapsMetadata, error)
	RetireIssueSourceMaps(context.Context, string, string, string, string) (api.IssueSourceMapsMetadata, error)
}

var ErrIssueSourceMapsConflict = errors.New("deployment source-map manifest is immutable")
var ErrIssueSourceMapsUnavailable = errors.New("deployment source-map artifacts are unavailable")
