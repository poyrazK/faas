package api

import "slices"

// Moderation actions, kinds and failure policies (ADR-1340).
const (
	OutboundAIModerationOff   = "off"
	OutboundAIModerationFlag  = "flag"
	OutboundAIModerationBlock = "block"

	// OutboundAIModerationKindOpenAI calls OpenAI's /v1/moderations.
	OutboundAIModerationKindOpenAI = "openai_moderation"
	// OutboundAIModerationKindClassifier asks a chat model which categories
	// apply, through the route translators.
	OutboundAIModerationKindClassifier = "classifier"

	OutboundAIModerationOnErrorAllow = "allow"
	OutboundAIModerationOnErrorBlock = "block"
)

// Moderation categories (ADR-1340). OpenAI's moderation endpoint has no
// prompt-injection category, so only a classifier can check it.
const (
	OutboundAIModerationPromptInjection = "prompt_injection"
	OutboundAIModerationHarassment      = "harassment"
	OutboundAIModerationHate            = "hate"
	OutboundAIModerationSelfHarm        = "self_harm"
	OutboundAIModerationSexual          = "sexual"
	OutboundAIModerationViolence        = "violence"
	OutboundAIModerationIllicit         = "illicit"
)

// OutboundAIModerationCategories lists every category in a stable order.
var OutboundAIModerationCategories = []string{
	OutboundAIModerationPromptInjection, OutboundAIModerationHarassment, OutboundAIModerationHate,
	OutboundAIModerationSelfHarm, OutboundAIModerationSexual, OutboundAIModerationViolence, OutboundAIModerationIllicit,
}

// OutboundAIModeration checks an AI request with a model before the provider
// call (ADR-1340). The check runs through another of the account's AI
// integrations and is billed by that integration's provider. Flag lets the
// call through and names the categories in X-Gregale-AI-Moderation; block
// refuses it. OnError decides what a failed check does.
type OutboundAIModeration struct {
	Action        string   `json:"action"`
	Kind          string   `json:"kind,omitempty"`
	IntegrationID string   `json:"integration_id,omitempty"`
	Model         string   `json:"model,omitempty"`
	Categories    []string `json:"categories"`
	OnError       string   `json:"on_error"`
}

// Normalized fills the action and failure policy and, when the check is
// off, clears everything else.
func (m OutboundAIModeration) Normalized() OutboundAIModeration {
	return m.normalized(false)
}

// normalized keeps an off check's settings when responses use it
// (ADR-1435).
func (m OutboundAIModeration) normalized(keep bool) OutboundAIModeration {
	if m.Action == "" {
		m.Action = OutboundAIModerationOff
	}
	if m.OnError == "" {
		m.OnError = OutboundAIModerationOnErrorAllow
	}
	if m.Action == OutboundAIModerationOff && !keep {
		return OutboundAIModeration{Action: OutboundAIModerationOff, Categories: []string{}, OnError: m.OnError}
	}
	if m.Categories == nil {
		m.Categories = []string{}
	}
	return m
}

// OutboundAIModerationResponseCategories is a check's categories that
// apply to responses: all but prompt_injection (ADR-1435).
func OutboundAIModerationResponseCategories(categories []string) []string {
	out := make([]string, 0, len(categories))
	for _, category := range categories {
		if category != OutboundAIModerationPromptInjection {
			out = append(out, category)
		}
	}
	return out
}

// Active reports whether requests are checked.
func (m OutboundAIModeration) Active() bool {
	return m.Normalized().Action != OutboundAIModerationOff
}

// Valid checks the shape of a check. That the integration is the account's
// own AI integration, of a provider the kind can call, is checked when the
// policy is stored.
func (m OutboundAIModeration) Valid() bool {
	return m.validFor(false)
}

// validFor also checks an off check's settings when responses use it: they
// need a category besides prompt_injection, which responses skip
// (ADR-1435).
func (m OutboundAIModeration) validFor(responses bool) bool {
	m = m.normalized(responses)
	if m.OnError != OutboundAIModerationOnErrorAllow && m.OnError != OutboundAIModerationOnErrorBlock {
		return false
	}
	switch m.Action {
	case OutboundAIModerationOff:
		if !responses {
			return true
		}
	case OutboundAIModerationFlag, OutboundAIModerationBlock:
	default:
		return false
	}
	if responses && len(OutboundAIModerationResponseCategories(m.Categories)) == 0 {
		return false
	}
	if m.IntegrationID == "" || len(m.Categories) == 0 || len(m.Categories) > len(OutboundAIModerationCategories) ||
		(m.Model != "" && !validAIModelText(m.Model)) {
		return false
	}
	switch m.Kind {
	case OutboundAIModerationKindOpenAI:
		if slices.Contains(m.Categories, OutboundAIModerationPromptInjection) {
			return false
		}
	case OutboundAIModerationKindClassifier:
		if m.Model == "" {
			return false
		}
	default:
		return false
	}
	seen := make(map[string]struct{}, len(m.Categories))
	for _, category := range m.Categories {
		if _, dup := seen[category]; dup || !slices.Contains(OutboundAIModerationCategories, category) {
			return false
		}
		seen[category] = struct{}{}
	}
	return true
}
