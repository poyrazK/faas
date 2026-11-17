package referrals

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrInvalidTemplate = errors.New("invalid message template")

type MessageTemplateInput struct {
	Title    string `json:"title"`
	Category string `json:"category"`
	Stage    string `json:"stage"`
	Status   string `json:"status"`
	Subject  string `json:"subject"`
	Body     string `json:"body"`
	Version  int64  `json:"version"`
}
type MessageTemplate struct {
	MessageTemplateInput
	ID        string    `json:"id"`
	UpdatedAt time.Time `json:"updated_at"`
}
type TemplateFilter struct {
	After string
	Limit int32
}
type TemplateLead struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Email        string `json:"email"`
	Company      string `json:"company"`
	Stage        string `json:"stage"`
	OwnerEmail   string `json:"owner_email"`
	Version      int64  `json:"version"`
	ReferralCode string `json:"referral_code"`
}
type TemplateSource struct {
	Template MessageTemplate
	Lead     TemplateLead
}
type MessagePreview struct {
	Subject string   `json:"subject"`
	Body    string   `json:"body"`
	Missing []string `json:"missing"`
}
type TemplateStore interface {
	MessageTemplate(context.Context, Scope, string) (MessageTemplate, error)
	MessageTemplates(context.Context, Scope, TemplateFilter) ([]MessageTemplate, error)
	SaveMessageTemplate(context.Context, Scope, string, MessageTemplateInput) (string, error)
	MessageTemplateSource(context.Context, Scope, string, string, int64) (TemplateSource, error)
}

func TemplateFields() []Choice {
	return []Choice{{"lead_name", "Lead name"}, {"lead_email", "Lead email"}, {"company", "Company"}, {"salesperson_email", "Assigned salesperson email"}, {"referral_code", "Assigned salesperson referral code"}}
}
func templateText(value string, max int, required bool) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, 0) && len(value) <= max && (!required || strings.TrimSpace(value) != "")
}
func ValidateMessageTemplate(in MessageTemplateInput) error {
	if !templateText(in.Title, api.SalesNameMaxBytes, true) || !templateText(in.Subject, api.SalesFieldMaxBytes, false) || !templateText(in.Body, api.SalesTemplateBodyMaxBytes, true) || strings.ContainsAny(in.Title+in.Subject, "\r\n") || in.Version < 0 {
		return ErrInvalidTemplate
	}
	if !slices.Contains([]string{"intro", "demo", "follow_up", "stalled"}, in.Category) || !slices.Contains([]string{"draft", "active", "archived"}, in.Status) || !slices.Contains([]string{"", "new", "contacted", "qualified", "demo", "won", "lost"}, in.Stage) {
		return ErrInvalidTemplate
	}
	for _, value := range []string{in.Subject, in.Body} {
		if _, _, err := expandMessage(value, nil); err != nil {
			return err
		}
	}
	return nil
}

// expandMessage scans the source once. Customer values are literal text, never new instructions or placeholders.
func expandMessage(value string, fields map[string]string) (string, []string, error) {
	var out strings.Builder
	missing := []string{}
	for {
		start := strings.Index(value, "{{")
		if start < 0 {
			if strings.Contains(value, "}}") {
				return "", nil, ErrInvalidTemplate
			}
			out.WriteString(value)
			break
		}
		if strings.Contains(value[:start], "}}") {
			return "", nil, ErrInvalidTemplate
		}
		out.WriteString(value[:start])
		value = value[start+2:]
		end := strings.Index(value, "}}")
		if end < 0 {
			return "", nil, ErrInvalidTemplate
		}
		key := strings.TrimSpace(value[:end])
		known := false
		for _, f := range TemplateFields() {
			if key == f.Code {
				known = true
				break
			}
		}
		if !known {
			return "", nil, fmt.Errorf("%w: unsupported placeholder %q", ErrInvalidTemplate, key)
		}
		replacement := fields[key]
		if replacement == "" {
			out.WriteString("{{" + key + "}}")
			if !slices.Contains(missing, key) {
				missing = append(missing, key)
			}
		} else {
			out.WriteString(replacement)
		}
		value = value[end+2:]
	}
	return out.String(), missing, nil
}
func PersonalizeMessage(in MessageTemplateInput, lead TemplateLead) (MessagePreview, error) {
	fields := map[string]string{"lead_name": lead.Name, "lead_email": lead.Email, "company": lead.Company, "salesperson_email": lead.OwnerEmail, "referral_code": lead.ReferralCode}
	subject, missing, err := expandMessage(in.Subject, fields)
	if err != nil {
		return MessagePreview{}, err
	}
	body, more, err := expandMessage(in.Body, fields)
	for _, key := range more {
		if !slices.Contains(missing, key) {
			missing = append(missing, key)
		}
	}
	return MessagePreview{Subject: subject, Body: body, Missing: missing}, err
}
