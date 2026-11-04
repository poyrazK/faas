package referrals

import (
	"context"
	"time"
)

// Inquiries are unverified website messages, separate from referral credit.
type InquiryInput struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Company string `json:"company"`
	Intent  string `json:"intent"`
	Message string `json:"message"`
	Code    string `json:"salesperson_code"`
}
type Inquiry struct {
	InquiryInput
	ID             string    `json:"id"`
	Status         string    `json:"status"`
	Reason         string    `json:"reason"`
	LeadID         string    `json:"lead_id"`
	TaskID         string    `json:"task_id"`
	OwnerID        string    `json:"owner_id"`
	OwnerEmail     string    `json:"owner_email"`
	LatestMessage  string    `json:"latest_message"`
	RepeatCount    int64     `json:"repeat_count"`
	Version        int64     `json:"version"`
	CreatedAt      time.Time `json:"created_at"`
	LastReceivedAt time.Time `json:"last_received_at"`
}
type IntakeOptions struct {
	MemberEmails []string
	RepEmails    []string
	DueOn        string
}
type InquiryFilter struct {
	Status, After string
	Limit         int32
}
type IntakeStore interface {
	SubmitInquiry(context.Context, string, time.Time, InquiryInput, IntakeOptions) error
	Inquiries(context.Context, Scope, InquiryFilter) ([]Inquiry, error)
	AssignInquiry(context.Context, string, int64, string, IntakeOptions) error
	CloseInquiry(context.Context, Scope, string, int64) (bool, error)
}
