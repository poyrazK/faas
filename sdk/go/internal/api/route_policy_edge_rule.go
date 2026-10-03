package api

import "encoding/json"

type CreateEdgeRuleRequest struct {
	MatchHost    string            `json:"match_host"`
	MatchPath    string            `json:"match_path"`
	MatchMethods []string          `json:"match_methods,omitempty"`
	MatchHeaders map[string]string `json:"match_headers,omitempty"`
	Priority     *int              `json:"priority,omitempty"`
	Enabled      *bool             `json:"enabled,omitempty"`
	Kind         string            `json:"kind"`
	ValidateMode string            `json:"validate_mode,omitempty"`
	Action       json.RawMessage   `json:"action"`
}

type UpdateEdgeRuleRequest struct {
	MatchHost    *string            `json:"match_host,omitempty"`
	MatchPath    *string            `json:"match_path,omitempty"`
	MatchMethods *[]string          `json:"match_methods,omitempty"`
	MatchHeaders *map[string]string `json:"match_headers,omitempty"`
	Priority     *int               `json:"priority,omitempty"`
	Enabled      *bool              `json:"enabled,omitempty"`
	ValidateMode *string            `json:"validate_mode,omitempty"`
	Action       *json.RawMessage   `json:"action,omitempty"`
}
