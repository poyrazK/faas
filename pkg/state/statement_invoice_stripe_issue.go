package state

import (
	"context"
	"fmt"
	"time"
)

// StripeInvoiceIssue checkpoints approval and delivery on the original export.
// The review token binds the approved terms without storing the recipient email.
type StripeInvoiceIssue struct {
	ReviewToken         string     `json:"review_token"`
	DueAt               time.Time  `json:"due_at"`
	ApprovedAt          time.Time  `json:"approved_at"`
	FinalizationStarted bool       `json:"finalization_started"`
	IssuedAt            *time.Time `json:"issued_at,omitempty"`
	SentAt              *time.Time `json:"sent_at,omitempty"`
}

type StripeInvoiceIssueStore interface {
	SaveStripeInvoiceIssue(ctx context.Context, accountID, exportID, claimToken string, issue StripeInvoiceIssue) error
}

func (m *MemStore) SaveStripeInvoiceIssue(_ context.Context, accountID, exportID, claimToken string, issue StripeInvoiceIssue) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.statementInvoiceExports[exportID]
	if e.AccountID != accountID || e.claimToken != claimToken || !e.claimedUntil.After(time.Now()) ||
		e.Status != StatementInvoiceExportCompleted || e.SourceKind != StatementSourceAppConsumer ||
		e.Provider != BillingProviderStripe || e.Collection != CollectionNone || (e.StripeIssue != nil && e.StripeIssue.SentAt != nil) {
		return ErrConflict
	}
	e.StripeIssue = &issue
	m.statementInvoiceExports[exportID] = e
	return nil
}

func (s *PgStore) SaveStripeInvoiceIssue(ctx context.Context, accountID, exportID, claimToken string, issue StripeInvoiceIssue) error {
	tag, err := s.pool.Exec(ctx, `update usage_statement_invoice_exports
		set stripe_issue = $4
		where account_id = $1::uuid and id = $2::uuid and claim_token = $3::uuid and claimed_until > now()
		  and status = 'completed' and source_kind = 'app_consumer' and provider = 'stripe' and collection = 'none'
		  and stripe_issue->>'sent_at' is null`, accountID, exportID, claimToken, issue)
	if err != nil {
		return fmt.Errorf("save stripe invoice issue: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	return nil
}
