package internalmail

import (
	"context"
	"net/mail"
	"strings"
	"time"

	outboxq "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
)

// An admission receipt proves maild committed a message. It makes no assertion
// about SMTP custody or destination delivery. Replay keys and payloads stay private.
type ProducerAdmissionReceipt struct {
	MessageID  string    `json:"message_id"`
	AdmittedAt time.Time `json:"admitted_at"`
}

type ProducerOutboxAdmissionReview struct {
	IntentID         string                    `json:"intent_id"`
	Revision         int64                     `json:"revision"`
	State            string                    `json:"state"`
	Template         string                    `json:"template"`
	RecoveryID       string                    `json:"recovery_id"`
	IntentRecoveryID string                    `json:"intent_recovery_id"`
	RecoveryHeld     bool                      `json:"recovery_held"`
	Quarantined      bool                      `json:"quarantined"`
	AdmissionStatus  string                    `json:"admission_status"`
	Receipt          *ProducerAdmissionReceipt `json:"receipt,omitempty"`
	CanAcknowledge   bool                      `json:"can_acknowledge"`
	BlockReason      string                    `json:"block_reason,omitempty"`
}

func producerOutboxAdmissionRoute(path string) bool {
	return strings.HasPrefix(path, ProducerOutboxPath+"/items/") && strings.HasSuffix(path, "/admission")
}

func (s *Service) outboxReconciliationEnabled() bool {
	return s.producerOutboxEnabled() && s.OutboxReconciliationEnabled != nil && s.OutboxReconciliationEnabled()
}

type producerAdmissionStore interface {
	producerOutboxAdmission(context.Context, string) (ProducerOutboxAdmissionReview, error)
}

func (s *Service) ProducerOutboxAdmission(ctx context.Context, id string) (ProducerOutboxAdmissionReview, error) {
	if !s.outboxReconciliationEnabled() || !s.operationsEnabled() {
		return ProducerOutboxAdmissionReview{}, ErrDisabled
	}
	if !canonicalOperatorID(id) {
		return ProducerOutboxAdmissionReview{}, ErrInvalid
	}
	store, ok := s.Store.(producerAdmissionStore)
	if !ok {
		return ProducerOutboxAdmissionReview{}, ErrInvalid
	}
	review, err := store.producerOutboxAdmission(ctx, id)
	if !s.outboxReconciliationEnabled() || !s.operationsEnabled() {
		return ProducerOutboxAdmissionReview{}, ErrDisabled
	}
	if review.Template == ProducerOutboxVerificationTemplate && !s.verificationOutboxEnabled() {
		review.CanAcknowledge = false
		review.BlockReason = "verification_disabled"
	}
	if review.Template == ProducerOutboxPasswordResetTemplate && !s.passwordResetOutboxEnabled() {
		review.CanAcknowledge = false
		review.BlockReason = "password_reset_disabled"
	}
	if review.Template == ProducerOutboxMagicLinkTemplate && !s.magicLinkOutboxEnabled() {
		review.CanAcknowledge = false
		review.BlockReason = "magic_link_disabled"
	}
	if review.Template == ProducerOutboxMFARecoveryAlertTemplate && !s.mfaRecoveryAlertOutboxEnabled() {
		review.CanAcknowledge = false
		review.BlockReason = "mfa_recovery_alert_disabled"
	}
	if review.Template == ProducerOutboxMFADisableTemplate && !s.mfaDisableOutboxEnabled() {
		review.CanAcknowledge = false
		review.BlockReason = "mfa_disable_disabled"
	}
	if review.Template == ProducerOutboxMFADisabledAlertTemplate && !s.mfaDisabledAlertOutboxEnabled() {
		review.CanAcknowledge = false
		review.BlockReason = "mfa_disabled_alert_disabled"
	}
	if review.Template == ProducerOutboxPasswordChangedAlertTemplate && !s.passwordChangedAlertOutboxEnabled() {
		review.CanAcknowledge = false
		review.BlockReason = "password_changed_alert_disabled"
	}
	if review.Template == ProducerOutboxMFAEnabledAlertTemplate && !s.mfaEnabledAlertOutboxEnabled() {
		review.CanAcknowledge = false
		review.BlockReason = "mfa_enabled_alert_disabled"
	}
	if review.Template == ProducerOutboxPaymentFailedTemplate && !s.paymentFailedOutboxEnabled() {
		review.CanAcknowledge = false
		review.BlockReason = "payment_failed_disabled"
	}
	if review.Template == ProducerOutboxAccountRestoredTemplate && !s.accountRestoredOutboxEnabled() {
		review.CanAcknowledge = false
		review.BlockReason = "account_restored_disabled"
	}
	if review.Template == ProducerOutboxDeploymentFailedTemplate && !s.deploymentFailedOutboxEnabled() {
		review.CanAcknowledge = false
		review.BlockReason = "deployment_failed_disabled"
	}
	if review.Template == ProducerOutboxSubscriptionEndedTemplate && !s.subscriptionEndedOutboxEnabled() {
		review.CanAcknowledge = false
		review.BlockReason = "subscription_ended_disabled"
	}
	if review.Template == ProducerOutboxQuotaWarningTemplate && !s.quotaWarningOutboxEnabled() {
		review.CanAcknowledge = false
		review.BlockReason = "quota_warning_disabled"
	}
	if review.Template == ProducerOutboxNonpaymentDeletionTemplate && !s.nonpaymentDeletionOutboxEnabled() {
		review.CanAcknowledge = false
		review.BlockReason = "nonpayment_deletion_disabled"
	}
	if review.Template == ProducerOutboxAccountSuspendedTemplate && !s.accountSuspendedOutboxEnabled() {
		review.CanAcknowledge = false
		review.BlockReason = "account_suspended_disabled"
	}
	return review, err
}

func (s *PGStore) producerOutboxAdmission(ctx context.Context, id string) (ProducerOutboxAdmissionReview, error) {
	return LookupProducerOutboxAdmission(ctx, s.pool, s.namespace, id)
}

// LookupProducerOutboxAdmission is a read projection shared by maild's private
// inspection and apid's owner transaction. apid rechecks it under its intent and
// recovery locks; it never trusts an operator-supplied message ID as evidence.
func LookupProducerOutboxAdmission(ctx context.Context, db outboxq.DBTX, namespace, id string) (ProducerOutboxAdmissionReview, error) {
	if !canonicalOperatorID(id) {
		return ProducerOutboxAdmissionReview{}, ErrInvalid
	}
	r, err := outboxq.New().AdmissionLookup(ctx, db, outboxq.AdmissionLookupParams{Namespace: namespace, ID: pgID(id)})
	if err != nil {
		return ProducerOutboxAdmissionReview{}, storeError(err)
	}
	review := ProducerOutboxAdmissionReview{IntentID: id, Revision: r.Revision, State: r.State, Template: r.Template, RecoveryID: idString(r.RecoveryID), IntentRecoveryID: idString(r.IntentRecoveryID), RecoveryHeld: r.RecoveryHeld, Quarantined: r.RecoveryID != r.IntentRecoveryID, AdmissionStatus: "missing"}
	if r.MessageID.Valid {
		review.AdmissionStatus = "mismatch"
		from, parseErr := mail.ParseAddress(r.FromAddress)
		if parseErr == nil && r.MetadataMatches.Valid && r.MetadataMatches.Bool && strings.EqualFold(from.Address, r.SenderAddress) {
			review.AdmissionStatus = "confirmed"
			review.Receipt = &ProducerAdmissionReceipt{MessageID: idString(r.MessageID), AdmittedAt: r.AdmittedAt.Time}
		}
	}
	switch {
	case review.RecoveryHeld:
		review.BlockReason = "recovery_held"
	case review.Quarantined:
		review.BlockReason = "recovery_quarantined"
	case r.State == "admitted":
		review.BlockReason = "already_admitted"
	case r.State == "cancelled" || r.State == "suppressed":
		review.BlockReason = "intent_closed"
	case r.ActiveClaim.Valid && r.ActiveClaim.Bool:
		review.BlockReason = "active_claim"
	case review.AdmissionStatus != "confirmed":
		review.BlockReason = "admission_" + review.AdmissionStatus
	default:
		review.CanAcknowledge = true
	}
	return review, nil
}
