package state

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type ProjectReleaseAdditionsStore interface {
	DormantProjectReleaseApps(context.Context, string, string) (map[string]bool, error)
	StageProjectReleaseAdditions(context.Context, string, string, string, *api.ProjectSafeRelease, []App) ([]App, error)
}

type projectReleaseAdditionRecord struct {
	AppID, AttemptID, Fingerprint string
	Dormant                       bool
}

// Fingerprints cover planned settings only; database defaults and physical IDs
// are deliberately excluded so scan and reservation describe the same intent.
func ProjectReleaseAdditionFingerprint(app App) string {
	data, _ := json.Marshal(struct {
		Account, Project, Slug, Root, Name, Start string
		Class                                     WorkloadClass
		Manifest                                  AppManifest
		Auth                                      bool
		AuthMode                                  string
		Tenant                                    bool
	}{app.AccountID, app.ProjectID, app.Slug, app.RootDir, app.WorkloadName, app.StartCommand,
		app.WorkloadClass, app.Manifest, app.RequireAuthn, app.PublicAuthMode, app.PlatformTenantRequired})
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func (m *MemStore) DormantProjectReleaseApps(_ context.Context, accountID, projectID string) (map[string]bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if project, ok := m.projects[projectID]; !ok || project.AccountID != accountID {
		return nil, ErrNotFound
	}
	out := map[string]bool{}
	for id, record := range m.projectReleaseAdditions {
		if app := m.apps[id]; app.ProjectID == projectID && app.Status != AppDeleted && record.Dormant {
			out[id] = true
		}
	}
	return out, nil
}

func additionOwnerFinished(payload []byte) bool {
	var r api.ProjectSubmissionReceipt
	if json.Unmarshal(payload, &r) != nil {
		return false
	}
	if r.Apply.SafeRelease != nil {
		switch r.Apply.SafeRelease.Status {
		case "failed", "conflicted", "rolled_back":
			return true
		}
	}
	return r.Status == "rejected" || r.Status == "interrupted"
}

func validateAdditionDrafts(accountID, projectID string, safe *api.ProjectSafeRelease, drafts []App) error {
	if safe == nil || !safe.AddServices || safe.Worker != nil || safe.ReleaseCommand != nil || safe.Configuration != nil || safe.Verification != nil && safe.Verification.Progressive || len(drafts) != len(safe.Additions) || len(drafts) == 0 {
		return ErrInvalidArgument
	}
	seen := map[string]bool{}
	for _, draft := range drafts {
		if draft.AccountID != accountID || draft.ProjectID != projectID || draft.PreviewOfSlug != "" || draft.WorkloadClass != WorkloadClassHTTP || draft.Manifest.RevisionPinTTLSeconds < safe.TTLSeconds || seen[draft.Slug] {
			return ErrInvalidArgument
		}
		if draft.Manifest.ExecutionMode == api.ExecutionModeWorker || draft.Manifest.ExecutionMode == api.ExecutionModeService || draft.Manifest.ProjectImage != "" || len(draft.Manifest.ServiceBindings) > 0 {
			return ErrInvalidArgument
		}
		seen[draft.Slug] = true
		found := false
		for _, addition := range safe.Additions {
			if addition.Slug == draft.Slug && addition.RootDir == draft.RootDir && addition.Fingerprint == ProjectReleaseAdditionFingerprint(draft) && addition.AppID == draft.ID {
				found = true
			}
		}
		if !found {
			return ErrConflict
		}
	}
	return nil
}

func (m *MemStore) StageProjectReleaseAdditions(_ context.Context, accountID, attemptID, projectID string, safe *api.ProjectSafeRelease, drafts []App) ([]App, error) {
	if err := validateAdditionDrafts(accountID, projectID, safe, drafts); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	project, found := m.projects[projectID]
	entry, reserved := m.projectSubmissions[accountID+"\x00"+attemptID]
	if !found || project.AccountID != accountID || !reserved || entry.Status != "processing" || m.activeProjectReleaseSets[releaseKey(projectID, "production")] != safe.PreviousReleaseID {
		return nil, ErrConflict
	}
	limits := api.MustLimitsFor(m.namespacePlanLocked(accountID))
	var receipt api.ProjectSubmissionReceipt
	if err := json.Unmarshal(entry.Payload, &receipt); err != nil {
		return nil, err
	}
	// Preflight the entire batch before the first app mutation.
	creates, observed := 0, 0
	for _, app := range m.apps {
		if app.AccountID == accountID && app.Status != AppDeleted && app.PreviewOfSlug == "" {
			observed++
		}
	}
	for _, draft := range drafts {
		if draft.ID != "" {
			record, ok := m.projectReleaseAdditions[draft.ID]
			current, exists := m.apps[draft.ID]
			if !ok || !exists || current.Status == AppDeleted || !record.Dormant || record.Fingerprint != ProjectReleaseAdditionFingerprint(current) || record.Fingerprint != ProjectReleaseAdditionFingerprint(draft) {
				return nil, ErrConflict
			}
			if record.AttemptID != attemptID && !additionOwnerFinished(m.projectSubmissions[accountID+"\x00"+record.AttemptID].Payload) {
				return nil, ErrConflict
			}
		} else {
			creates++
			for _, app := range m.apps {
				if app.Status != AppDeleted && app.Slug == draft.Slug {
					return nil, ErrConflict
				}
			}
		}
	}
	if observed+creates > limits.DeployedApps {
		return nil, &QuotaError{Kind: QuotaErrorKindApps, Limit: limits.DeployedApps, Observed: observed + creates}
	}
	if m.projectReleaseAdditions == nil {
		m.projectReleaseAdditions = map[string]projectReleaseAdditionRecord{}
	}
	var out []App
	var inserted []string
	for _, draft := range drafts {
		app := draft
		if draft.ID == "" {
			var err error
			app, err = m.createAppIfUnderQuotaLocked(draft, limits)
			if err != nil {
				for _, id := range inserted {
					delete(m.apps, id)
					delete(m.projectReleaseAdditions, id)
				}
				return nil, err
			}
			inserted = append(inserted, app.ID)
		}
		out = append(out, app)
	}
	receipt.Apply.SafeRelease = captureReservedAdditions(safe, out)
	payload, err := json.Marshal(receipt)
	if err != nil {
		for _, id := range inserted {
			delete(m.apps, id)
		}
		return nil, err
	}
	for _, app := range out {
		m.projectReleaseAdditions[app.ID] = projectReleaseAdditionRecord{app.ID, attemptID, ProjectReleaseAdditionFingerprint(app), true}
	}
	entry.Payload, entry.UpdatedAt = payload, time.Now().UTC()
	m.projectSubmissions[accountID+"\x00"+attemptID] = entry
	return out, nil
}

func captureReservedAdditions(safe *api.ProjectSafeRelease, apps []App) *api.ProjectSafeRelease {
	capture := *safe
	capture.Additions = append([]api.ProjectReleaseAddition(nil), safe.Additions...)
	for i := range capture.Additions {
		for _, app := range apps {
			if app.Slug == capture.Additions[i].Slug {
				capture.Additions[i].AppID = app.ID
			}
		}
	}
	return &capture
}

type releaseAdditionChange struct {
	AttemptID string
	Safe      *api.ProjectSafeRelease
	Rollback  bool
}

func guardReleaseAdditionDeployment(d Deployment, record projectReleaseAdditionRecord) error {
	if record.Dormant && (d.submissionAttemptID != ProjectWorkloadAttempt(record.AttemptID, d.AppID) || normalizedDeploymentScope(d.Scope) != "production" || !d.TrafficPercentExplicit || d.TrafficPercent != 0 || d.Kind != DeploymentKindTarball && d.Kind != DeploymentKindDockerfile) {
		return ErrConflict
	}
	return nil
}

type releaseAdditionChangeKey struct{}

func withReleaseAdditionChange(ctx context.Context, attemptID string, safe *api.ProjectSafeRelease, rollback bool) context.Context {
	if safe == nil || !safe.AddServices && !safe.RemoveServices {
		return ctx
	}
	return context.WithValue(ctx, releaseAdditionChangeKey{}, &releaseAdditionChange{attemptID, safe, rollback})
}

// Only the captured additions and retirements may cross the serving-membership
// boundary: an addition joins as its record leaves dormancy, a retirement
// (ADR-20261021120000) leaves as its record enters it, and rollback reverses both.
func allowedReleaseAdditionOmissions(records map[string]projectReleaseAdditionRecord, members map[string]string, change *releaseAdditionChange) (map[string]bool, error) {
	allowed := map[string]bool{}
	changes := map[string]bool{}
	removing := map[string]bool{}
	if change != nil {
		for _, addition := range change.Safe.Additions {
			record, ok := records[addition.AppID]
			if !ok || record.AttemptID != change.AttemptID || record.Fingerprint != addition.Fingerprint || changes[addition.AppID] {
				return nil, ErrConflict
			}
			_, included := members[addition.AppID]
			if included == change.Rollback || record.Dormant == change.Rollback {
				return nil, ErrConflict
			}
			changes[addition.AppID] = true
		}
		for _, removal := range change.Safe.Removals {
			record, ok := records[removal.AppID]
			if !ok || record.AttemptID != change.AttemptID || record.Fingerprint != removal.Fingerprint || changes[removal.AppID] || removing[removal.AppID] {
				return nil, ErrConflict
			}
			_, included := members[removal.AppID]
			if included != change.Rollback || record.Dormant != change.Rollback {
				return nil, ErrConflict
			}
			removing[removal.AppID] = true
		}
	}
	for id, record := range records {
		_, included := members[id]
		if record.Dormant {
			if included && !changes[id] && !removing[id] {
				return nil, ErrConflict
			}
			if !included {
				allowed[id] = true
			}
		} else if change != nil && (change.Rollback && changes[id] || !change.Rollback && removing[id]) {
			allowed[id] = true
		}
	}
	return allowed, nil
}

func guardReleaseAdditionMembership(change *releaseAdditionChange, baseline []ProjectReleaseMember, members map[string]string) error {
	if change == nil {
		return nil
	}
	expected := map[string]bool{}
	for _, member := range baseline {
		expected[member.AppID] = true
	}
	for _, addition := range change.Safe.Additions {
		if expected[addition.AppID] {
			return ErrConflict
		}
		if !change.Rollback {
			expected[addition.AppID] = true
		}
	}
	for _, removal := range change.Safe.Removals {
		if !expected[removal.AppID] {
			return ErrConflict
		}
		if !change.Rollback {
			delete(expected, removal.AppID)
		}
	}
	if len(expected) != len(members) {
		return ErrConflict
	}
	for id := range members {
		if !expected[id] {
			return ErrConflict
		}
	}
	return nil
}
