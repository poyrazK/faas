package tasktoolsource

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

// Change reuses the native Task review state machine, with the hosted intent's
// own digest, retained source and original deadline. It never creates execution
// authority or changes the immutable review or source identity.
func Change(record api.HostedTaskSourceIntent, action string, in api.HostedTaskSourceDecision, fresh, hasSource bool, now time.Time) (api.HostedTaskSourceIntent, error) {
	if ValidateRecord(record, record.AccountID, record.ID) != nil || !toolpublication.ValidID(in.ReviewSHA256) || action != "check" && action != "decide" {
		return record, ErrInvalid
	}
	if action == "decide" {
		if in.Decision != "approve" && in.Decision != "reject" && in.Decision != "revoke" {
			return record, ErrInvalid
		}
		if in.IntentSHA256 != record.IntentSHA256 || in.ReviewSHA256 != record.ReviewSHA256 {
			return record, tasktoolrelease.ErrChanged
		}
	}
	record = View(record, hasSource, now)
	review := api.TaskToolReleaseIntentRecord{Version: 1, ID: record.ID, AccountID: record.AccountID,
		RequesterKeyID: record.RequesterKeyID, IntentSHA256: record.IntentSHA256,
		Intent: api.TaskToolReleaseIntent{ReleaseFingerprint: record.ReviewSHA256},
		Status: record.ReviewStatus, CreatedAt: record.CreatedAt, ExpiresAt: record.ExpiresAt, DecidedAt: record.DecidedAt}
	if action == "check" || in.Decision == "approve" {
		review = tasktoolrelease.Check(review, in.ReviewSHA256, fresh && hasSource, now)
	}
	if action == "decide" && review.Status != "stale" && review.Status != "expired" {
		var err error
		review, err = tasktoolrelease.Decide(review, api.TaskToolReleaseIntentDecision{
			IntentSHA256: in.IntentSHA256, ReleaseFingerprint: in.ReviewSHA256, Decision: in.Decision}, now)
		if err != nil {
			return record, err
		}
	}
	record.ReviewStatus, record.DecidedAt = review.Status, review.DecidedAt
	return record, nil
}
