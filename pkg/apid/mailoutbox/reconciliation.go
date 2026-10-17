package mailoutbox

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

func validID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed != uuid.Nil && parsed.String() == id
}

func uuidString(id [16]byte) string { return uuid.UUID(id).String() }

type ReconciliationRequest struct {
	internalmail.OperatorAuditInput
	ExpectedRevision int64  `json:"expected_revision"`
	RecoveryID       string `json:"recovery_id"`
	MessageID        string `json:"message_id"`
}

func (r ReconciliationRequest) Validate() error {
	if r.OperatorAuditInput.Validate() != nil || r.ExpectedRevision < 1 ||
		!internalmail.ValidProducerOutboxGeneration(r.RecoveryID) || !validID(r.MessageID) {
		return internalmail.ErrInvalid
	}
	return nil
}

type ReconciliationRecord struct {
	RequestID         string    `json:"request_id"`
	IntentID          string    `json:"intent_id"`
	MessageID         string    `json:"message_id"`
	RecoveryID        string    `json:"recovery_id"`
	ExpectedRevision  int64     `json:"expected_revision"`
	ResultingRevision int64     `json:"resulting_revision"`
	PreviousState     string    `json:"previous_state"`
	State             string    `json:"state"`
	Template          string    `json:"template"`
	Actor             string    `json:"actor"`
	Reason            string    `json:"reason"`
	Evidence          string    `json:"evidence"`
	CreatedAt         time.Time `json:"created_at"`
}

// Reconciler is an apid-owned, local operator capability. It has no sender,
// listener, key material, admission writes, or route in the customer API.
type Reconciler struct {
	pool                        *pgxpool.Pool
	namespace                   string
	enabled                     func() bool
	verificationEnabled         func() bool
	passwordResetEnabled        func() bool
	magicLinkEnabled            func() bool
	mfaRecoveryAlertEnabled     func() bool
	mfaDisabledAlertEnabled     func() bool
	deploymentFailedEnabled     func() bool
	subscriptionEndedEnabled    func() bool
	quotaWarningEnabled         func() bool
	nonpaymentDeletionEnabled   func() bool
	accountSuspendedEnabled     func() bool
	accountRestoredEnabled      func() bool
	paymentFailedEnabled        func() bool
	mfaEnabledAlertEnabled      func() bool
	passwordChangedAlertEnabled func() bool
	mfaDisableEnabled           func() bool
}

func (s *Reconciler) WithVerification(enabled func() bool) *Reconciler {
	copy := *s
	copy.verificationEnabled = enabled
	return &copy
}

func (s *Reconciler) templateEnabled(template string) bool {
	if !s.on() {
		return false
	}
	switch template {
	case "account_deletion_pending":
		return true
	case VerificationTemplate:
		return s.verificationEnabled != nil && s.verificationEnabled()
	case PasswordResetTemplate:
		return s.passwordResetEnabled != nil && s.passwordResetEnabled()
	case MagicLinkTemplate:
		return s.magicLinkEnabled != nil && s.magicLinkEnabled()
	case MFADisableTemplate:
		return s.mfaDisableEnabled != nil && s.mfaDisableEnabled()
	case DeploymentFailedTemplate:
		return s.deploymentFailedEnabled != nil && s.deploymentFailedEnabled()
	case SubscriptionEndedTemplate:
		return s.subscriptionEndedEnabled != nil && s.subscriptionEndedEnabled()
	case QuotaWarningTemplate:
		return s.quotaWarningEnabled != nil && s.quotaWarningEnabled()
	case NonpaymentDeletionTemplate:
		return s.nonpaymentDeletionEnabled != nil && s.nonpaymentDeletionEnabled()
	case AccountSuspendedTemplate:
		return s.accountSuspendedEnabled != nil && s.accountSuspendedEnabled()
	case AccountRestoredTemplate:
		return s.accountRestoredEnabled != nil && s.accountRestoredEnabled()
	case PaymentFailedTemplate:
		return s.paymentFailedEnabled != nil && s.paymentFailedEnabled()
	case MFAEnabledAlertTemplate:
		return s.mfaEnabledAlertEnabled != nil && s.mfaEnabledAlertEnabled()
	case PasswordChangedAlertTemplate:
		return s.passwordChangedAlertEnabled != nil && s.passwordChangedAlertEnabled()
	case MFADisabledAlertTemplate:
		return s.mfaDisabledAlertEnabled != nil && s.mfaDisabledAlertEnabled()
	case MFARecoveryAlertTemplate:
		return s.mfaRecoveryAlertEnabled != nil && s.mfaRecoveryAlertEnabled()
	default:
		return false
	}
}

func (s *Reconciler) WithPasswordReset(enabled func() bool) *Reconciler {
	copy := *s
	copy.passwordResetEnabled = enabled
	return &copy
}

func (s *Reconciler) WithMagicLink(enabled func() bool) *Reconciler {
	copy := *s
	copy.magicLinkEnabled = enabled
	return &copy
}

func (s *Reconciler) WithMFARecoveryAlert(enabled func() bool) *Reconciler {
	copy := *s
	copy.mfaRecoveryAlertEnabled = enabled
	return &copy
}

func (s *Reconciler) WithMFADisable(enabled func() bool) *Reconciler {
	copy := *s
	copy.mfaDisableEnabled = enabled
	return &copy
}

func (s *Reconciler) WithMFADisabledAlert(enabled func() bool) *Reconciler {
	copy := *s
	copy.mfaDisabledAlertEnabled = enabled
	return &copy
}

func NewReconciler(pool *pgxpool.Pool, namespace string, enabled func() bool) (*Reconciler, error) {
	if enabled == nil || !enabled() {
		return nil, internalmail.ErrDisabled
	}
	if pool == nil || !regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`).MatchString(namespace) {
		return nil, internalmail.ErrInvalid
	}
	return &Reconciler{pool: pool, namespace: namespace, enabled: enabled}, nil
}

func (s *Reconciler) on() bool { return s != nil && s.enabled != nil && s.enabled() }

func (s *Reconciler) Audit(ctx context.Context, id string) (ReconciliationRecord, error) {
	if !s.on() {
		return ReconciliationRecord{}, internalmail.ErrDisabled
	}
	if !validID(id) {
		return ReconciliationRecord{}, internalmail.ErrInvalid
	}
	r, err := q.New().Reconciliation(ctx, s.pool, q.ReconciliationParams{Namespace: s.namespace, RequestID: pgID(id)})
	if !s.templateEnabled(r.Template) {
		return ReconciliationRecord{}, internalmail.ErrDisabled
	}
	return reconciliationRecord(r), reconciliationError(err)
}

// Acknowledge records an existing admission only. It cannot create mail, reset
// a budget/deadline/generation, release a suppression, or change maild custody.
func (s *Reconciler) Acknowledge(ctx context.Context, id string, input ReconciliationRequest) (ReconciliationRecord, error) {
	if !s.on() {
		return ReconciliationRecord{}, internalmail.ErrDisabled
	}
	if !validID(id) || input.Validate() != nil {
		return ReconciliationRecord{}, internalmail.ErrInvalid
	}
	raw, err := json.Marshal(struct {
		IntentID string
		Input    ReconciliationRequest
	}{id, input})
	if err != nil {
		return ReconciliationRecord{}, err
	}
	hash := sha256.Sum256(raw)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ReconciliationRecord{}, err
	}
	defer rollback(ctx, tx)
	queries := q.New()
	if err := queries.LockReconciliation(ctx, tx, q.LockReconciliationParams{Namespace: s.namespace, RequestID: input.RequestID}); err != nil {
		return ReconciliationRecord{}, err
	}
	previous, err := queries.Reconciliation(ctx, tx, q.ReconciliationParams{Namespace: s.namespace, RequestID: pgID(input.RequestID)})
	if err == nil {
		if !s.templateEnabled(previous.Template) {
			return ReconciliationRecord{}, internalmail.ErrDisabled
		}
		if subtle.ConstantTimeCompare(previous.RequestHash, hash[:]) != 1 {
			return ReconciliationRecord{}, internalmail.ErrConflict
		}
		return reconciliationRecord(previous), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ReconciliationRecord{}, err
	}
	return s.acknowledgeLocked(ctx, tx, queries, id, input, hash[:])
}

func (s *Reconciler) acknowledgeLocked(ctx context.Context, tx pgx.Tx, queries *q.Queries, id string, input ReconciliationRequest, hash []byte) (ReconciliationRecord, error) {
	// Match the dispatch lock order. In particular, retain no recovery fence
	// while waiting for a dispatch that might be entering maild over HTTP.
	initial, err := queries.Intent(ctx, tx, q.IntentParams{Namespace: s.namespace, ID: pgID(id)})
	if err != nil {
		return ReconciliationRecord{}, reconciliationError(err)
	}
	if _, err := queries.ShareAccount(ctx, tx, initial.AccountID); err != nil {
		return ReconciliationRecord{}, reconciliationError(err)
	}
	intent, err := queries.LockIntent(ctx, tx, q.LockIntentParams{Namespace: s.namespace, ID: pgID(id)})
	if err != nil {
		return ReconciliationRecord{}, reconciliationError(err)
	}
	if err := queries.ShareRecovery(ctx, tx, s.namespace); err != nil {
		return ReconciliationRecord{}, err
	}
	if !s.templateEnabled(intent.Template) {
		return ReconciliationRecord{}, internalmail.ErrDisabled
	}
	review, err := internalmail.LookupProducerOutboxAdmission(ctx, tx, s.namespace, id)
	if err != nil {
		return ReconciliationRecord{}, err
	}
	if !review.CanAcknowledge || review.Revision != input.ExpectedRevision || review.RecoveryID != input.RecoveryID || review.Receipt.MessageID != input.MessageID {
		return ReconciliationRecord{}, internalmail.ErrConflict
	}
	count, err := queries.AcknowledgeAdmission(ctx, tx, q.AcknowledgeAdmissionParams{Namespace: s.namespace, ID: pgID(id), Revision: input.ExpectedRevision})
	if err != nil {
		return ReconciliationRecord{}, err
	}
	if count != 1 {
		return ReconciliationRecord{}, internalmail.ErrConflict
	}
	r, err := queries.InsertReconciliation(ctx, tx, q.InsertReconciliationParams{Namespace: s.namespace, RequestID: pgID(input.RequestID), IntentID: pgID(id), MessageID: pgID(input.MessageID), RecoveryID: pgID(input.RecoveryID), ExpectedRevision: input.ExpectedRevision, PreviousState: intent.State, Actor: input.Actor, Reason: input.Reason, Evidence: input.Evidence, RequestHash: hash, Template: intent.Template})
	if err != nil {
		return ReconciliationRecord{}, err
	}
	if !s.templateEnabled(intent.Template) {
		return ReconciliationRecord{}, internalmail.ErrDisabled
	}
	if err := tx.Commit(ctx); err != nil {
		return ReconciliationRecord{}, err
	}
	return reconciliationRecord(r), nil
}

func reconciliationRecord(r q.PlatformMailOutboxReconciliation) ReconciliationRecord {
	return ReconciliationRecord{RequestID: uuidString(r.RequestID.Bytes), IntentID: uuidString(r.IntentID.Bytes), MessageID: uuidString(r.MessageID.Bytes), RecoveryID: uuidString(r.RecoveryID.Bytes),
		ExpectedRevision: r.ExpectedRevision, ResultingRevision: r.ResultingRevision, PreviousState: r.PreviousState, State: "admitted", Template: r.Template, Actor: r.Actor, Reason: r.Reason, Evidence: r.Evidence, CreatedAt: r.CreatedAt.Time}
}

func reconciliationError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return internalmail.ErrNotFound
	}
	return err
}

func (s *Reconciler) WithPasswordChangedAlert(enabled func() bool) *Reconciler {
	copy := *s
	copy.passwordChangedAlertEnabled = enabled
	return &copy
}

func (s *Reconciler) WithMFAEnabledAlert(enabled func() bool) *Reconciler {
	copy := *s
	copy.mfaEnabledAlertEnabled = enabled
	return &copy
}

func (s *Reconciler) WithPaymentFailed(enabled func() bool) *Reconciler {
	copy := *s
	copy.paymentFailedEnabled = enabled
	return &copy
}

func (s *Reconciler) WithAccountRestored(enabled func() bool) *Reconciler {
	copy := *s
	copy.accountRestoredEnabled = enabled
	return &copy
}

func (s *Reconciler) WithAccountSuspended(enabled func() bool) *Reconciler {
	copy := *s
	copy.accountSuspendedEnabled = enabled
	return &copy
}

func (s *Reconciler) WithNonpaymentDeletion(enabled func() bool) *Reconciler {
	copy := *s
	copy.nonpaymentDeletionEnabled = enabled
	return &copy
}

func (s *Reconciler) WithQuotaWarning(enabled func() bool) *Reconciler {
	copy := *s
	copy.quotaWarningEnabled = enabled
	return &copy
}

func (s *Reconciler) WithSubscriptionEnded(enabled func() bool) *Reconciler {
	copy := *s
	copy.subscriptionEndedEnabled = enabled
	return &copy
}

func (s *Reconciler) WithDeploymentFailed(enabled func() bool) *Reconciler {
	copy := *s
	copy.deploymentFailedEnabled = enabled
	return &copy
}
