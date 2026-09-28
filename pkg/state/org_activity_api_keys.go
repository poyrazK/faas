package state

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

func bindOrgActivityToAPIKey(entry OrgActivity, key APIKey) (OrgActivity, error) {
	orgID, err := uuid.Parse(key.OrgID)
	if err != nil {
		return OrgActivity{}, errors.New("state: api key activity requires an organization-bound key")
	}
	entry.OrgID = orgID
	if entry.ResourceType == "" {
		entry.ResourceType = "api_key"
	}
	entry.ResourceID = key.ID
	if strings.TrimSpace(key.Label) != "" {
		entry.ResourceLabel = key.Label
	} else if strings.TrimSpace(entry.ResourceLabel) == "" {
		entry.ResourceLabel = "API key"
	}
	if entry.SourceID == "" {
		entry.SourceID = entry.SourceType + ":" + key.ID
	}
	return normalizeOrgActivity(entry, time.Now())
}
