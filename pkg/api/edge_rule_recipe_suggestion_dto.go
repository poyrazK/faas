package api

// ADR-20261020105000 wire types for GET /v1/apps/{slug}/edge-rules/suggestions. Kept
// apart from the suggestion logic so the spec-compliance scan sees only the
// response DTOs.

import "time"

// EdgeRuleRecipeSuggestion recommends one recipe with the parameters it should be
// added with and the observation behind it.
type EdgeRuleRecipeSuggestion struct {
	Recipe string            `json:"recipe"`
	Title  string            `json:"title"`
	Reason string            `json:"reason"`
	Params map[string]string `json:"params,omitempty"`
	// Alternative is a softer recipe for the same observation: challenge
	// browsers instead of rate-limiting or restricting by IP (ADR-831
	// amendment 7). Absent when there is none.
	Alternative *EdgeRuleRecipeSuggestionAlternative `json:"alternative,omitempty"`
}

// EdgeRuleRecipeSuggestionAlternative is the other recipe a suggestion can
// be applied with, and its parameters.
type EdgeRuleRecipeSuggestionAlternative struct {
	Recipe string            `json:"recipe"`
	Title  string            `json:"title"`
	Params map[string]string `json:"params,omitempty"`
}

// EdgeRuleRecipeSuggestionsResponse is GET /v1/apps/{slug}/edge-rules/suggestions.
type EdgeRuleRecipeSuggestionsResponse struct {
	Window      string                     `json:"window"`
	Since       time.Time                  `json:"since"`
	Suggestions []EdgeRuleRecipeSuggestion `json:"suggestions"`
}
