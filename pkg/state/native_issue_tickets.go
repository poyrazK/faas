package state

import (
	"context"
	"time"
)

type NativeIssueTicketReceipt struct {
	AccountID, RequestID, AppID, IssueID, Hash, State, ClaimID string
	TicketURL, ProviderID, LinkID                              string
	RecoverAfter                                               time.Time
}
type NativeIssueTicketStore interface {
	ReserveNativeIssueTicket(context.Context, NativeIssueTicketReceipt) (NativeIssueTicketReceipt, error)
	ClaimNativeIssueTicket(context.Context, NativeIssueTicketReceipt, bool) (NativeIssueTicketReceipt, error)
	FinishNativeIssueTicket(context.Context, NativeIssueTicketReceipt) (NativeIssueTicketReceipt, error)
	LinkNativeIssueTicket(context.Context, NativeIssueTicketReceipt) (NativeIssueTicketReceipt, error)
}
