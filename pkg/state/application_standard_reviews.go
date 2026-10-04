package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
)

var (
	ErrApplicationStandardReviewStale     = errors.New("state: application standard review inputs changed")
	ErrApplicationStandardReviewExpired   = errors.New("state: application standard review expired")
	ErrApplicationStandardReviewBlocked   = errors.New("state: application standard review has blockers")
	ErrApplicationStandardReviewForbidden = errors.New("state: application standard review requires an active organization owner or administrator")
)

// Reviews are persisted intent, not assignment activation. Public activation
// remains unavailable until projection and consumer verification are wired.
type ApplicationStandardReviewRequest struct {
	AssignmentID     string `json:"assignment_id,omitempty"`
	Scope            string `json:"scope"`
	ScopeID          string `json:"scope_id"`
	StandardID       string `json:"standard_id"`
	AdmissionVersion int64  `json:"admission_version"`
	ExpectedRevision int64  `json:"expected_revision"`
	Active           bool   `json:"active"`
	BatchSize        int    `json:"batch_size"`
}

type ApplicationStandardReviewBlocker struct {
	AppID   string             `json:"app_id,omitempty"`
	Scope   string             `json:"scope,omitempty"`
	ScopeID string             `json:"scope_id,omitempty"`
	Field   appstandards.Field `json:"field,omitempty"`
	Code    string             `json:"code"`
}

type ApplicationStandardReviewedApp struct {
	AppID                     string                  `json:"app_id"`
	AccountID                 string                  `json:"account_id"`
	Slug                      string                  `json:"slug"`
	ProjectID                 string                  `json:"project_id,omitempty"`
	DesiredRevision           int64                   `json:"desired_revision"`
	BeforeSettings            appstandards.Settings   `json:"before_settings"`
	BeforeAdoptions           []appstandards.Adoption `json:"before_adoptions"`
	AfterAdoptions            []appstandards.Adoption `json:"after_adoptions"`
	BaseSettings              appstandards.Settings   `json:"base_settings"`
	LocalSettings             appstandards.Settings   `json:"local_settings"`
	AdditionalLogDestinations []string                `json:"additional_log_destinations"`
	Effective                 appstandards.Effective  `json:"effective"`
	ChangedFields             []appstandards.Field    `json:"changed_fields"`
}

type ApplicationStandardReviewPlan struct {
	ID           string                             `json:"id"`
	OrgID        string                             `json:"org_id"`
	CreatedBy    string                             `json:"created_by"`
	Request      ApplicationStandardReviewRequest   `json:"request"`
	ApprovalHash string                             `json:"approval_hash"`
	Applications []ApplicationStandardReviewedApp   `json:"applications"`
	Blockers     []ApplicationStandardReviewBlocker `json:"blockers"`
	CreatedAt    time.Time                          `json:"created_at"`
	ExpiresAt    time.Time                          `json:"expires_at"`
	// Only hashes of private snapshot inputs are retained in this proof.
	approvalInputs appstandards.ApprovalInputs
}

type ApplicationStandardReviewStore interface {
	PreviewApplicationStandardAssignment(context.Context, string, string, ApplicationStandardReviewRequest) (ApplicationStandardReviewPlan, error)
	GetApplicationStandardReviewPlan(context.Context, string, string) (ApplicationStandardReviewPlan, error)
	ValidateApplicationStandardReview(context.Context, string, string, string, string) (ApplicationStandardReviewPlan, error)
}

// Snapshot types are deliberately separate from App/Deployment: using those
// large records would accidentally expose credentials or make heartbeats stale
// a review. SQL reads all inputs in one MVCC statement, including empty scopes.
type standardReviewSnapshot struct {
	OrgID           string                      `json:"org_id"`
	OrgPlan         api.Plan                    `json:"org_plan"`
	OrgStatus       string                      `json:"org_status"`
	DeletedPending  bool                        `json:"deleted_pending"`
	ActorAuthorized bool                        `json:"actor_authorized"`
	ScopeOwned      bool                        `json:"scope_owned"`
	ScopeOwnerID    string                      `json:"scope_owner_id"`
	Assignments     []standardReviewAssignment  `json:"assignments"`
	Versions        []standardReviewVersion     `json:"versions"`
	Destinations    []standardReviewResource    `json:"destinations"`
	Publishers      []standardReviewResource    `json:"publishers"`
	Applications    []standardReviewAppSnapshot `json:"applications"`
}
type standardReviewAssignment struct {
	appstandards.Assignment
	Revision int64 `json:"revision"`
	Active   bool  `json:"active"`
}
type standardReviewVersion struct {
	StandardID     string          `json:"standard_id"`
	Version        int64           `json:"version"`
	Definition     json.RawMessage `json:"definition"`
	DefinitionHash string          `json:"definition_hash"`
}
type standardReviewResource struct {
	ID          string `json:"id"`
	ConfigHash  string `json:"config_hash,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}
type standardReviewAppSnapshot struct {
	AppID                       string                           `json:"app_id"`
	OrgID                       string                           `json:"org_id"`
	ProjectID                   string                           `json:"project_id"`
	AccountID                   string                           `json:"account_id"`
	Slug                        string                           `json:"slug"`
	Status                      string                           `json:"status"`
	Type                        string                           `json:"type"`
	WorkloadClass               string                           `json:"workload_class"`
	AccountPlan                 api.Plan                         `json:"account_plan"`
	AccountStatus               string                           `json:"account_status"`
	AccountEgressAllowlistExtra int                              `json:"account_egress_allowlist_extra,omitempty"`
	AccountDrainCount           int                              `json:"account_drain_count"`
	Settings                    appstandards.Settings            `json:"settings"`
	HasEnrollment               bool                             `json:"has_enrollment"`
	Enrollment                  standardReviewEnrollment         `json:"enrollment"`
	Drains                      []standardReviewDrain            `json:"drains"`
	Signers                     []standardReviewSigner           `json:"signers"`
	Artifacts                   []standardReviewArtifact         `json:"artifacts"`
	Exceptions                  []ApplicationStandardException   `json:"exceptions,omitempty"`
	ArchivedResources           []standardReviewArchivedResource `json:"archived_resources"`
}
type standardReviewArchivedResource struct {
	Field       appstandards.Field `json:"field"`
	ID          string             `json:"id"`
	ConfigHash  string             `json:"config_hash"`
	Body        json.RawMessage    `json:"body,omitempty"`
	Fingerprint string             `json:"fingerprint,omitempty"`
}
type standardReviewEnrollment struct {
	MaterializedFields        []appstandards.Field    `json:"materialized_fields,omitempty"`
	OrgID                     string                  `json:"org_id"`
	ProjectID                 string                  `json:"project_id"`
	BaseSettings              appstandards.Settings   `json:"base_settings"`
	LocalSettings             appstandards.Settings   `json:"local_settings"`
	AdditionalLogDestinations []string                `json:"additional_log_destinations"`
	Adoptions                 []appstandards.Adoption `json:"adoptions"`
	DesiredRevision           int64                   `json:"desired_revision"`
	Effective                 appstandards.Effective  `json:"effective"`
	EffectiveHash             string                  `json:"effective_hash"`
}
type standardReviewDrain struct {
	ID         string `json:"id"`
	ResourceID string `json:"resource_id,omitempty"`
	Kind       string `json:"kind"`
	TargetHash string `json:"target_hash"`
	AuthHash   string `json:"auth_hash"`
	Enabled    bool   `json:"enabled"`
}
type standardReviewSigner struct {
	Name        string `json:"name"`
	ResourceID  string `json:"resource_id,omitempty"`
	Fingerprint string `json:"fingerprint"`
}
type standardReviewArtifact struct {
	ID             string                          `json:"id"`
	Scope          string                          `json:"scope"`
	Kind           string                          `json:"kind"`
	Status         string                          `json:"status"`
	ImageDigest    string                          `json:"image_digest"`
	RootfsKey      string                          `json:"rootfs_key"`
	RootfsBytes    int64                           `json:"rootfs_bytes"`
	SourceSHA256   string                          `json:"source_sha256"`
	ParkedReason   string                          `json:"parked_reason"`
	ScanStatus     string                          `json:"scan_status"`
	ScanResultHash string                          `json:"scan_result_hash"`
	SidecarHash    string                          `json:"sidecar_hash"`
	Security       *standardReviewArtifactSecurity `json:"security,omitempty"`
}

func prepareStandardReview(orgID, actorID string, r ApplicationStandardReviewRequest) (ApplicationStandardReviewRequest, error) {
	if !validStandardResourceRead(orgID, actorID) || !validStandardResourceRead(r.ScopeID, r.StandardID) ||
		!slices.Contains([]string{"organization", "project", "application"}, r.Scope) ||
		r.AdmissionVersion < 1 || r.AdmissionVersion > api.ApplicationStandardMaxVersion ||
		r.ExpectedRevision < 0 || r.ExpectedRevision >= api.ApplicationStandardMaxVersion || r.BatchSize < 1 || r.BatchSize > api.ApplicationStandardMaxRolloutBatch ||
		(r.Scope == "organization" && !sameStandardUUID(orgID, r.ScopeID)) {
		return r, ErrInvalidArgument
	}
	if r.AssignmentID == "" {
		if r.ExpectedRevision != 0 || !r.Active {
			return r, ErrInvalidArgument
		}
		r.AssignmentID = uuid.NewString()
	}
	if !validStandardResourceRead(orgID, r.AssignmentID) {
		return r, ErrInvalidArgument
	}
	if r.ExpectedRevision == 0 && !r.Active {
		return r, ErrInvalidArgument
	}
	r.AssignmentID, r.ScopeID, r.StandardID = canonicalStandardUUID(r.AssignmentID), canonicalStandardUUID(r.ScopeID), canonicalStandardUUID(r.StandardID)
	return r, nil
}

func canonicalStandardUUID(raw string) string { id, _ := uuid.Parse(raw); return id.String() }

func standardReviewDigest(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return standardReviewBytesDigest(raw), nil
}
func standardReviewBytesDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func buildStandardReview(snapshot standardReviewSnapshot, request ApplicationStandardReviewRequest, id, actorID string, now time.Time) (ApplicationStandardReviewPlan, error) {
	var normalizeErr error
	snapshot, normalizeErr = normalizeStandardReviewSnapshot(snapshot)
	if normalizeErr != nil {
		return ApplicationStandardReviewPlan{}, normalizeErr
	}
	p := ApplicationStandardReviewPlan{ID: id, OrgID: snapshot.OrgID, CreatedBy: canonicalStandardUUID(actorID), Request: request, Applications: []ApplicationStandardReviewedApp{}, Blockers: []ApplicationStandardReviewBlocker{}, CreatedAt: now, ExpiresAt: now.Add(api.ApplicationStandardReviewTTL)}
	if snapshot.DeletedPending || !snapshot.ScopeOwned {
		return p, ErrNotFound
	}
	if !snapshot.ActorAuthorized {
		return p, ErrApplicationStandardReviewForbidden
	}
	if snapshot.OrgStatus != string(OrgStatusActive) {
		return p, ErrConflict
	}
	target := appstandards.Assignment{ID: request.AssignmentID, OrgID: snapshot.OrgID, Scope: request.Scope, ScopeID: request.ScopeID, StandardID: request.StandardID, AdmissionVersion: request.AdmissionVersion}
	before, after, relevant, err := standardReviewAssignments(snapshot, target, request)
	if err != nil {
		return p, err
	}
	versions, candidate, err := standardReviewVersions(snapshot, target)
	if err != nil {
		return p, err
	}
	usedVersions := map[appstandards.VersionKey]bool{{StandardID: target.StandardID, Version: target.AdmissionVersion}: true}
	resources := map[appstandards.Field]map[string]bool{appstandards.LogDestinations: {}, appstandards.TrustedPublishers: {}}
	markStandardReviewResources(resources, candidate)
	admissionBlockers, err := reviewStandardAdmissions(snapshot, before, after, request, versions, usedVersions, resources, now)
	if err != nil {
		return p, err
	}
	p.Blockers = append(p.Blockers, admissionBlockers...)
	approvalApps := []appstandards.ApplicationApprovalInput{}
	accountDelta := map[string]int{}
	for _, app := range snapshot.Applications {
		if !app.HasEnrollment || app.Enrollment.DesiredRevision < 1 || !sameStandardUUID(app.Enrollment.OrgID, app.OrgID) || (app.ProjectID != "" || app.Enrollment.ProjectID != "") && !sameStandardUUID(app.ProjectID, app.Enrollment.ProjectID) {
			return p, fmt.Errorf("application has no valid standards enrollment")
		}
		owned := appstandards.ApplicationOwnership{ID: app.AppID, OrgID: app.OrgID, ProjectID: app.ProjectID}
		prior, err := appstandards.SelectAdopted(owned, before, app.Enrollment.Adoptions, versions, api.ApplicationStandardResolverLimits())
		if err != nil {
			return p, fmt.Errorf("select reviewed application requirements: %w", err)
		}
		pins := slices.DeleteFunc(append([]appstandards.Adoption{}, prior.Adoptions...), func(pin appstandards.Adoption) bool { return pin.AssignmentID == target.ID })
		if request.Active {
			pins = append(pins, appstandards.Adoption{AssignmentID: target.ID, Version: target.AdmissionVersion})
		}
		next, err := appstandards.SelectAdopted(owned, after, pins, versions, api.ApplicationStandardResolverLimits())
		if err != nil {
			return p, fmt.Errorf("select proposed application requirements: %w", err)
		}
		for _, selected := range []appstandards.Selection{prior, next} {
			for _, layer := range selected.Layers {
				usedVersions[appstandards.VersionKey{StandardID: layer.StandardID, Version: layer.Version}] = true
				markStandardReviewResources(resources, layer.Definition)
			}
		}
		reviewed, err := resolveStandardReviewedApp(app, prior, next, now)
		if err != nil {
			return p, err
		}
		appHash, err := standardReviewDigest(app)
		if err != nil {
			return p, err
		}
		effectiveHash, err := standardReviewDigest(reviewed.Effective)
		if err != nil {
			return p, err
		}
		approvalApps = append(approvalApps, appstandards.ApplicationApprovalInput{AppID: app.AppID, OrgID: app.OrgID, ProjectID: app.ProjectID, DesiredRevision: app.Enrollment.DesiredRevision, SnapshotHash: appHash, BeforeAdoptions: prior.Adoptions, AfterAdoptions: next.Adoptions, EffectiveHash: effectiveHash})
		p.Applications = append(p.Applications, reviewed)
		p.Blockers = append(p.Blockers, bindStandardReviewAppResources(snapshot, app, reviewed, resources)...)
		p.Blockers = append(p.Blockers, standardReviewAppBlockers(app, reviewed)...)
		p.Blockers = append(p.Blockers, standardReviewArtifactBlockers(snapshot.Publishers, app, reviewed, now)...)
		p.ExpiresAt = standardReviewArtifactExpiry(p.ExpiresAt, app, reviewed)
		if deadline := standardEffectiveExceptionExpiry(reviewed.Effective, app.Exceptions); deadline != nil && deadline.Before(p.ExpiresAt) {
			p.ExpiresAt = *deadline
		}
		if _, managed := reviewed.Effective.Sources[appstandards.LogDestinations]; managed || len(prior.Layers) != 0 {
			accountDelta[app.AccountID] += len(standardReviewStrings(reviewed.Effective.Values[appstandards.LogDestinations])) - len(app.Drains)
		}
	}
	for _, app := range snapshot.Applications {
		if app.AccountDrainCount+accountDelta[app.AccountID] > app.AccountPlan.LogDrainPerAccount() && accountDelta[app.AccountID] > 0 {
			p.Blockers = append(p.Blockers, ApplicationStandardReviewBlocker{AppID: app.AppID, Field: appstandards.LogDestinations, Code: "plan_log_drain_account_limit"})
		}
	}
	scopeHash, err := standardReviewScopeHash(snapshot, relevant, usedVersions, resources)
	if err != nil {
		return p, err
	}
	p.approvalInputs = appstandards.ApprovalInputs{PlanID: id, OrgID: p.OrgID, CreatedBy: p.CreatedBy, ScopeSnapshotHash: scopeHash, Assignment: target, ExpectedRevision: request.ExpectedRevision, Active: request.Active, BatchSize: request.BatchSize, Applications: approvalApps}
	p.ApprovalHash, err = appstandards.HashApproval(p.approvalInputs)
	return p, err
}

func standardReviewAssignments(s standardReviewSnapshot, target appstandards.Assignment, r ApplicationStandardReviewRequest) ([]appstandards.Assignment, []appstandards.Assignment, []standardReviewAssignment, error) {
	before, after, relevant := []appstandards.Assignment{}, []appstandards.Assignment{}, []standardReviewAssignment{}
	found := false
	for _, a := range s.Assignments {
		if a.ID == target.ID {
			found = true
			if a.Revision != r.ExpectedRevision || a.OrgID != target.OrgID || a.Scope != target.Scope || a.ScopeID != target.ScopeID || a.StandardID != target.StandardID {
				return nil, nil, nil, ErrConflict
			}
		}
		if a.ID != target.ID && a.Scope == target.Scope && a.ScopeID == target.ScopeID && a.StandardID == target.StandardID {
			return nil, nil, nil, ErrConflict
		}
		before = append(before, a.Assignment)
		if a.ID != target.ID {
			after = append(after, a.Assignment)
		}
		matches := a.ID == target.ID || a.Active && a.Scope == "organization"
		for _, app := range s.Applications {
			matches = matches || a.Active && (a.Scope == "application" && a.ScopeID == app.AppID || a.Scope == "project" && a.ScopeID == app.ProjectID)
			for _, pin := range app.Enrollment.Adoptions {
				matches = matches || pin.AssignmentID == a.ID
			}
		}
		if a.Active && a.Scope == "project" && (r.Scope == "organization" || r.Scope == "project" && a.ScopeID == r.ScopeID) {
			matches = true
		}
		if matches {
			relevant = append(relevant, a)
		}
	}
	if found == (r.ExpectedRevision == 0) {
		return nil, nil, nil, ErrConflict
	}
	if r.Active {
		after = append(after, target)
	}
	slices.SortFunc(relevant, func(a, b standardReviewAssignment) int { return strings.Compare(a.ID, b.ID) })
	return before, after, relevant, nil
}

func standardReviewVersions(s standardReviewSnapshot, target appstandards.Assignment) (map[appstandards.VersionKey]appstandards.PublishedVersion, appstandards.Definition, error) {
	versions, err := standardSnapshotVersions(s)
	if err != nil {
		return nil, nil, err
	}
	var candidate appstandards.Definition
	if selected, exists := versions[appstandards.VersionKey{StandardID: target.StandardID, Version: target.AdmissionVersion}]; exists {
		candidate = selected.Definition
	}

	if candidate == nil {
		return nil, nil, ErrNotFound
	}
	return versions, candidate, nil
}

// Existing application pins can hide conflicts in the admission versions that
// new services will inherit. Review those combinations as well, including empty
// projects. Entitlements remain per-app: a template cannot assume a future
// creator's account plan merely from the organization's billing plan.
func reviewStandardAdmissions(s standardReviewSnapshot, before, after []appstandards.Assignment, r ApplicationStandardReviewRequest, versions map[appstandards.VersionKey]appstandards.PublishedVersion, used map[appstandards.VersionKey]bool, refs map[appstandards.Field]map[string]bool, now time.Time) ([]ApplicationStandardReviewBlocker, error) {
	blockers := []ApplicationStandardReviewBlocker{}
	if r.Scope == "application" {
		return blockers, nil
	}
	active := map[string]bool{}
	for _, assignment := range s.Assignments {
		active[assignment.ID] = assignment.Active
	}
	projects := []string{}
	if r.Scope == "project" {
		projects = append(projects, r.ScopeID)
	} else {
		projects = append(projects, "")
		for _, assignment := range after {
			if assignment.Scope == "project" && active[assignment.ID] {
				projects = append(projects, assignment.ScopeID)
			}
		}
	}
	slices.Sort(projects)
	projects = slices.Compact(projects)
	// Application-only assignments have no authority over a future service.
	before = slices.DeleteFunc(append([]appstandards.Assignment{}, before...), func(a appstandards.Assignment) bool { return a.Scope == "application" || !active[a.ID] })
	after = slices.DeleteFunc(append([]appstandards.Assignment{}, after...), func(a appstandards.Assignment) bool {
		return a.Scope == "application" || a.ID != r.AssignmentID && !active[a.ID]
	})
	for _, projectID := range projects {
		owned := appstandards.ApplicationOwnership{ID: r.AssignmentID, OrgID: s.OrgID, ProjectID: projectID}
		prior, err := appstandards.Select(owned, before, nil, versions, api.ApplicationStandardResolverLimits())
		if err != nil {
			return nil, err
		}
		next, err := appstandards.Select(owned, after, nil, versions, api.ApplicationStandardResolverLimits())
		if err != nil {
			return nil, err
		}
		for _, selected := range []appstandards.Selection{prior, next} {
			for _, layer := range selected.Layers {
				used[appstandards.VersionKey{StandardID: layer.StandardID, Version: layer.Version}] = true
				markStandardReviewResources(refs, layer.Definition)
			}
		}
		effective, err := appstandards.Resolve(applicationStandardBaseSettings(App{}), next.Layers, nil, nil, now, api.ApplicationStandardResolverLimits())
		if err != nil {
			return nil, err
		}
		scope, scopeID := "organization", s.OrgID
		if projectID != "" {
			scope, scopeID = "project", projectID
		}
		add := func(field appstandards.Field, code string) {
			blockers = append(blockers, ApplicationStandardReviewBlocker{Scope: scope, ScopeID: scopeID, Field: field, Code: code})
		}
		for _, violation := range effective.Violations {
			add(violation.Field, violation.Code)
		}
		var signed bool
		_ = json.Unmarshal(effective.Values[appstandards.RequireSigned], &signed)
		var posture api.AppSecurityPolicy
		_ = json.Unmarshal(effective.Values[appstandards.SecurityPolicy], &posture)
		if (signed || posture.RequiresSignedImage()) && len(standardReviewStrings(effective.Values[appstandards.TrustedPublishers])) == 0 {
			add(appstandards.TrustedPublishers, "trusted_publisher_required")
		}
		var ports []int
		_ = json.Unmarshal(effective.Values[appstandards.EgressExtraPorts], &ports)
		for _, port := range ports {
			if _, forbidden := api.TenantEgressForbiddenPort(port); forbidden {
				add(appstandards.EgressExtraPorts, "platform_egress_port_forbidden")
				break
			}
		}
	}
	return blockers, nil
}

func markStandardReviewResources(resources map[appstandards.Field]map[string]bool, d appstandards.Definition) {
	for field, ids := range resources {
		for _, id := range standardReviewStrings(d[field].Value) {
			ids[id] = true
		}
	}
}

func bindStandardReviewAppResources(s standardReviewSnapshot, before standardReviewAppSnapshot, app ApplicationStandardReviewedApp, refs map[appstandards.Field]map[string]bool) []ApplicationStandardReviewBlocker {
	out := []ApplicationStandardReviewBlocker{}
	available := map[appstandards.Field]map[string]bool{appstandards.LogDestinations: {}, appstandards.TrustedPublishers: {}}
	for _, d := range s.Destinations {
		available[appstandards.LogDestinations][d.ID] = true
	}
	for _, p := range s.Publishers {
		available[appstandards.TrustedPublishers][p.ID] = true
	}
	for field, owned := range available {
		legacy := standardReviewStrings(app.BeforeSettings[field])
		for _, archived := range before.ArchivedResources {
			if archived.Field == field {
				legacy = append(legacy, archived.ID)
			}
		}
		missing := false
		for _, id := range standardReviewStrings(app.Effective.Values[field]) {
			if owned[id] {
				refs[field][id] = true
			} else if !slices.Contains(legacy, id) {
				missing = true
			}
		}
		if missing {
			out = append(out, ApplicationStandardReviewBlocker{AppID: app.AppID, Field: field, Code: "application_standard_resource_not_available"})
		}
	}
	slices.SortFunc(out, func(a, b ApplicationStandardReviewBlocker) int {
		return strings.Compare(string(a.Field), string(b.Field))
	})
	return out
}

func standardReviewScopeHash(s standardReviewSnapshot, assignments []standardReviewAssignment, used map[appstandards.VersionKey]bool, refs map[appstandards.Field]map[string]bool) (string, error) {
	// Candidate publication and unrelated organization resources must not stale
	// an approval. Bind only selected versions and their immutable resources.
	versions := []standardReviewVersion{}
	for _, v := range s.Versions {
		if used[appstandards.VersionKey{StandardID: v.StandardID, Version: v.Version}] {
			definition, _, err := appstandards.Parse(v.Definition, api.ApplicationStandardResolverLimits())
			if err != nil {
				return "", err
			}
			v.Definition, _ = json.Marshal(definition)
			versions = append(versions, v)
		}
	}
	slices.SortFunc(versions, func(a, b standardReviewVersion) int {
		if a.StandardID != b.StandardID {
			return strings.Compare(a.StandardID, b.StandardID)
		}
		return int(a.Version - b.Version)
	})
	resources := []standardReviewResource{}
	for _, resource := range s.Destinations {
		if refs[appstandards.LogDestinations][resource.ID] {
			resources = append(resources, resource)
			delete(refs[appstandards.LogDestinations], resource.ID)
		}
	}
	for _, resource := range s.Publishers {
		if refs[appstandards.TrustedPublishers][resource.ID] {
			resources = append(resources, resource)
			delete(refs[appstandards.TrustedPublishers], resource.ID)
		}
	}
	for _, missing := range refs {
		if len(missing) != 0 {
			return "", fmt.Errorf("reviewed standard resource is absent from its organization")
		}
	}
	slices.SortFunc(resources, func(a, b standardReviewResource) int { return strings.Compare(a.ID, b.ID) })
	members := []string{}
	for _, app := range s.Applications {
		members = append(members, app.AppID)
	}
	slices.Sort(members)
	return standardReviewDigest(struct {
		OrgID        string                     `json:"org_id"`
		ScopeOwnerID string                     `json:"scope_owner_id"`
		Plan         api.Plan                   `json:"plan"`
		Status       string                     `json:"status"`
		Assignments  []standardReviewAssignment `json:"assignments"`
		Versions     []standardReviewVersion    `json:"versions"`
		Resources    []standardReviewResource   `json:"resources"`
		Members      []string                   `json:"members"`
	}{s.OrgID, s.ScopeOwnerID, s.OrgPlan, s.OrgStatus, assignments, versions, resources, members})
}

func standardReviewStrings(raw json.RawMessage) []string {
	var out []string
	_ = json.Unmarshal(raw, &out)
	return out
}

func resolveStandardReviewedApp(app standardReviewAppSnapshot, prior, next appstandards.Selection, now time.Time) (ApplicationStandardReviewedApp, error) {
	current := cloneStandardSettings(app.Settings)
	logIDs, signerIDs := []string{}, []string{}
	for _, d := range app.Drains {
		id := d.ID
		if d.ResourceID != "" {
			id = d.ResourceID
		}
		logIDs = append(logIDs, id)
	}
	for _, signer := range app.Signers {
		id := signer.ResourceID
		if id == "" {
			id = standardLegacySignerID(app.AppID, signer.Name, signer.Fingerprint)
		}
		signerIDs = append(signerIDs, id)
	}
	slices.Sort(logIDs)
	slices.Sort(signerIDs)
	current[appstandards.LogDestinations], _ = json.Marshal(logIDs)
	current[appstandards.TrustedPublishers], _ = json.Marshal(signerIDs)
	base, local := cloneStandardSettings(current), cloneStandardSettings(app.Enrollment.LocalSettings)
	managed := map[appstandards.Field]bool{}
	for _, field := range app.Enrollment.MaterializedFields {
		managed[field] = true
		if original, exists := app.Enrollment.BaseSettings[field]; exists {
			base[field] = append(json.RawMessage{}, original...)
		}
	}
	defaults := applicationStandardBaseSettings(App{})
	defaults[appstandards.LogDestinations], defaults[appstandards.TrustedPublishers] = json.RawMessage(`[]`), json.RawMessage(`[]`)
	constrained := map[appstandards.Field]bool{}
	logExtend, logExact := false, false
	for _, layer := range next.Layers {
		for field, rule := range layer.Definition {
			if rule.Mode != appstandards.Default {
				constrained[field] = true
				if field == appstandards.LogDestinations {
					logExtend = logExtend || rule.Override == appstandards.Extend
					logExact = logExact || rule.Override != appstandards.Extend
				}
			}
		}
	}
	// Legacy explicit settings remain local intent when a field first receives
	// a default. Mandatory fields can replace them, with the change visible.
	for _, layer := range next.Layers {
		for field, rule := range layer.Definition {
			if !managed[field] && !constrained[field] && rule.Mode == appstandards.Default && string(current[field]) != string(defaults[field]) {
				if _, exists := local[field]; !exists {
					local[field] = append(json.RawMessage{}, current[field]...)
				}
			}
		}
	}
	effective, err := appstandards.Resolve(base, next.Layers, local, standardResolverExceptions(app.Exceptions, now), now, api.ApplicationStandardResolverLimits())
	if err != nil {
		return ApplicationStandardReviewedApp{}, fmt.Errorf("resolve reviewed application: %w", err)
	}
	additional := append([]string{}, app.Enrollment.AdditionalLogDestinations...)
	if !managed[appstandards.LogDestinations] && logExtend && !logExact {
		additional = append(additional, logIDs...)
	}
	slices.Sort(additional)
	additional = slices.Compact(additional)
	if len(additional) > 0 {
		extras := append(standardReviewStrings(effective.Values[appstandards.LogDestinations]), additional...)
		slices.Sort(extras)
		extras = slices.Compact(extras)
		withExtras := cloneStandardSettings(local)
		withExtras[appstandards.LogDestinations], _ = json.Marshal(extras)
		effective, err = appstandards.Resolve(base, next.Layers, withExtras, standardResolverExceptions(app.Exceptions, now), now, api.ApplicationStandardResolverLimits())
		if err != nil {
			return ApplicationStandardReviewedApp{}, err
		}
	}
	changes := []appstandards.Field{}
	for field, value := range effective.Values {
		if string(value) != string(current[field]) {
			changes = append(changes, field)
		}
	}
	slices.Sort(changes)
	return ApplicationStandardReviewedApp{AppID: app.AppID, AccountID: app.AccountID, Slug: app.Slug, ProjectID: app.ProjectID, DesiredRevision: app.Enrollment.DesiredRevision, BeforeSettings: current, BeforeAdoptions: prior.Adoptions, AfterAdoptions: next.Adoptions, BaseSettings: base, LocalSettings: local, AdditionalLogDestinations: additional, Effective: effective, ChangedFields: changes}, nil
}

func cloneStandardSettings(in appstandards.Settings) appstandards.Settings {
	out := appstandards.Settings{}
	for field, value := range in {
		out[field] = append(json.RawMessage{}, value...)
	}
	return out
}

func standardReviewAppBlockers(app standardReviewAppSnapshot, reviewed ApplicationStandardReviewedApp) []ApplicationStandardReviewBlocker {
	out := []ApplicationStandardReviewBlocker{}
	add := func(field appstandards.Field, code string) {
		out = append(out, ApplicationStandardReviewBlocker{AppID: app.AppID, Field: field, Code: code})
	}
	if app.AccountStatus != string(AccountActive) {
		add("", "account_not_active")
	}
	limits, valid := api.LimitsFor(app.AccountPlan)
	if !valid {
		add("", "plan_not_supported")
		return out
	}
	for _, violation := range reviewed.Effective.Violations {
		add(violation.Field, violation.Code)
	}
	logs := standardReviewStrings(reviewed.Effective.Values[appstandards.LogDestinations])
	if len(logs) > limits.LogDrainPerApp && slices.Contains(reviewed.ChangedFields, appstandards.LogDestinations) {
		add(appstandards.LogDestinations, "plan_log_drain_app_limit")
	}
	ranges := standardReviewStrings(reviewed.Effective.Values[appstandards.EgressCIDRs])
	if len(ranges) > 0 && !limits.EgressAllowlistAllowed {
		add(appstandards.EgressCIDRs, "plan_egress_allowlist_not_allowed")
	}
	if len(ranges) > limits.EgressAllowlistMaxSize+app.AccountEgressAllowlistExtra {
		add(appstandards.EgressCIDRs, "plan_egress_allowlist_limit")
	}
	var ports []int
	_ = json.Unmarshal(reviewed.Effective.Values[appstandards.EgressExtraPorts], &ports)
	if len(ports) > limits.EgressExtraPortsMax {
		add(appstandards.EgressExtraPorts, "plan_egress_extra_ports_limit")
	}
	for _, port := range ports {
		if _, forbidden := api.TenantEgressForbiddenPort(port); forbidden {
			add(appstandards.EgressExtraPorts, "platform_egress_port_forbidden")
			break
		}
	}
	var signed bool
	_ = json.Unmarshal(reviewed.Effective.Values[appstandards.RequireSigned], &signed)
	var posture api.AppSecurityPolicy
	_ = json.Unmarshal(reviewed.Effective.Values[appstandards.SecurityPolicy], &posture)
	signed = signed || posture.RequiresSignedImage()
	publishers := standardReviewStrings(reviewed.Effective.Values[appstandards.TrustedPublishers])
	if len(publishers) > limits.TrustedSignerCountMax {
		add(appstandards.TrustedPublishers, "plan_trusted_publisher_limit")
	}
	if signed && len(publishers) == 0 {
		add(appstandards.TrustedPublishers, "trusted_publisher_required")
	}
	return out
}

func validateStandardReviewHash(saved, fresh ApplicationStandardReviewPlan, expected string, now time.Time) error {
	if !now.Before(saved.ExpiresAt) {
		return ErrApplicationStandardReviewExpired
	}
	if expected != saved.ApprovalHash || fresh.ApprovalHash != saved.ApprovalHash {
		return ErrApplicationStandardReviewStale
	}
	if len(fresh.Blockers) != 0 {
		return ErrApplicationStandardReviewBlocked
	}
	return nil
}

func cloneStandardReviewPlan(in ApplicationStandardReviewPlan) ApplicationStandardReviewPlan {
	raw, _ := json.Marshal(in)
	var out ApplicationStandardReviewPlan
	_ = json.Unmarshal(raw, &out)
	proof, _ := json.Marshal(in.approvalInputs)
	_ = json.Unmarshal(proof, &out.approvalInputs)
	return out
}

func appStandardReviewProofHash(p ApplicationStandardReviewPlan) (string, error) {
	proof := p.approvalInputs
	a := proof.Assignment
	r := p.Request
	if proof.PlanID != p.ID || proof.OrgID != p.OrgID || proof.CreatedBy != p.CreatedBy ||
		a.ID != r.AssignmentID || a.OrgID != p.OrgID || a.Scope != r.Scope || a.ScopeID != r.ScopeID ||
		a.StandardID != r.StandardID || a.AdmissionVersion != r.AdmissionVersion ||
		proof.ExpectedRevision != r.ExpectedRevision || proof.Active != r.Active || proof.BatchSize != r.BatchSize {
		return "", fmt.Errorf("stored standard review intent differs from its proof")
	}
	return appstandards.HashApproval(proof)
}

func standardReviewFreshnessError(err error) error {
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) {
		return ErrApplicationStandardReviewStale
	}
	return err
}

func normalizeStandardReviewSnapshot(s standardReviewSnapshot) (standardReviewSnapshot, error) {
	// Round-trip first: normalization never changes a caller's preview inputs.
	raw, err := json.Marshal(s)
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return s, err
	}
	for i := range s.Applications {
		a := &s.Applications[i]
		if err := normalizeStandardExceptions(a); err != nil {
			return s, err
		}
		if !standardMaterializedFieldsValid(a.Enrollment.MaterializedFields) {
			return s, fmt.Errorf("invalid materialized field context")
		}
		slices.Sort(a.Enrollment.MaterializedFields)
		a.Enrollment.MaterializedFields = slices.Compact(a.Enrollment.MaterializedFields)
		for j := range a.Enrollment.Adoptions {
			pin := &a.Enrollment.Adoptions[j]
			if !validStandardResourceRead(s.OrgID, pin.AssignmentID) {
				return s, fmt.Errorf("invalid reviewed adoption identity")
			}
			pin.AssignmentID = canonicalStandardUUID(pin.AssignmentID)
		}
		for j, id := range a.Enrollment.AdditionalLogDestinations {
			if !validStandardResourceRead(s.OrgID, id) {
				return s, fmt.Errorf("invalid reviewed additional destination identity")
			}
			a.Enrollment.AdditionalLogDestinations[j] = canonicalStandardUUID(id)
		}
		for _, settings := range []struct {
			input  appstandards.Settings
			target *appstandards.Settings
		}{{a.Settings, &a.Settings}, {a.Enrollment.BaseSettings, &a.Enrollment.BaseSettings}, {a.Enrollment.LocalSettings, &a.Enrollment.LocalSettings}} {
			normalized, err := appstandards.Resolve(settings.input, nil, nil, nil, time.Time{}, api.ApplicationStandardResolverLimits())
			if err != nil {
				return s, fmt.Errorf("invalid reviewed application settings: %w", err)
			}
			*settings.target = normalized.Values
		}
		if a.ArchivedResources == nil {
			a.ArchivedResources = []standardReviewArchivedResource{}
		}
		slices.SortFunc(a.ArchivedResources, func(a, b standardReviewArchivedResource) int {
			return strings.Compare(string(a.Field)+a.ID, string(b.Field)+b.ID)
		})
		// Metadata may be ordered differently by memory fixtures and PostgreSQL.
		slices.SortFunc(a.Drains, func(a, b standardReviewDrain) int { return strings.Compare(a.ID, b.ID) })
		slices.SortFunc(a.Signers, func(a, b standardReviewSigner) int { return strings.Compare(a.Name, b.Name) })
		slices.SortFunc(a.Artifacts, func(a, b standardReviewArtifact) int { return strings.Compare(a.ID, b.ID) })
		slices.SortFunc(a.Enrollment.Adoptions, func(a, b appstandards.Adoption) int { return strings.Compare(a.AssignmentID, b.AssignmentID) })
		slices.Sort(a.Enrollment.AdditionalLogDestinations)
	}
	slices.SortFunc(s.Applications, func(a, b standardReviewAppSnapshot) int { return strings.Compare(a.AppID, b.AppID) })
	return s, nil
}
