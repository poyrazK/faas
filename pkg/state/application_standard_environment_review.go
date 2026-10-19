package state

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Settings are read privately to verify the immutable revision hash, then
// discarded. Reviews retain only identity, provenance and the verified hash.
type standardReviewEnvironmentWorkload struct {
	EnvironmentID   string                              `json:"environment_id"`
	Scope           string                              `json:"scope"`
	Protected       bool                                `json:"protected"`
	AccountID       string                              `json:"account_id"`
	ProjectID       string                              `json:"project_id"`
	AppID           string                              `json:"app_id"`
	Role            string                              `json:"role"`
	DeploymentID    string                              `json:"deployment_id"`
	DeploymentScope string                              `json:"deployment_scope"`
	SpecID          string                              `json:"spec_id"`
	Revision        int64                               `json:"revision"`
	SettingsHash    string                              `json:"settings_hash"`
	Settings        *ProjectEnvironmentWorkloadSettings `json:"settings,omitempty"`
	SettingsBody    string                              `json:"settings_body,omitempty"`
	verified        bool
}

func makeStandardReviewEnvironmentWorkload(spec ProjectEnvironmentWorkloadSpec, env ProjectEnvironment, role, deploymentID, deploymentScope string) (standardReviewEnvironmentWorkload, error) {
	if !sameStandardUUID(spec.EnvironmentID, env.ID) || !sameStandardUUID(spec.AccountID, env.AccountID) ||
		!sameStandardUUID(spec.ProjectID, env.ProjectID) || spec.EnvironmentSlug != env.Slug {
		return standardReviewEnvironmentWorkload{}, fmt.Errorf("%w: reviewed environment workload owner", ErrConflict)
	}
	return standardReviewEnvironmentWorkload{EnvironmentID: env.ID, Scope: env.Slug, Protected: env.Protected,
		AccountID: env.AccountID, ProjectID: env.ProjectID, AppID: spec.AppID, Role: role, DeploymentID: deploymentID,
		DeploymentScope: deploymentScope, SpecID: spec.ID, Revision: spec.Revision, SettingsHash: spec.Hash, Settings: &spec.Settings}, nil
}

func normalizeStandardReviewEnvironmentWorkloads(app *standardReviewAppSnapshot) error {
	for i := range app.EnvironmentWorkloads {
		w := &app.EnvironmentWorkloads[i]
		if !validStandardResourceRead(w.EnvironmentID, w.SpecID) || !sameStandardUUID(w.AccountID, app.AccountID) ||
			!sameStandardUUID(w.ProjectID, app.ProjectID) || !sameStandardUUID(w.AppID, app.AppID) || w.Revision <= 0 ||
			api.ValidateScope(w.Scope) != nil || !slices.Contains([]string{"desired", "deployed"}, w.Role) ||
			w.Role == "desired" && w.DeploymentID != "" || w.Role == "deployed" && !validStandardResourceRead(w.DeploymentID, w.DeploymentID) {
			return fmt.Errorf("%w: reviewed environment workload identity", ErrConflict)
		}
		if w.Role == "deployed" {
			scope := w.DeploymentScope
			if scope == "default" {
				scope = "production"
			}
			if scope != w.Scope {
				return fmt.Errorf("%w: reviewed deployment environment scope", ErrConflict)
			}
		}
		// The SQL snapshot uses text so jsonb cannot rewrite nested RawMessage
		// numbers or whitespace before WorkloadSettingsHash verifies the body.
		if w.SettingsBody != "" {
			var settings ProjectEnvironmentWorkloadSettings
			if err := json.Unmarshal([]byte(w.SettingsBody), &settings); err != nil {
				return fmt.Errorf("%w: reviewed environment workload body", ErrConflict)
			}
			w.Settings = &settings
		}
		if w.Settings != nil {
			hash, err := WorkloadSettingsHash(*w.Settings)
			if err != nil || hash != w.SettingsHash {
				return fmt.Errorf("%w: reviewed environment workload hash", ErrConflict)
			}
			w.Settings, w.SettingsBody, w.verified = nil, "", true
		} else if !w.verified {
			return fmt.Errorf("%w: reviewed environment workload body missing", ErrConflict)
		}
		w.EnvironmentID, w.SpecID = canonicalStandardUUID(w.EnvironmentID), canonicalStandardUUID(w.SpecID)
		w.AccountID, w.ProjectID, w.AppID = canonicalStandardUUID(w.AccountID), canonicalStandardUUID(w.ProjectID), canonicalStandardUUID(w.AppID)
		if w.DeploymentID != "" {
			w.DeploymentID = canonicalStandardUUID(w.DeploymentID)
		}
	}
	slices.SortFunc(app.EnvironmentWorkloads, func(a, b standardReviewEnvironmentWorkload) int {
		return strings.Compare(a.EnvironmentID+"\x00"+a.Role+"\x00"+a.DeploymentID+"\x00"+a.SpecID,
			b.EnvironmentID+"\x00"+b.Role+"\x00"+b.DeploymentID+"\x00"+b.SpecID)
	})
	return nil
}
