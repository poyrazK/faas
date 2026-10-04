package main

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
	"github.com/onebox-faas/faas/pkg/state"
)

func applicationStandardReviewedAppResponse(a state.ApplicationStandardReviewedApp) api.ApplicationStandardReviewedApp {
	before, local := a.BeforeSettings, a.LocalSettings
	if before == nil {
		before = appstandards.Settings{}
	}
	if local == nil {
		local = appstandards.Settings{}
	}
	return api.ApplicationStandardReviewedApp{AppID: a.AppID, Slug: a.Slug, ProjectID: a.ProjectID, DesiredRevision: a.DesiredRevision,
		BeforeSettings: before, LocalSettings: local, BeforeAdoptions: append([]appstandards.Adoption{}, a.BeforeAdoptions...), AfterAdoptions: append([]appstandards.Adoption{}, a.AfterAdoptions...),
		AdditionalLogDestinations: append([]string{}, a.AdditionalLogDestinations...), Effective: a.Effective, ChangedFields: append([]appstandards.Field{}, a.ChangedFields...)}
}

func applicationStandardReviewResponse(p state.ApplicationStandardReviewPlan) api.ApplicationStandardReview {
	result := api.ApplicationStandardReview{ID: p.ID, OrgID: p.OrgID, CreatedBy: p.CreatedBy, Request: api.ApplicationStandardReviewRequest(p.Request), ApprovalHash: p.ApprovalHash,
		Applications: []api.ApplicationStandardReviewedApp{}, Blockers: []api.ApplicationStandardReviewBlocker{}, CreatedAt: p.CreatedAt, ExpiresAt: p.ExpiresAt}
	for _, a := range p.Applications {
		result.Applications = append(result.Applications, applicationStandardReviewedAppResponse(a))
	}
	for _, b := range p.Blockers {
		result.Blockers = append(result.Blockers, api.ApplicationStandardReviewBlocker(b))
	}
	return result
}

func applicationStandardOperationResponse(o state.ApplicationStandardOperation) api.ApplicationStandardOperation {
	result := api.ApplicationStandardOperation{ID: o.ID, OrgID: o.OrgID, PlanID: o.PlanID, AssignmentID: o.AssignmentID, ApprovalHash: o.ApprovalHash, ApprovedBy: o.ApprovedBy,
		BatchSize: o.BatchSize, State: o.State, ErrorCode: o.ErrorCode, Targets: []api.ApplicationStandardOperationTarget{}, CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt}
	for _, t := range o.Targets {
		result.Targets = append(result.Targets, api.ApplicationStandardOperationTarget{AppID: t.AppID, Position: t.Position,
			ApprovedApp: applicationStandardReviewedAppResponse(t.ApprovedApp), State: t.State, DesiredRevision: t.DesiredRevision, ErrorCode: t.ErrorCode, UpdatedAt: t.UpdatedAt})
	}
	return result
}

func applicationStandardExceptionResponse(x state.ApplicationStandardException, now time.Time) api.ApplicationStandardException {
	status := "active"
	if !now.Before(x.ExpiresAt) {
		status = "expired"
	}
	if x.RevokedAt != nil {
		status = "revoked"
	}
	return api.ApplicationStandardException{ID: x.ID, OrgID: x.OrgID, AppID: x.AppID, StandardID: x.StandardID, Version: x.Version, Field: x.Field, Value: x.Value,
		Reason: x.Reason, ExpiresAt: x.ExpiresAt, ApprovedBy: x.ApprovedBy, CreatedAt: x.CreatedAt, RevokedBy: x.RevokedBy, RevokedAt: x.RevokedAt, Status: status}
}
