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
}

// EdgeRuleRecipeSuggestionsResponse is GET /v1/apps/{slug}/edge-rules/suggestions.
type EdgeRuleRecipeSuggestionsResponse struct {
	Window      string                     `json:"window"`
	Since       time.Time                  `json:"since"`
	Suggestions []EdgeRuleRecipeSuggestion `json:"suggestions"`
}
