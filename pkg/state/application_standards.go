package state

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
)

type ApplicationStandardVersion = api.ApplicationStandardVersion
type ApplicationStandardPublish struct {
	OrgID   string
	ActorID string
	Slug    string
	api.CreateApplicationStandardVersionRequest
}
type ApplicationStandardStore interface {
	PublishApplicationStandardVersion(context.Context, ApplicationStandardPublish) (ApplicationStandardVersion, error)
	GetApplicationStandardVersion(context.Context, string, string, int64) (ApplicationStandardVersion, error)
	ListApplicationStandards(context.Context, string, string, int) ([]ApplicationStandardVersion, error)
}

var applicationStandardSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}[a-z0-9]$`)

func prepareApplicationStandard(p ApplicationStandardPublish) (ApplicationStandardVersion, error) {
	if !applicationStandardSlug.MatchString(p.Slug) || !utf8.ValidString(p.Description) || len(p.Description) > api.ApplicationStandardMaxDescriptionBytes {
		return ApplicationStandardVersion{}, fmt.Errorf("invalid standard slug or description: %w", ErrInvalidArgument)
	}
	for _, id := range []string{p.OrgID, p.ActorID} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil {
			return ApplicationStandardVersion{}, ErrInvalidArgument
		}
	}
	if p.ExpectedVersion < 0 || p.ExpectedVersion >= api.ApplicationStandardMaxVersion {
		return ApplicationStandardVersion{}, ErrInvalidArgument
	}
	definition, hash, err := appstandards.Parse(p.Definition, api.ApplicationStandardResolverLimits())
	if err != nil {
		return ApplicationStandardVersion{}, fmt.Errorf("invalid standard definition: %w: %w", err, ErrInvalidArgument)
	}
	return ApplicationStandardVersion{OrgID: p.OrgID, Slug: p.Slug, Version: p.ExpectedVersion + 1, Definition: definition, DefinitionHash: hash, Description: p.Description, CreatedBy: p.ActorID}, nil
}

func cloneApplicationStandard(version ApplicationStandardVersion) ApplicationStandardVersion {
	raw, _ := json.Marshal(version)
	var out ApplicationStandardVersion
	_ = json.Unmarshal(raw, &out)
	return out
}

func validApplicationStandardRead(orgID, slug string, version int64) bool {
	org, err := uuid.Parse(orgID)
	return err == nil && org != uuid.Nil && applicationStandardSlug.MatchString(slug) && version >= 0 && version <= api.ApplicationStandardMaxVersion
}

func validApplicationStandardPage(orgID, after string, limit int) bool {
	org, err := uuid.Parse(orgID)
	return err == nil && org != uuid.Nil && (after == "" || applicationStandardSlug.MatchString(after)) && limit > 0 && limit <= api.ApplicationStandardMaxListPage+1
}
