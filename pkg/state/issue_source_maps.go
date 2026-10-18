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

// BuildIssueSourceMapStore extends the operational build-completion fence.
// The publication and success become visible in one transaction, before
// imaged can find the build. It does not modify customer deployment intent.
type BuildIssueSourceMapStore interface {
	CompleteBuildWithIssueSourceMaps(context.Context, Build, string, string, int64, BuildProvenance, BuildIssueSourceMapPublication) error
}

type BuildIssueSourceMapPublication struct {
	AccountID string
	AppID     string
	Request   api.PublishIssueSourceMapsRequest
}

var ErrIssueSourceMapsConflict = errors.New("deployment source-map manifest is immutable")
var ErrIssueSourceMapsUnavailable = errors.New("deployment source-map artifacts are unavailable")
