package internalmail

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const ReputationPath = OperationsPath + "/reputation"

var (
	ErrReputationHeld     = errors.New("internal mail reputation protection holds this sender")
	ErrReputationRevision = errors.New("internal mail reputation hold revision changed")
)

type ReputationScope struct {
	Kind string `json:"scope"`
	Key  string `json:"key"`
}

func (s ReputationScope) Validate() error {
	if s.Kind == "domain" && canonicalOperatorID(s.Key) || s.Kind == "producer" && (s.Key == "" || validProducerID(s.Key)) {
		return nil
	}
	return ErrInvalid
}
func (s ReputationScope) cursor() string { return s.Kind + ":" + s.Key }

type ReputationWindow struct {
	Start         time.Time `json:"start"`
	End           time.Time `json:"end"`
	Samples       int64     `json:"samples"`
	Bounces       int64     `json:"bounces"`
	Complaints    int64     `json:"complaints"`
	BounceRate    float64   `json:"bounce_rate"`
	ComplaintRate float64   `json:"complaint_rate"`
}

func (w ReputationWindow) withRates() ReputationWindow {
	if w.Samples > 0 {
		w.BounceRate = float64(w.Bounces) / float64(w.Samples)
		w.ComplaintRate = float64(w.Complaints) / float64(w.Samples)
	}
	return w
}

func (w ReputationWindow) reason() string {
	if w.Samples < api.InternalMailReputationMinRecipients {
		return ""
	}
	if w.Complaints >= api.InternalMailReputationMinComplaints && w.Complaints*api.InternalMailReputationRateScale >= w.Samples*api.InternalMailReputationComplaintRate {
		return "complaint_rate"
	}
	if w.Bounces >= api.InternalMailReputationMinBounces && w.Bounces*api.InternalMailReputationRateScale >= w.Samples*api.InternalMailReputationBounceRate {
		return "bounce_rate"
	}
	return ""
}

type ReputationEvidence struct {
	ReputationWindow
	RecipientID string `json:"recipient_id"`
	EventID     string `json:"event_id"`
}
type ReputationStatus struct {
	ReputationScope
	Held      bool                `json:"held"`
	Revision  int64               `json:"revision"`
	Reason    string              `json:"reason,omitempty"`
	Evidence  *ReputationEvidence `json:"evidence,omitempty"`
	UpdatedAt *time.Time          `json:"updated_at,omitempty"`
	Window    ReputationWindow    `json:"window"`
}
type ReputationEvent struct {
	ID string `json:"id"`
	ReputationScope
	Revision  int64              `json:"revision"`
	Held      bool               `json:"held"`
	Reason    string             `json:"reason"`
	Evidence  ReputationEvidence `json:"evidence"`
	RequestID string             `json:"request_id,omitempty"`
	CreatedAt time.Time          `json:"created_at"`
}
type ReputationReleaseRequest struct {
	OperatorAuditInput
	ReputationScope
	ExpectedRevision  *int64 `json:"expected_revision"`
	ConfirmedReviewed bool   `json:"confirmed_reviewed"`
}

func (r ReputationReleaseRequest) Validate() error {
	if r.OperatorAuditInput.Validate() != nil || r.ReputationScope.Validate() != nil || r.ExpectedRevision == nil || *r.ExpectedRevision < 1 || !r.ConfirmedReviewed {
		return ErrInvalid
	}
	return nil
}

type reputationStore interface {
	refreshReputation(context.Context) error
	claimProtected(context.Context, domainAuthorization, []string, bool, PacingPolicy, bool, bool) (Recipient, error)
	beginProtected(context.Context, Recipient, string, domainAuthorization, []string, bool, PacingPolicy, bool, bool) error
	reportFeedback(context.Context, Report, func() bool) error
	reputationStatus(context.Context, ReputationScope) (ReputationStatus, error)
	reputationHolds(context.Context, OperatorList) (OperatorPage[ReputationStatus], error)
	reputationEvents(context.Context, OperatorList) (OperatorPage[ReputationEvent], error)
	releaseReputation(context.Context, ReputationReleaseRequest, func() bool) (OperationRecord, error)
}

func (s *Service) reputationEnabled() bool {
	return s.enabled() && s.ReputationEnabled != nil && s.ReputationEnabled()
}
func (s *Service) reputationOperations() (reputationStore, error) {
	if !s.reputationEnabled() || !s.operationsEnabled() {
		return nil, ErrDisabled
	}
	store, ok := s.Store.(reputationStore)
	if !ok {
		return nil, ErrInvalid
	}
	return store, nil
}
func (s *Service) ReputationStatus(ctx context.Context, scope ReputationScope) (ReputationStatus, error) {
	store, err := s.reputationOperations()
	if err != nil {
		return ReputationStatus{}, err
	}
	if scope.Validate() != nil {
		return ReputationStatus{}, ErrInvalid
	}
	return store.reputationStatus(ctx, scope)
}
func (s *Service) ReleaseReputation(ctx context.Context, input ReputationReleaseRequest) (OperationRecord, error) {
	store, err := s.reputationOperations()
	if err != nil {
		return OperationRecord{}, err
	}
	if input.Validate() != nil {
		return OperationRecord{}, ErrInvalid
	}
	return store.releaseReputation(ctx, input, func() bool { return s.reputationEnabled() && s.operationsEnabled() })
}

func reputationAdverse(r Report) bool {
	return r.Kind == "complained" || r.Kind == "bounced" && strings.HasPrefix(r.Code, "dsn_5.")
}
