package state

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
)

var (
	ErrApplicationStandardLeaseLost      = errors.New("state: application standard worker lease lost")
	ErrApplicationStandardManagedControl = errors.New("state: application standard manages this control")
)

// Private worker authority never appears in customer operation responses.
// A claim is fencing authority, not evidence of runtime convergence.
type ApplicationStandardWorkerClaim struct {
	OperationID string
	OrgID       string
	Owner       string
	Generation  int64
	Until       time.Time
}

type ApplicationStandardMaterializationStore interface {
	ClaimApplicationStandardOperation(context.Context, string) (ApplicationStandardWorkerClaim, error)
	MaterializeNextApplicationStandardTarget(context.Context, ApplicationStandardWorkerClaim) (ApplicationStandardOperation, error)
}

type standardControlBinding struct {
	AppID      string
	Field      appstandards.Field
	ResourceID string
	PhysicalID string
}

type standardControlBackup struct {
	AppID      string
	Field      appstandards.Field
	ID         string
	Body       json.RawMessage
	ConfigHash string
}

type standardControlProjection struct {
	Drains         []AppLogDrain
	Signers        []AppTrustedSigner
	Bindings       []standardControlBinding
	Backups        []standardControlBackup
	RequireSigned  bool
	SecurityPolicy api.AppSecurityPolicy
	CIDRs          []netip.Prefix
	Ports          []int
}

func standardLegacySignerID(appID, name, fingerprint string) string {
	return uuid.NewSHA1(uuid.MustParse(appID), []byte("gregale.legacy-signer\x00"+name+"\x00"+fingerprint)).String()
}

func standardBindingKey(appID string, field appstandards.Field, id string) string {
	return canonicalStandardUUID(appID) + "\x00" + string(field) + "\x00" + id
}

func standardWorkerOwnerValid(owner string) bool {
	return owner != "" && len(owner) <= api.ApplicationStandardMaxWorkerOwnerBytes && utf8.ValidString(owner) && strings.TrimSpace(owner) == owner && !strings.ContainsAny(owner, "\x00\r\n")
}

func standardWorkerClaimValid(c ApplicationStandardWorkerClaim) bool {
	return validStandardResourceRead(c.OrgID, c.OperationID) && standardWorkerOwnerValid(c.Owner) && c.Generation > 0
}

// Only the current wave is eligible. Saved settings never unlock a later wave.
func standardNextTarget(o ApplicationStandardOperation) int {
	first := -1
	for i, t := range o.Targets {
		if t.State != "observed" && t.State != "skipped" {
			first = i
			break
		}
	}
	if first < 0 {
		return -1
	}
	end := min((first/o.BatchSize+1)*o.BatchSize, len(o.Targets))
	for i := first; i < end; i++ {
		if o.Targets[i].State == "blocked" {
			return -1
		}
		if o.Targets[i].State == "queued" {
			return i
		}
	}
	return -1
}

func standardProjectionOperationState(o ApplicationStandardOperation) string {
	for _, t := range o.Targets {
		if t.State != "observed" && t.State != "skipped" {
			if standardNextTarget(o) >= 0 {
				return "running"
			}
			return "waiting"
		}
	}
	return "completed"
}

func standardMaterializationInput(s standardReviewSnapshot, o ApplicationStandardOperation, t ApplicationStandardOperationTarget, r ApplicationStandardReviewRequest, now time.Time) (standardReviewAppSnapshot, error) {
	var app standardReviewAppSnapshot
	var err error
	s, err = normalizeStandardReviewSnapshot(s)
	if err != nil {
		return app, err
	}
	if s.DeletedPending || s.OrgStatus != string(OrgStatusActive) || !s.ScopeOwned || len(s.Applications) != 1 {
		return app, ErrApplicationStandardReviewStale
	}
	app = s.Applications[0]
	if app.AppID != t.AppID || app.AccountID != t.ApprovedApp.AccountID || !app.HasEnrollment || app.AccountStatus != string(AccountActive) {
		return app, ErrApplicationStandardReviewStale
	}
	// A prior target in this same operation can legitimately change the shared
	// account count. Subtract only its immutable approved delta; other changes
	// still stale this target and the current quota is checked again below.
	proofApp := app
	for _, done := range o.Targets {
		if (done.State == "persisted" || done.State == "observed") && done.ApprovedApp.AccountID == app.AccountID {
			proofApp.AccountDrainCount -= len(standardReviewStrings(done.ApprovedApp.Effective.Values[appstandards.LogDestinations])) - len(standardReviewStrings(done.ApprovedApp.BeforeSettings[appstandards.LogDestinations]))
		}
	}
	hash, err := standardReviewDigest(proofApp)
	if err != nil {
		return app, err
	}
	if hash != t.approvalInput.SnapshotHash {
		return app, ErrApplicationStandardReviewStale
	}
	target := appstandards.Assignment{ID: r.AssignmentID, OrgID: o.OrgID, Scope: r.Scope, ScopeID: r.ScopeID, StandardID: r.StandardID, AdmissionVersion: r.AdmissionVersion}
	versions, _, err := standardReviewVersions(s, target)
	if err != nil {
		return app, err
	}
	assignments := make([]appstandards.Assignment, 0, len(s.Assignments))
	for _, a := range s.Assignments {
		assignments = append(assignments, a.Assignment)
	}
	ownership := appstandards.ApplicationOwnership{ID: app.AppID, OrgID: app.OrgID, ProjectID: app.ProjectID}
	prior, err := appstandards.SelectAdopted(ownership, assignments, app.Enrollment.Adoptions, versions, api.ApplicationStandardResolverLimits())
	if err != nil {
		return app, err
	}
	next, err := appstandards.SelectAdopted(ownership, assignments, t.ApprovedApp.AfterAdoptions, versions, api.ApplicationStandardResolverLimits())
	if err != nil {
		return app, err
	}
	fresh, err := resolveStandardReviewedApp(app, prior, next, now)
	if err != nil {
		return app, err
	}
	expected, err := standardReviewDigest(t.ApprovedApp)
	if err != nil {
		return app, err
	}
	actual, err := standardReviewDigest(fresh)
	if err != nil {
		return app, err
	}
	if actual != expected {
		return app, ErrApplicationStandardReviewStale
	}
	blockers := standardReviewAppBlockers(app, fresh)
	delta := len(standardReviewStrings(fresh.Effective.Values[appstandards.LogDestinations])) - len(app.Drains)
	if app.AccountDrainCount+delta > app.AccountPlan.LogDrainPerAccount() && delta > 0 {
		return app, ErrApplicationStandardReviewBlocked
	}
	if len(blockers) != 0 {
		return app, ErrApplicationStandardReviewBlocked
	}
	return app, nil
}

func buildStandardControlProjection(app standardReviewAppSnapshot, target ApplicationStandardReviewedApp, drains []AppLogDrain, signers []AppTrustedSigner, bindings []standardControlBinding, backups []standardControlBackup, destinations map[string]ApplicationStandardLogDestination, publishers map[string]api.ApplicationStandardPublisher, now time.Time) (standardControlProjection, error) {
	p := standardControlProjection{Drains: []AppLogDrain{}, Signers: []AppTrustedSigner{}, Bindings: []standardControlBinding{}, Backups: []standardControlBackup{}, CIDRs: []netip.Prefix{}, Ports: []int{}}
	projected := map[string]bool{}
	for _, b := range bindings {
		projected[string(b.Field)+"\x00"+b.PhysicalID] = true
	}
	legacyDrains, legacySigners := map[string]AppLogDrain{}, map[string]AppTrustedSigner{}
	for _, backup := range backups {
		switch backup.Field {
		case appstandards.LogDestinations:
			var d AppLogDrain
			if err := json.Unmarshal(backup.Body, &d); err != nil {
				return p, err
			}
			raw, _ := json.Marshal(d)
			if standardReviewBytesDigest(raw) != backup.ConfigHash || !sameStandardUUID(d.AppID, app.AppID) || !sameStandardUUID(d.AccountID, app.AccountID) || !sameStandardUUID(d.ID, backup.ID) {
				return p, fmt.Errorf("invalid private drain backup")
			}
			legacyDrains[canonicalStandardUUID(d.ID)] = d
		case appstandards.TrustedPublishers:
			var signer AppTrustedSigner
			if err := json.Unmarshal(backup.Body, &signer); err != nil {
				return p, err
			}
			raw, _ := json.Marshal(signer)
			if standardReviewBytesDigest(raw) != backup.ConfigHash || !sameStandardUUID(signer.AppID, app.AppID) || !sameStandardUUID(signer.AccountID, app.AccountID) || standardLegacySignerID(app.AppID, signer.SignerName, standardReviewBytesDigest(signer.CosignPublicKey)) != backup.ID {
				return p, fmt.Errorf("invalid private signer backup")
			}
			legacySigners[backup.ID] = signer
		}
	}
	for _, d := range drains {
		if projected[string(appstandards.LogDestinations)+"\x00"+d.ID] {
			continue
		}
		legacyDrains[canonicalStandardUUID(d.ID)] = cloneAppLogDrain(d)
		raw, _ := json.Marshal(d)
		p.Backups = append(p.Backups, standardControlBackup{AppID: app.AppID, Field: appstandards.LogDestinations, ID: canonicalStandardUUID(d.ID), Body: raw, ConfigHash: standardReviewBytesDigest(raw)})
	}
	for _, signer := range signers {
		if projected[string(appstandards.TrustedPublishers)+"\x00"+signer.SignerName] {
			continue
		}
		id := standardLegacySignerID(app.AppID, signer.SignerName, standardReviewBytesDigest(signer.CosignPublicKey))
		legacySigners[id] = signer
		raw, _ := json.Marshal(signer)
		p.Backups = append(p.Backups, standardControlBackup{AppID: app.AppID, Field: appstandards.TrustedPublishers, ID: id, Body: raw, ConfigHash: standardReviewBytesDigest(raw)})
	}
	urls := map[string]bool{}
	for _, id := range standardReviewStrings(target.Effective.Values[appstandards.LogDestinations]) {
		var d AppLogDrain
		if resource, exists := destinations[id]; exists && sameStandardUUID(resource.OrgID, app.OrgID) {
			d = AppLogDrain{ID: uuid.NewSHA1(uuid.MustParse(app.AppID), []byte("gregale.standard-drain\x00"+id)).String(), AppID: app.AppID, AccountID: app.AccountID, Kind: AppLogDrainKind(resource.Kind), TargetURL: resource.TargetURL, AuthHeaderSealed: append([]byte{}, resource.AuthHeaderSealed...), Enabled: true, CreatedAt: now, UpdatedAt: now}
			for _, current := range drains {
				if current.ID == d.ID {
					d.CreatedAt = current.CreatedAt
				}
			}
			p.Bindings = append(p.Bindings, standardControlBinding{AppID: app.AppID, Field: appstandards.LogDestinations, ResourceID: id, PhysicalID: d.ID})
		} else {
			var exists bool
			d, exists = legacyDrains[id]
			if !exists {
				return p, ErrApplicationStandardReviewBlocked
			}
		}
		if urls[d.TargetURL] {
			return p, ErrConflict
		}
		urls[d.TargetURL] = true
		p.Drains = append(p.Drains, cloneAppLogDrain(d))
	}
	names := map[string]bool{}
	for _, id := range standardReviewStrings(target.Effective.Values[appstandards.TrustedPublishers]) {
		var signer AppTrustedSigner
		if resource, exists := publishers[id]; exists && sameStandardUUID(resource.OrgID, app.OrgID) {
			der, err := base64.StdEncoding.DecodeString(resource.PublicKeyDER)
			if err != nil || standardReviewBytesDigest(der) != resource.Fingerprint {
				return p, fmt.Errorf("invalid approved publisher key")
			}
			signer = AppTrustedSigner{AppID: app.AppID, AccountID: app.AccountID, SignerName: "standard-" + id, CosignPublicKey: der, AddedAt: now, AddedByAccountID: app.AccountID}
			for _, current := range signers {
				if current.SignerName == signer.SignerName {
					signer.AddedAt = current.AddedAt
				}
			}
			p.Bindings = append(p.Bindings, standardControlBinding{AppID: app.AppID, Field: appstandards.TrustedPublishers, ResourceID: id, PhysicalID: signer.SignerName})
		} else {
			var exists bool
			signer, exists = legacySigners[id]
			if !exists {
				return p, ErrApplicationStandardReviewBlocked
			}
		}
		if names[signer.SignerName] {
			return p, ErrConflict
		}
		names[signer.SignerName] = true
		signer.CosignPublicKey = append([]byte{}, signer.CosignPublicKey...)
		p.Signers = append(p.Signers, signer)
	}
	for _, part := range []struct {
		field  appstandards.Field
		target any
	}{{appstandards.RequireSigned, &p.RequireSigned}, {appstandards.SecurityPolicy, &p.SecurityPolicy}, {appstandards.EgressExtraPorts, &p.Ports}} {
		if err := json.Unmarshal(target.Effective.Values[part.field], part.target); err != nil {
			return p, err
		}
	}
	for _, raw := range standardReviewStrings(target.Effective.Values[appstandards.EgressCIDRs]) {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			return p, err
		}
		p.CIDRs = append(p.CIDRs, prefix)
	}
	return p, nil
}

func standardInstalledEnrollment(app standardReviewAppSnapshot, t ApplicationStandardOperationTarget, now time.Time) ApplicationStandardEnrollment {
	e := ApplicationStandardEnrollment{AppID: app.AppID, OrgID: app.OrgID, ProjectID: app.ProjectID, BaseSettings: cloneStandardSettings(t.ApprovedApp.BaseSettings), LocalSettings: cloneStandardSettings(t.ApprovedApp.LocalSettings), AdditionalLogDestinations: append([]string{}, t.ApprovedApp.AdditionalLogDestinations...), Adoptions: append([]appstandards.Adoption{}, t.ApprovedApp.AfterAdoptions...), Effective: t.ApprovedApp.Effective, EffectiveHash: t.approvalInput.EffectiveHash, DesiredRevision: app.Enrollment.DesiredRevision + 1, State: "persisted", UpdatedAt: now}
	e.PersistedRevision = e.DesiredRevision
	return e
}

func standardManagedField(e ApplicationStandardEnrollment, field appstandards.Field) bool {
	return len(e.Effective.Sources[field]) != 0
}
