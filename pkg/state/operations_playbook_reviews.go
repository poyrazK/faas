package state

import (
	"context"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func workflowPlaybookProgressActive(p api.OperationWorkflowPlaybookProgress) bool {
	return p.Disposition == "" || p.Disposition == "active"
}
func reviewWorkflowPlaybookProgress(ctx context.Context, store workflowPlaybookProgressBackend, account, tenant string, opts api.OperationWorkflowCoordinationTimelineOptions, id, actor string, req api.OperationWorkflowPlaybookReviewRequest, operator bool) (api.OperationWorkflowPlaybookProgress, error) {
	r, err := preparePlaybookProgressIdentity(ctx, store, account, tenant, opts, operator)
	if err != nil {
		return r.Progress, err
	}
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil {
		return r.Progress, ErrInvalidArgument
	}
	id = parsed.String()
	if actor == "" || req.Revision < 1 || req.Revision >= api.OperationWorkflowReadinessCoordinationRevisionMax || strings.TrimSpace(req.Reason) == "" || len(req.Reason) > api.OperationWorkflowReadinessFollowUpNoteMaxBytes || !utf8.ValidString(req.Reason) || strings.IndexFunc(req.Reason, unicode.IsControl) >= 0 {
		return r.Progress, ErrInvalidArgument
	}
	switch req.Decision {
	case "continue", "retire":
		if req.ReplacementProgressID != "" || req.ReplacementRevision != 0 {
			return r.Progress, ErrInvalidArgument
		}
	case "replace":
		target, err := uuid.Parse(req.ReplacementProgressID)
		if err != nil || target == uuid.Nil || target.String() == id || req.ReplacementRevision < 1 || req.ReplacementRevision >= api.OperationWorkflowReadinessCoordinationRevisionMax {
			return r.Progress, ErrInvalidArgument
		}
		req.ReplacementProgressID = target.String()
	default:
		return r.Progress, ErrInvalidArgument
	}
	opts.AppID = r.App
	overview, err := workflowCase(ctx, store, r.Account, r.Tenant, opts, operator)
	if err != nil {
		return r.Progress, err
	}
	for _, p := range overview.PlaybookProgress {
		if p.ID == id {
			r.Progress = p
			break
		}
	}
	p := &r.Progress
	if p.ID == "" {
		return *p, ErrNotFound
	}
	if p.Revision != req.Revision || !workflowPlaybookProgressActive(*p) {
		return *p, ErrConflict
	}
	if p.Applicability == nil {
		return *p, ErrConflict
	}
	if req.Decision == "replace" {
		found := false
		for _, target := range overview.PlaybookProgress {
			if target.ID == req.ReplacementProgressID {
				found = true
				if !workflowPlaybookProgressActive(target) || target.Revision != req.ReplacementRevision || target.Applicability == nil || target.Applicability.Status != "applicable" {
					return *p, ErrConflict
				}
				break
			}
		}
		if !found {
			return *p, ErrNotFound
		}
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	review := api.OperationWorkflowPlaybookReview{Revision: p.Revision + 1, Decision: req.Decision, Reason: req.Reason, Actor: actor, At: now, Applicability: *p.Applicability, ReplacementProgressID: req.ReplacementProgressID, ReplacementRevision: req.ReplacementRevision}
	// Live case fields are not persisted alongside durable review observations.
	p.Applicability = nil
	p.ReplacesProgressIDs = nil
	p.Revision++
	p.UpdatedAt = now
	p.LatestReview = &review
	p.Disposition = "active"
	if req.Decision == "retire" {
		p.Disposition = "retired"
	}
	if req.Decision == "replace" {
		p.Disposition = "replaced"
	}
	p.Reviews = append(p.Reviews, review)
	if len(p.Reviews) > api.OperationWorkflowPlaybookReviewsMax {
		p.Reviews = p.Reviews[len(p.Reviews)-api.OperationWorkflowPlaybookReviewsMax:]
		p.ReviewsTruncated = true
	}
	return store.savePlaybookProgressReview(ctx, r, req.Revision, req.ReplacementProgressID, req.ReplacementRevision)
}
func validatePlaybookReplacement(rows []api.OperationWorkflowPlaybookProgress, id string, revision int64) error {
	if id == "" {
		return nil
	}
	for _, p := range rows {
		if p.ID == id {
			if !workflowPlaybookProgressActive(p) || p.Revision != revision {
				return ErrConflict
			}
			return nil
		}
	}
	return ErrNotFound
}
func projectPlaybookReplacementLinks(rows []api.OperationWorkflowPlaybookProgress) {
	for i := range rows {
		rows[i].ReplacesProgressIDs = nil
		for _, prior := range rows {
			if prior.Disposition == "replaced" && prior.LatestReview != nil && prior.LatestReview.ReplacementProgressID == rows[i].ID {
				rows[i].ReplacesProgressIDs = append(rows[i].ReplacesProgressIDs, prior.ID)
			}
		}
	}
}
