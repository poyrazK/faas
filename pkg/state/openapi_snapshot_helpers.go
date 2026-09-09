package state

import (
	"encoding/json"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
)

func edgeRuleToCreateEdgeRuleRequest(r EdgeRule) (api.CreateEdgeRuleRequest, error) {
	action, err := json.Marshal(r.Action)
	if err != nil {
		return api.CreateEdgeRuleRequest{}, err
	}
	priority := r.Priority
	enabled := r.Enabled
	return api.CreateEdgeRuleRequest{
		MatchHost:    r.MatchHost,
		MatchPath:    r.MatchPath,
		MatchMethods: append([]string(nil), r.MatchMethods...),
		Priority:     &priority,
		Enabled:      &enabled,
		Kind:         string(r.Kind),
		ValidateMode: r.ValidateMode,
		Action:       action,
	}, nil
}

func validateOpenAPISnapshot(snap OpenAPISnapshot) error {
	if snap.DeploymentID == "" {
		return errors.New("empty deployment_id")
	}
	if snap.AppID == "" {
		return errors.New("empty app_id")
	}
	if snap.Scope == "" {
		return errors.New("empty scope")
	}
	if len(snap.Snapshot) == 0 {
		return errors.New("empty snapshot bytes")
	}
	if snap.SHA256 == "" {
		return errors.New("empty sha256")
	}
	if snap.SchemaVersion < 1 {
		return errors.New("schema_version must be >= 1")
	}
	return nil
}
