// commands_ai_content.go — `gregale ai content-policy` (ADR-997): an AI
// integration's content guardrails.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const aiContentPolicyUsage = "usage: gregale ai content-policy <integration-id> [--email off|redact|block] " +
	"[--payment-card off|redact|block] [--iban off|redact|block] [--secrets off|redact|block] [--block-term TERM]... [--clear-terms] " +
	"[--moderation off|flag|block] [--moderation-kind openai_moderation|classifier] [--moderation-integration ID] " +
	"[--moderation-model MODEL] [--moderation-categories a,b] [--moderation-on-error allow|block]"

// cmdAIContentPolicy shows or updates an integration's content guardrails.
// Flags left out keep their values.
func cmdAIContentPolicy(args []string) int {
	fs := newFlagSet("ai content-policy", flag.ContinueOnError)
	email := fs.String("email", "", "e-mail addresses: off, redact or block")
	card := fs.String("payment-card", "", "payment card numbers: off, redact or block")
	iban := fs.String("iban", "", "IBANs: off, redact or block")
	secrets := fs.String("secrets", "", "API keys, tokens and private keys: off, redact or block")
	var terms stringListFlag
	fs.Var(&terms, "block-term", "block requests that contain this term, ignoring case (repeatable)")
	clearTerms := fs.Bool("clear-terms", false, "remove every blocked term before adding --block-term values")
	moderation := moderationFlags(fs)
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 1 {
		return printErr("Invalid ai content-policy arguments", errors.New(aiContentPolicyUsage))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	current, err := findOutboundIntegration(client, fs.Arg(0))
	if err != nil {
		return printErr("Could not read integration", err)
	}
	policy := current.AIContentPolicy.Normalized()
	changed := len(terms) > 0 || *clearTerms
	for _, setting := range []struct {
		value *string
		dst   *string
	}{{email, &policy.Email}, {card, &policy.PaymentCard}, {iban, &policy.IBAN}, {secrets, &policy.Secret}} {
		if *setting.value != "" {
			*setting.dst, changed = *setting.value, true
		}
	}
	if *clearTerms {
		policy.BlockedTerms = []string{}
	}
	policy.BlockedTerms = append(policy.BlockedTerms, terms...)
	if moderation.apply(&policy.Moderation) {
		changed = true
	}
	if changed {
		if !policy.Valid() {
			return printErr("Invalid content policy", errors.New("actions are off, redact or block; at most 32 distinct terms of 1 to 128 bytes; "+
				"an active moderation check needs a kind, an integration, categories and, for a classifier, a model"))
		}
		if err := client.SetOutboundAIContentPolicy(context.Background(), fs.Arg(0), policy); err != nil {
			return printErr("Could not update the content policy", err)
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(policy))
	}
	_, _ = fmt.Fprintf(osStdout, "Content policy for %s\n  email: %s\n  payment cards: %s\n  IBANs: %s\n  secrets: %s\n  blocked terms: %s\n  moderation: %s\n",
		fs.Arg(0), policy.Email, policy.PaymentCard, policy.IBAN, policy.Secret, formatBlockedTerms(policy.BlockedTerms),
		formatModeration(policy.Moderation.Normalized()))
	return 0
}

// moderationFlagValues are the content-policy flags of a moderation check
// (ADR-1340); empty values keep the stored setting.
type moderationFlagValues struct {
	action, kind, integration, model, categories, onError *string
}

func moderationFlags(fs *flag.FlagSet) moderationFlagValues {
	return moderationFlagValues{
		action:      fs.String("moderation", "", "check requests with a model: off, flag (let through and name categories) or block"),
		kind:        fs.String("moderation-kind", "", "openai_moderation (OpenAI's free endpoint) or classifier (any chat model)"),
		integration: fs.String("moderation-integration", "", "the AI integration that runs the check"),
		model:       fs.String("moderation-model", "", "the check's model (required for classifier)"),
		categories: fs.String("moderation-categories", "",
			"comma-separated: prompt_injection, harassment, hate, self_harm, sexual, violence, illicit"),
		onError: fs.String("moderation-on-error", "", "when the check fails: allow or block"),
	}
}

// apply sets the given flags on a check and reports whether any was given.
func (v moderationFlagValues) apply(moderation *api.OutboundAIModeration) bool {
	changed := false
	for _, setting := range []struct {
		value *string
		dst   *string
	}{{v.action, &moderation.Action}, {v.kind, &moderation.Kind}, {v.integration, &moderation.IntegrationID},
		{v.model, &moderation.Model}, {v.onError, &moderation.OnError}} {
		if *setting.value != "" {
			*setting.dst, changed = *setting.value, true
		}
	}
	if *v.categories != "" {
		moderation.Categories, changed = strings.Split(*v.categories, ","), true
	}
	return changed
}

func formatModeration(moderation api.OutboundAIModeration) string {
	if moderation.Action == api.OutboundAIModerationOff {
		return "off"
	}
	via := moderation.IntegrationID
	if moderation.Model != "" {
		via += ":" + moderation.Model
	}
	return fmt.Sprintf("%s %s with %s via %s (on error: %s)", moderation.Action, strings.Join(moderation.Categories, ", "),
		moderation.Kind, via, moderation.OnError)
}

func formatBlockedTerms(terms []string) string {
	if len(terms) == 0 {
		return "none"
	}
	quoted := make([]string, 0, len(terms))
	for _, term := range terms {
		quoted = append(quoted, fmt.Sprintf("%q", term))
	}
	return strings.Join(quoted, ", ")
}
