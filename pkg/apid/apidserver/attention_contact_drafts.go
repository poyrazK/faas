package apidserver

import (
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// attentionDraft is the prefilled contact message for one signal
// (ADR-20261113180000). Staff edit it before sending; it names only what
// the customer can already see in their own dashboard.
type attentionDraft struct {
	Subject, Category, Body string
}

var attentionDrafts = map[string]attentionDraft{
	api.AttentionKindDeployFailing: {"Your deployments of {subject} are failing", "deployment",
		"We noticed that the recent deployments of {subject} failed ({detail}). Would you like a hand getting it deploying again? Reply here with what you are seeing and we will look at the build with you."},
	api.AttentionKindAppOOM: {"{subject} is running out of memory", "deployment",
		"{subject} was stopped because it used more memory than its plan allows ({detail}). Reducing its memory use or moving it to a plan with more RAM usually fixes this. Reply here if you would like help finding where the memory goes."},
	api.AttentionKindBootFailing: {"{subject} is failing to start", "deployment",
		"We noticed that {subject} failed to start several times recently ({detail}). Reply here and we will help you find out why."},
	api.AttentionKindJobsFailing: {"Scheduled runs of {subject} are failing", "deployment",
		"The recent scheduled runs of the job {subject} did not succeed ({detail}). Reply here if you would like us to look at the run logs with you."},
	api.AttentionKindDomainStuck: {"Your domain {subject} is not serving yet", "deployment",
		"Your custom domain {subject} is not serving traffic yet ({detail}). Reply here and we will help you check the DNS records and the certificate."},
	api.AttentionKindRefusedBurst: {"Requests are being refused ({subject})", "other",
		"Many of your recent API requests were refused with {subject}. Reply here with what you are trying to do and we will help."},
	api.AttentionKindLimitPressure: {"You are reaching a plan limit ({subject})", "account",
		"Some of your requests were refused because of a plan limit ({subject}, {detail}). Reply here if you would like to talk about the limit or the plan options."},
	api.AttentionKindPaymentFailing: {"A payment for your Gregale account failed", "billing",
		"A recent payment for your account did not go through ({detail}). Updating the payment method in billing settings usually fixes this. Reply here if you need help."},
}

func attentionContactDraft(signal api.ObsAttentionSignal) attentionDraft {
	draft, ok := attentionDrafts[signal.Kind]
	if !ok {
		draft = attentionDraft{"Can we help?", "other", "We noticed something that may be getting in your way ({detail}). Reply here and we will help."}
	}
	detail := signal.Detail
	if detail == "" {
		detail = signal.Kind
	}
	fill := strings.NewReplacer("{subject}", signal.Subject, "{detail}", detail)
	subject := fill.Replace(draft.Subject)
	if len(subject) > api.SupportSubjectMaxBytes {
		subject = strings.ToValidUTF8(subject[:api.SupportSubjectMaxBytes], "")
	}
	return attentionDraft{Subject: subject, Category: draft.Category, Body: fill.Replace(draft.Body) + "\n\n— Gregale support"}
}
