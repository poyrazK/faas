package referrals_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/referrals"
)

func messageInput() referrals.MessageTemplateInput {
	return referrals.MessageTemplateInput{Title: "Demo invitation", Category: "demo", Stage: "qualified", Status: "draft", Subject: "A demo for {{company}}", Body: "Hi {{lead_name}},\n\nContact {{salesperson_email}}. Your referral code: {{referral_code}}."}
}
func TestMessageTemplateValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*referrals.MessageTemplateInput)
	}{
		{"unknown field", func(i *referrals.MessageTemplateInput) { i.Body = "{{commission}}" }},
		{"template execution", func(i *referrals.MessageTemplateInput) { i.Body = "{{if .Name}}" }},
		{"unfinished", func(i *referrals.MessageTemplateInput) { i.Body = "Hi {{lead_name" }},
		{"unopened", func(i *referrals.MessageTemplateInput) { i.Subject = "company}}" }},
		{"nested", func(i *referrals.MessageTemplateInput) { i.Body = "{{{{lead_name}}}}" }},
		{"category", func(i *referrals.MessageTemplateInput) { i.Category = "pricing" }},
		{"stage", func(i *referrals.MessageTemplateInput) { i.Stage = "paid" }},
		{"status", func(i *referrals.MessageTemplateInput) { i.Status = "published" }},
		{"version", func(i *referrals.MessageTemplateInput) { i.Version = -1 }},
		{"title", func(i *referrals.MessageTemplateInput) { i.Title = "  " }},
		{"title line", func(i *referrals.MessageTemplateInput) { i.Title = "Hello\nWorld" }},
		{"subject line", func(i *referrals.MessageTemplateInput) { i.Subject = "Hello\r\nBcc: someone" }},
		{"body bytes", func(i *referrals.MessageTemplateInput) { i.Body = strings.Repeat("界", 1334) }},
		{"invalid UTF8", func(i *referrals.MessageTemplateInput) { i.Body = string([]byte{0xff}) }},
		{"nul", func(i *referrals.MessageTemplateInput) { i.Body = "Hello\x00" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := messageInput()
			tc.change(&in)
			if err := referrals.ValidateMessageTemplate(in); !errors.Is(err, referrals.ErrInvalidTemplate) {
				t.Fatal(err)
			}
		})
	}
	in := messageInput()
	if err := referrals.ValidateMessageTemplate(in); err != nil {
		t.Fatal(err)
	}
	in.Body = strings.Repeat("x", 4000)
	if err := referrals.ValidateMessageTemplate(in); err != nil {
		t.Fatal(err)
	}
}
func TestMessagePersonalizationIsLiteralAndReportsMissing(t *testing.T) {
	in := messageInput()
	in.Subject = "For {{ lead_name }}"
	in.Body = "{{company}} / {{company}} / {{referral_code}} / {{salesperson_email}} / {{lead_email}}"
	p, err := referrals.PersonalizeMessage(in, referrals.TemplateLead{Name: "<img src=x onerror=alert(1)>{{company}}", OwnerEmail: "rep@example.com", Email: "maya@example.com"})
	if err != nil || p.Subject != "For <img src=x onerror=alert(1)>{{company}}" || strings.Join(p.Missing, ",") != "company,referral_code" || !strings.Contains(p.Body, "{{company}} / {{company}}") {
		t.Fatal(p, err)
	}
	p, err = referrals.PersonalizeMessage(messageInput(), referrals.TemplateLead{Name: "Maya", Company: "Orbit", OwnerEmail: "rep@example.com", ReferralCode: "POJU43"})
	if err != nil || len(p.Missing) != 0 || !strings.Contains(p.Body, "POJU43") || p.Subject != "A demo for Orbit" {
		t.Fatal(p, err)
	}
}
