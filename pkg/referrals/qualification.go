package referrals

import (
	"context"
	"errors"
	"time"
)

var ErrInvalidOutcome = errors.New("choose a valid closing reason and explain other")

var ErrOutcomeRequired = errors.New("a reason is required when closing a lead")

type Choice struct {
	Code  string `json:"code"`
	Label string `json:"label"`
}

func OutcomeReasons() map[string][]Choice {
	return map[string][]Choice{
		"won":  {{"product_fit", "Product fit"}, {"capabilities", "Platform capabilities"}, {"value", "Value"}, {"experience", "Pilot or demo experience"}, {"referral", "Trusted introduction"}, {"other", "Other"}},
		"lost": {{"product_gap", "Missing capabilities"}, {"budget", "Budget"}, {"timing", "Timing"}, {"competitor", "Competitor selected"}, {"no_response", "No response"}, {"no_need", "No current need"}, {"other", "Other"}},
	}
}
func ClosedStage(stage string) bool { return stage == "won" || stage == "lost" }
func ValidOutcomeReason(stage, reason string) bool {
	for _, r := range OutcomeReasons()[stage] {
		if r.Code == reason {
			return true
		}
	}
	return false
}

type QualificationInput struct {
	LeadVersion    int64  `json:"lead_version"`
	Version        int64  `json:"version"`
	UseCase        string `json:"use_case"`
	Workload       string `json:"workload"`
	DecisionMaker  string `json:"decision_maker"`
	BuyingTimeline string `json:"buying_timeline"`
	DecisionOn     string `json:"decision_on"`
	Priority       string `json:"priority"`
	ProductFit     string `json:"product_fit"`
	Blocker        string `json:"blocker"`
}
type Qualification struct {
	QualificationInput
	UpdatedBy string     `json:"updated_by"`
	UpdatedAt *time.Time `json:"updated_at"`
}
type Outcome struct {
	Stage       string    `json:"stage"`
	Reason      string    `json:"reason"`
	Note        string    `json:"note"`
	ActorID     string    `json:"actor_id"`
	OwnerID     string    `json:"owner_id"`
	LeadVersion int64     `json:"lead_version"`
	RecordedAt  time.Time `json:"recorded_at"`
}
type QualificationProfile struct {
	Lead          Prospect
	Qualification Qualification
	Outcome       *Outcome
}
type QualificationGap struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Email      string   `json:"email"`
	Stage      string   `json:"stage"`
	OwnerID    string   `json:"owner_id"`
	OwnerEmail string   `json:"owner_email"`
	Captured   int32    `json:"captured"`
	Missing    []string `json:"missing"`
}
type QualificationCounts struct {
	Open                 int64 `json:"open"`
	Complete             int64 `json:"complete"`
	MissingUseCase       int64 `json:"missing_use_case"`
	MissingWorkload      int64 `json:"missing_workload"`
	MissingDecisionMaker int64 `json:"missing_decision_maker"`
	MissingTimeline      int64 `json:"missing_timeline"`
	UnclassifiedWon      int64 `json:"unclassified_won"`
	UnclassifiedLost     int64 `json:"unclassified_lost"`
}
type OutcomeCount struct {
	Stage  string `json:"stage"`
	Reason string `json:"reason"`
	Count  int64  `json:"count"`
}
type QualificationFilter struct {
	Owner, After string
	Limit        int32
}
type QualificationStore interface {
	QualificationProfile(context.Context, Scope, string) (QualificationProfile, error)
	SaveQualification(context.Context, Scope, string, QualificationInput) error
	QualificationOverview(context.Context, Scope, QualificationFilter) ([]QualificationGap, QualificationCounts, []OutcomeCount, error)
}
