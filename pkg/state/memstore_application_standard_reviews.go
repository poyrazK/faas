package state

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/appstandards"
)

var _ ApplicationStandardReviewStore = (*MemStore)(nil)

func (m *MemStore) PreviewApplicationStandardAssignment(ctx context.Context, orgID, actorID string, input ApplicationStandardReviewRequest) (ApplicationStandardReviewPlan, error) {
	r, err := prepareStandardReview(orgID, actorID, input)
	if err != nil {
		return ApplicationStandardReviewPlan{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshot, err := m.standardReviewSnapshotLocked(ctx, orgID, actorID, r)
	if err != nil {
		return ApplicationStandardReviewPlan{}, err
	}
	p, err := buildStandardReview(snapshot, r, uuid.NewString(), actorID, time.Now().UTC())
	if err != nil {
		return ApplicationStandardReviewPlan{}, err
	}
	if m.applicationStandardReviewPlans == nil {
		m.applicationStandardReviewPlans = map[string]ApplicationStandardReviewPlan{}
	}
	m.applicationStandardReviewPlans[p.ID] = cloneStandardReviewPlan(p)
	return cloneStandardReviewPlan(p), nil
}

func (m *MemStore) GetApplicationStandardReviewPlan(_ context.Context, orgID, planID string) (ApplicationStandardReviewPlan, error) {
	if !validStandardResourceRead(orgID, planID) {
		return ApplicationStandardReviewPlan{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.standardReviewPlanLocked(orgID, planID)
}

func (m *MemStore) standardReviewPlanLocked(orgID, planID string) (ApplicationStandardReviewPlan, error) {
	p, exists := m.applicationStandardReviewPlans[canonicalStandardUUID(planID)]
	if !exists || !sameStandardUUID(p.OrgID, orgID) {
		return ApplicationStandardReviewPlan{}, ErrNotFound
	}
	return cloneStandardReviewPlan(p), nil
}

func (m *MemStore) ValidateApplicationStandardReview(ctx context.Context, orgID, actorID, planID, expected string) (ApplicationStandardReviewPlan, error) {
	if !validStandardResourceRead(orgID, actorID) || !validStandardResourceRead(orgID, planID) {
		return ApplicationStandardReviewPlan{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	saved, err := m.standardReviewPlanLocked(orgID, planID)
	if err != nil {
		return ApplicationStandardReviewPlan{}, err
	}
	snapshot, err := m.standardReviewSnapshotLocked(ctx, orgID, actorID, saved.Request)
	if err != nil {
		return ApplicationStandardReviewPlan{}, standardReviewFreshnessError(err)
	}
	fresh, err := buildStandardReview(snapshot, saved.Request, saved.ID, saved.CreatedBy, time.Now().UTC())
	if err != nil {
		return ApplicationStandardReviewPlan{}, standardReviewFreshnessError(err)
	}
	return saved, validateStandardReviewHash(saved, fresh, expected, time.Now().UTC())
}

func (m *MemStore) standardReviewSnapshotLocked(ctx context.Context, orgID, actorID string, r ApplicationStandardReviewRequest) (standardReviewSnapshot, error) {
	s := standardReviewSnapshot{Assignments: []standardReviewAssignment{}, Versions: []standardReviewVersion{}, Destinations: []standardReviewResource{}, Publishers: []standardReviewResource{}, Applications: []standardReviewAppSnapshot{}}
	var org Org
	found := false
	for _, row := range m.orgs {
		if sameStandardUUID(row.ID, orgID) {
			org, found = row, true
			break
		}
	}
	if !found {
		return s, ErrNotFound
	}
	s.OrgID, s.OrgPlan, s.OrgStatus, s.DeletedPending = canonicalStandardUUID(org.ID), org.Plan, string(org.Status), org.DeletedPending
	for _, membership := range m.memberships {
		if !sameStandardUUID(membership.OrgID, orgID) || !sameStandardUUID(membership.AccountID, actorID) || membership.RemovedAt != nil || (membership.Role != OrgRoleOwner && membership.Role != OrgRoleAdmin) {
			continue
		}
		for _, account := range m.accounts {
			if sameStandardUUID(account.ID, actorID) && account.Status == AccountActive {
				s.ActorAuthorized = true
			}
		}
	}
	switch r.Scope {
	case "organization":
		s.ScopeOwned = sameStandardUUID(r.ScopeID, org.ID)
	case "application":
		for _, app := range m.apps {
			if sameStandardUUID(app.ID, r.ScopeID) && sameStandardUUID(app.OrgID, org.ID) && app.Status != AppDeleted {
				s.ScopeOwned = true
			}
		}
	case "project":
		for _, project := range m.projects {
			if !sameStandardUUID(project.ID, r.ScopeID) {
				continue
			}
			s.ScopeOwnerID = canonicalStandardUUID(project.AccountID)
			s.ScopeOwned = org.PersonalOwnerAccountID != nil && sameStandardUUID(*org.PersonalOwnerAccountID, project.AccountID)
			for _, member := range m.memberships {
				if sameStandardUUID(member.OrgID, org.ID) && sameStandardUUID(member.AccountID, project.AccountID) && member.RemovedAt == nil {
					s.ScopeOwned = true
				}
			}
		}
		for _, app := range m.apps {
			if sameStandardUUID(app.ProjectID, r.ScopeID) && app.Status != AppDeleted && !sameStandardUUID(app.OrgID, org.ID) {
				s.ScopeOwned = false
			}
		}
	}
	for _, record := range m.applicationStandardAssignments {
		if !sameStandardUUID(record.OrgID, org.ID) {
			continue
		}
		a := record.Assignment
		a.ID, a.OrgID, a.ScopeID, a.StandardID = canonicalStandardUUID(a.ID), s.OrgID, canonicalStandardUUID(a.ScopeID), canonicalStandardUUID(a.StandardID)
		s.Assignments = append(s.Assignments, standardReviewAssignment{Assignment: a, Revision: record.Revision, Active: record.Active})
	}
	relevantStandards := map[string]bool{r.StandardID: true}
	for _, assignment := range s.Assignments {
		relevantStandards[assignment.StandardID] = true
	}
	for _, versions := range m.applicationStandardVersions {
		for _, version := range versions {
			if !sameStandardUUID(version.OrgID, org.ID) || !relevantStandards[canonicalStandardUUID(version.StandardID)] {
				continue
			}
			raw, err := json.Marshal(version.Definition)
			if err != nil {
				return s, err
			}
			s.Versions = append(s.Versions, standardReviewVersion{StandardID: canonicalStandardUUID(version.StandardID), Version: version.Version, Definition: raw, DefinitionHash: version.DefinitionHash})
		}
	}
	for _, d := range m.applicationStandardLogDestinations {
		if sameStandardUUID(d.OrgID, org.ID) {
			s.Destinations = append(s.Destinations, standardReviewResource{ID: canonicalStandardUUID(d.ID), ConfigHash: d.ConfigHash, Kind: d.Kind})
		}
	}
	for _, p := range m.applicationStandardPublishers {
		if sameStandardUUID(p.OrgID, org.ID) {
			s.Publishers = append(s.Publishers, standardReviewResource{ID: canonicalStandardUUID(p.ID), Fingerprint: p.Fingerprint})
		}
	}
	accountDrainCounts := map[string]int{}
	owners := map[string]App{}
	for _, app := range m.apps {
		owners[canonicalStandardUUID(app.ID)] = app
	}
	for _, d := range m.appLogDrains {
		if owner, exists := owners[canonicalStandardUUID(d.AppID)]; exists && owner.Status != AppDeleted {
			accountDrainCounts[canonicalStandardUUID(owner.AccountID)]++
		}
	}
	for _, app := range m.apps {
		if !sameStandardUUID(app.OrgID, org.ID) || app.Status == AppDeleted || (r.Scope == "project" && !sameStandardUUID(app.ProjectID, r.ScopeID)) || (r.Scope == "application" && !sameStandardUUID(app.ID, r.ScopeID)) {
			continue
		}
		a := standardReviewAppSnapshot{AppID: canonicalStandardUUID(app.ID), OrgID: s.OrgID, AccountID: canonicalStandardUUID(app.AccountID), Slug: app.Slug, Status: string(app.Status), Type: string(app.Type), WorkloadClass: string(app.WorkloadClass), Settings: applicationStandardBaseSettings(app), Drains: []standardReviewDrain{}, Signers: []standardReviewSigner{}, Artifacts: []standardReviewArtifact{}, ArchivedResources: []standardReviewArchivedResource{}}
		a.AccountDrainCount = accountDrainCounts[a.AccountID]
		a.Exceptions = m.standardActiveExceptionsLocked(app.OrgID, app.ID, time.Now())
		if app.ProjectID != "" {
			a.ProjectID = canonicalStandardUUID(app.ProjectID)
		}
		for _, account := range m.accounts {
			if sameStandardUUID(account.ID, app.AccountID) {
				a.AccountPlan, a.AccountStatus = account.Plan, string(account.Status)
				a.AccountEgressAllowlistExtra = account.EgressAllowlistExtra
			}
		}
		for _, enrollment := range m.applicationStandardEnrollments {
			if sameStandardUUID(enrollment.AppID, app.ID) {
				a.HasEnrollment = true
				projectID := ""
				if enrollment.ProjectID != "" {
					projectID = canonicalStandardUUID(enrollment.ProjectID)
				}
				a.Enrollment = standardReviewEnrollment{MaterializedFields: append([]appstandards.Field{}, enrollment.MaterializedFields...), OrgID: canonicalStandardUUID(enrollment.OrgID), ProjectID: projectID, BaseSettings: cloneStandardSettings(enrollment.BaseSettings), LocalSettings: cloneStandardSettings(enrollment.LocalSettings), AdditionalLogDestinations: append([]string{}, enrollment.AdditionalLogDestinations...), Adoptions: append([]appstandards.Adoption{}, enrollment.Adoptions...), DesiredRevision: enrollment.DesiredRevision, Effective: enrollment.Effective, EffectiveHash: enrollment.EffectiveHash}
			}
		}
		for _, d := range m.appLogDrains {
			if sameStandardUUID(d.AppID, app.ID) {
				resourceID := ""
				for _, b := range m.applicationStandardControlBindings {
					if sameStandardUUID(b.AppID, app.ID) && b.Field == appstandards.LogDestinations && b.PhysicalID == d.ID {
						resourceID = b.ResourceID
					}
				}
				a.Drains = append(a.Drains, standardReviewDrain{ResourceID: resourceID, ID: canonicalStandardUUID(d.ID), Kind: string(d.Kind), TargetHash: standardReviewBytesDigest([]byte(d.TargetURL)), AuthHash: standardReviewBytesDigest(d.AuthHeaderSealed), Enabled: d.Enabled})
			}
		}
		for _, signer := range m.trustedSigners {
			if sameStandardUUID(signer.AppID, app.ID) {
				resourceID := ""
				for _, b := range m.applicationStandardControlBindings {
					if sameStandardUUID(b.AppID, app.ID) && b.Field == appstandards.TrustedPublishers && b.PhysicalID == signer.SignerName {
						resourceID = b.ResourceID
					}
				}
				a.Signers = append(a.Signers, standardReviewSigner{ResourceID: resourceID, Name: signer.SignerName, Fingerprint: standardReviewBytesDigest(signer.CosignPublicKey)})
			}
		}
		for _, b := range m.applicationStandardControlBackups {
			if sameStandardUUID(b.AppID, app.ID) {
				a.ArchivedResources = append(a.ArchivedResources, standardReviewArchivedResource{Field: b.Field, ID: b.ID, ConfigHash: b.ConfigHash, Body: b.Body})
			}
		}
		for key, specID := range m.projectEnvironmentWorkloadHeads {
			environmentID, ownerAppID, validKey := strings.Cut(key, ":")
			if !validKey || !sameStandardUUID(ownerAppID, app.ID) {
				continue
			}
			spec, exists := m.projectEnvironmentWorkloadSpecs[specID]
			if !exists || !sameStandardUUID(spec.AppID, app.ID) || !sameStandardUUID(spec.EnvironmentID, environmentID) {
				return s, fmt.Errorf("%w: reviewed workload head binding", ErrConflict)
			}
			env := m.projectEnvironments[spec.EnvironmentID]
			workload, err := makeStandardReviewEnvironmentWorkload(spec, env, "desired", "", "")
			if err != nil {
				return s, err
			}
			a.EnvironmentWorkloads = append(a.EnvironmentWorkloads, workload)
		}
		for _, deployment := range m.deployments {
			retained := !slices.Contains([]string{"failed", "superseded", "cancelled"}, string(deployment.Status))
			for _, instance := range m.instances {
				if sameStandardUUID(instance.DeploymentID, deployment.ID) && instance.TerminalAt == nil && instance.State != "stopped" && instance.State != "failed" {
					retained = true
				}
			}
			if !sameStandardUUID(deployment.AppID, app.ID) || !retained {
				continue
			}
			if specID := m.projectEnvironmentWorkloadDeploymentSpecs[deployment.ID]; specID != "" {
				spec := m.projectEnvironmentWorkloadSpecs[specID]
				env := m.projectEnvironments[spec.EnvironmentID]
				workload, err := makeStandardReviewEnvironmentWorkload(spec, env, "deployed", deployment.ID, deployment.Scope)
				if err != nil {
					return s, err
				}
				a.EnvironmentWorkloads = append(a.EnvironmentWorkloads, workload)
			}
			layers := []DeploymentSidecarLayer{}
			for _, layer := range m.deploymentSidecarLayers {
				if sameStandardUUID(layer.DeploymentID, deployment.ID) {
					layer.CreatedAt, layer.UpdatedAt = time.Time{}, time.Time{}
					layers = append(layers, layer)
				}
			}
			slices.SortFunc(layers, func(a, b DeploymentSidecarLayer) int { return strings.Compare(a.SidecarName, b.SidecarName) })
			layerHash := standardReviewBytesDigest(nil)
			if len(layers) != 0 {
				layerHash, _ = standardReviewDigest(layers)
			}
			a.Artifacts = append(a.Artifacts, standardReviewArtifact{ID: canonicalStandardUUID(deployment.ID), Scope: deployment.Scope, Kind: string(deployment.Kind), Status: string(deployment.Status), ImageDigest: deployment.ImageDigest, RootfsKey: deployment.RootfsKey, RootfsBytes: deployment.RootfsBytes, SourceSHA256: deployment.SourceSHA256, ParkedReason: deployment.ParkedReason, ScanStatus: deployment.ScanStatus, ScanResultHash: standardReviewBytesDigest(deployment.ScanResult), SidecarHash: layerHash})
		}
		slices.SortFunc(a.Drains, func(a, b standardReviewDrain) int { return strings.Compare(a.ID, b.ID) })
		slices.SortFunc(a.Signers, func(a, b standardReviewSigner) int { return strings.Compare(a.Name, b.Name) })
		slices.SortFunc(a.Artifacts, func(a, b standardReviewArtifact) int { return strings.Compare(a.ID, b.ID) })
		s.Applications = append(s.Applications, a)
	}
	slices.SortFunc(s.Applications, func(a, b standardReviewAppSnapshot) int { return strings.Compare(a.AppID, b.AppID) })
	if err := m.completeStandardReviewArtifactSecurityLocked(ctx, &s); err != nil {
		return s, err
	}
	return s, nil
}
