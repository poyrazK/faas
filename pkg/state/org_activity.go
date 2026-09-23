package state

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

const orgActivityStoreLimitMax = 101

var orgActivityKindPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

func normalizeOrgActivity(entry OrgActivity, now time.Time) (OrgActivity, error) {
	if entry.OrgID == uuid.Nil {
		return OrgActivity{}, errors.New("state: org activity requires org id")
	}
	if strings.TrimSpace(entry.Kind) == "" || strings.TrimSpace(entry.ActorLabel) == "" ||
		strings.TrimSpace(entry.ResourceType) == "" || strings.TrimSpace(entry.ResourceLabel) == "" ||
		strings.TrimSpace(entry.SourceType) == "" || strings.TrimSpace(entry.SourceID) == "" {
		return OrgActivity{}, errors.New("state: org activity requires kind, actor, resource, and source")
	}
	if !orgActivityKindPattern.MatchString(entry.Kind) {
		return OrgActivity{}, errors.New("state: org activity kind must be namespaced")
	}
	switch entry.ActorType {
	case OrgActivityActorUser, OrgActivityActorAPIKey, OrgActivityActorGitHub,
		OrgActivityActorSystem, OrgActivityActorOperator:
	default:
		return OrgActivity{}, errors.New("state: org activity has invalid actor type")
	}
	if entry.OccurredAt.IsZero() {
		entry.OccurredAt = now.UTC()
	}
	if len(entry.Data) == 0 {
		entry.Data = json.RawMessage(`{}`)
	}
	var object map[string]any
	if err := json.Unmarshal(entry.Data, &object); err != nil || object == nil {
		return OrgActivity{}, errors.New("state: org activity data must be a JSON object")
	}
	entry.Data = append(json.RawMessage(nil), entry.Data...)
	return entry, nil
}

func normalizeOrgActivityLimit(limit int) int {
	if limit <= 0 || limit > orgActivityStoreLimitMax {
		return orgActivityStoreLimitMax
	}
	return limit
}
