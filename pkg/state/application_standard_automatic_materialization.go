package state

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
)

type ApplicationStandardEnrollmentClaim struct {
	AppID           string
	OrgID           string
	Owner           string
	Generation      int64
	DesiredRevision int64
	Until           time.Time
}

type ApplicationStandardAutomaticMaterializationStore interface {
	ClaimApplicationStandardEnrollment(context.Context, string) (ApplicationStandardEnrollmentClaim, error)
	MaterializeApplicationStandardEnrollment(context.Context, ApplicationStandardEnrollmentClaim) (ApplicationStandardEnrollment, error)
	ReleaseApplicationStandardOperationWorker(context.Context, ApplicationStandardWorkerClaim) error
}

func standardEnrollmentClaimValid(c ApplicationStandardEnrollmentClaim) bool {
	return validStandardResourceRead(c.OrgID, c.AppID) && standardWorkerOwnerValid(c.Owner) && c.Generation > 0 && c.DesiredRevision > 0
}

// Resolve only the pins captured at the ownership boundary. Publishing or
// advancing admission cannot silently move this service to another version.
func resolveAutomaticStandardEnrollment(s standardReviewSnapshot, now time.Time) (standardReviewAppSnapshot, ApplicationStandardReviewedApp, string, error) {
	var app standardReviewAppSnapshot
	var proposed ApplicationStandardReviewedApp
	var err error
	s, err = normalizeStandardReviewSnapshot(s)
	if err != nil {
		return app, proposed, "invalid_enrollment_inputs", nil
	}
	if s.DeletedPending || s.OrgStatus != string(OrgStatusActive) || !s.ScopeOwned || len(s.Applications) != 1 {
		return app, proposed, "application_scope_not_active", nil
	}
	app = s.Applications[0]
	if !app.HasEnrollment || app.Enrollment.OrgID != app.OrgID || app.Enrollment.ProjectID != app.ProjectID {
		return app, proposed, "invalid_enrollment_scope", nil
	}
	versions, err := standardSnapshotVersions(s)
	if err != nil {
		return app, proposed, "invalid_standard_version", nil
	}
	assignments := make([]appstandards.Assignment, 0, len(s.Assignments))
	for _, a := range s.Assignments {
		assignments = append(assignments, a.Assignment)
	}
	selection, err := appstandards.SelectAdopted(appstandards.ApplicationOwnership{ID: app.AppID, OrgID: app.OrgID, ProjectID: app.ProjectID}, assignments, app.Enrollment.Adoptions, versions, api.ApplicationStandardResolverLimits())
	if err != nil {
		return app, proposed, "invalid_standard_adoption", nil
	}
	proposed, err = resolveStandardReviewedApp(app, selection, selection, now)
	if err != nil {
		return app, proposed, "invalid_application_projection", nil
	}
	blockers := standardReviewAppBlockers(app, proposed)
	refs := map[appstandards.Field]map[string]bool{appstandards.LogDestinations: {}, appstandards.TrustedPublishers: {}}
	blockers = append(blockers, bindStandardReviewAppResources(s, app, proposed, refs)...)
	blockers = append(blockers, standardReviewArtifactBlockers(s.Publishers, app, proposed, now)...)
	if len(blockers) > 0 {
		return app, proposed, blockers[0].Code, nil
	}
	delta := len(standardReviewStrings(proposed.Effective.Values[appstandards.LogDestinations])) - len(app.Drains)
	if delta > 0 && app.AccountDrainCount+delta > app.AccountPlan.LogDrainPerAccount() {
		return app, proposed, "plan_log_drain_account_limit", nil
	}
	return app, proposed, "", nil
}

func standardSnapshotVersions(s standardReviewSnapshot) (map[appstandards.VersionKey]appstandards.PublishedVersion, error) {
	versions := map[appstandards.VersionKey]appstandards.PublishedVersion{}
	for _, v := range s.Versions {
		definition, hash, err := appstandards.Parse(v.Definition, api.ApplicationStandardResolverLimits())
		if err != nil || hash != v.DefinitionHash {
			return nil, fmt.Errorf("stored standard version is invalid")
		}
		key := appstandards.VersionKey{StandardID: v.StandardID, Version: v.Version}
		if _, exists := versions[key]; exists {
			return nil, fmt.Errorf("duplicate stored standard version")
		}
		versions[key] = appstandards.PublishedVersion{OrgID: s.OrgID, Definition: definition, DefinitionHash: hash}
	}
	return versions, nil
}

func automaticInstalledStandardEnrollment(app standardReviewAppSnapshot, p ApplicationStandardReviewedApp, now time.Time) (ApplicationStandardEnrollment, error) {
	hash, err := standardReviewDigest(p.Effective)
	if err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	e := standardInstalledEnrollment(app, ApplicationStandardOperationTarget{ApprovedApp: p, approvalInput: appstandards.ApplicationApprovalInput{EffectiveHash: hash}}, now)
	// Automatic repair satisfies already persisted desired intent; it does not
	// manufacture a new version or make an older reviewed plan appear current.
	e.DesiredRevision = app.Enrollment.DesiredRevision
	e.PersistedRevision = e.DesiredRevision
	return e, nil
}

func standardMaterializedFieldsValid(fields []appstandards.Field) bool {
	allowed := []appstandards.Field{appstandards.LogDestinations, appstandards.RequireSigned, appstandards.SecurityPolicy, appstandards.TrustedPublishers, appstandards.EgressCIDRs, appstandards.EgressExtraPorts}
	for _, f := range fields {
		if !slices.Contains(allowed, f) {
			return false
		}
	}
	return true
}
