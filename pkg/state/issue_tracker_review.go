package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// IssueTrackerReviewStore lists saved observations only; listing never contacts a provider.
type IssueTrackerReviewStore interface {
	ListIssueTrackerReviews(context.Context, string, string, string, IssueCursor) (IssueTrackerReviewPage, error)
}

type IssueTrackerReviewPage struct {
	Items      []IssueTrackerReview
	NextCursor string
}

type IssueTrackerReview struct {
	LinkID, IssueID, IssueTitle, TicketTitle, ExternalKey, Provider, Environment     string
	Direction, Outcome, Reason, PendingState, ObservedState, SourceState, IssueState string
	Revision                                                                         int64
	Regrouped                                                                        bool
	CreatedAt, UpdatedAt                                                             time.Time
	LastCheckedAt, LastSuccessfulAt, NextCheckAt, ClaimUntil                         *time.Time
}

func ValidIssueTrackerReviewCategory(category string) bool {
	switch category {
	case "all", "closures", "conflicts", "pending", "paused":
		return true
	default:
		return false
	}
}

func issueTrackerReviewHash(app, category string) string {
	v := sha256.Sum256([]byte("issue_tracker_reviews/" + app + "/" + category))
	return hex.EncodeToString(v[:])
}
