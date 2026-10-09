package edgeruletrace

// Proposed-change traces: overlay a draft edit (rules to add, update or
// remove) on an app's current rules, simulate the same request against both
// sets, and report what the change would do to that request before it is
// applied. The proposal never touches the server.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// proposalKinds is the closed edge-rule kind vocabulary (the authoritative
// list is state.EdgeRuleKind / the edge_rules kind CHECK).
var proposalKinds = map[string]bool{
	"route": true, "rewrite": true, "redirect": true, "headers": true, "cors": true,
	"jwt": true, "ip": true, "validate": true, "limit": true, "geo": true,
	"maintenance": true, "throttle": true, "budget": true, "cache": true,
	"respond": true, "retry": true, "circuit_breaker": true, "async": true,
}

// ProposedRuleIDPrefix marks rules that exist only in a proposal.
const ProposedRuleIDPrefix = "proposed-"

// Proposal is a draft edit of an app's edge rules. Update keys and Remove
// entries are existing rule IDs.
type Proposal struct {
	Add    []api.CreateEdgeRuleRequest          `json:"add,omitempty"`
	Update map[string]api.UpdateEdgeRuleRequest `json:"update,omitempty"`
	Remove []string                             `json:"remove,omitempty"`
}

// Empty reports whether the proposal changes nothing.
func (p Proposal) Empty() bool {
	return len(p.Add) == 0 && len(p.Update) == 0 && len(p.Remove) == 0
}

// ParseProposal decodes a proposal document, rejecting unknown fields so a
// typo cannot silently drop part of the draft.
func ParseProposal(raw []byte) (Proposal, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var p Proposal
	if err := dec.Decode(&p); err != nil {
		return Proposal{}, fmt.Errorf("proposal: %w", err)
	}
	return p, nil
}

// ApplyProposal returns the rule set the app would have after the proposal.
// Added rules get IDs "proposed-1", "proposed-2", ... and creation times
// after every existing rule, so among equal priorities they evaluate last,
// as a newly created rule would on the gateway. Validation is structural
// (known kind, JSON action, existing IDs, bounds); the server still applies
// its full per-kind and plan validation when the change is made.
func ApplyProposal(rules []api.EdgeRuleResponse, p Proposal, now time.Time) ([]api.EdgeRuleResponse, error) {
	out := make([]api.EdgeRuleResponse, 0, len(rules)+len(p.Add))
	index := map[string]int{}
	appID := ""
	latest := now
	for _, rule := range rules {
		index[rule.ID] = len(out)
		out = append(out, rule)
		appID = rule.AppID
		if rule.CreatedAt.After(latest) {
			latest = rule.CreatedAt
		}
	}
	removed := map[string]bool{}
	for _, id := range p.Remove {
		if _, ok := index[id]; !ok {
			return nil, fmt.Errorf("proposal: remove %q: no such rule on this app", id)
		}
		removed[id] = true
	}
	ids := make([]string, 0, len(p.Update))
	for id := range p.Update {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		i, ok := index[id]
		if !ok {
			return nil, fmt.Errorf("proposal: update %q: no such rule on this app", id)
		}
		if removed[id] {
			return nil, fmt.Errorf("proposal: rule %q is both updated and removed", id)
		}
		updated, err := applyRuleUpdate(out[i], p.Update[id], now)
		if err != nil {
			return nil, fmt.Errorf("proposal: update %q: %w", id, err)
		}
		out[i] = updated
	}
	kept := out[:0]
	for _, rule := range out {
		if !removed[rule.ID] {
			kept = append(kept, rule)
		}
	}
	out = kept
	for n, req := range p.Add {
		created, err := proposedRule(req, appID, latest.Add(time.Duration(n+1)*time.Microsecond), n+1, now)
		if err != nil {
			return nil, fmt.Errorf("proposal: add #%d: %w", n+1, err)
		}
		out = append(out, created)
	}
	return out, nil
}

func proposedRule(req api.CreateEdgeRuleRequest, appID string, createdAt time.Time, n int, now time.Time) (api.EdgeRuleResponse, error) {
	if !proposalKinds[req.Kind] {
		return api.EdgeRuleResponse{}, fmt.Errorf("kind %q is not an edge-rule kind", req.Kind)
	}
	if strings.TrimSpace(req.MatchHost) == "" {
		return api.EdgeRuleResponse{}, fmt.Errorf("match_host is required")
	}
	if err := requireJSONObject(req.Action); err != nil {
		return api.EdgeRuleResponse{}, err
	}
	if prob := api.ValidateEdgeRuleMetadata(&req.Name, &req.Description, req.ExpiresAt, now); prob != nil {
		return api.EdgeRuleResponse{}, fmt.Errorf("%s", prob.Detail)
	}
	if prob := api.ValidateEdgeRuleMatch(req.Match); prob != nil {
		return api.EdgeRuleResponse{}, fmt.Errorf("%s", prob.Detail)
	}
	matchPath := req.MatchPath
	if matchPath == "" {
		matchPath = "/"
	}
	priority, enabled := 100, true
	if req.Priority != nil {
		priority = *req.Priority
	}
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if priority < 0 || priority > 10000 {
		return api.EdgeRuleResponse{}, fmt.Errorf("priority must be in 0..10000 (got %d)", priority)
	}
	return api.EdgeRuleResponse{
		ID: fmt.Sprintf("%s%d", ProposedRuleIDPrefix, n), AppID: appID,
		MatchHost: strings.ToLower(req.MatchHost), MatchPath: matchPath,
		MatchMethods: req.MatchMethods, MatchHeaders: req.MatchHeaders,
		Priority: priority, Enabled: enabled, Kind: req.Kind,
		ValidateMode: req.ValidateMode, Action: req.Action,
		Name: strings.TrimSpace(req.Name), Description: req.Description,
		ExpiresAt: req.ExpiresAt, Match: req.Match, CreatedAt: createdAt, UpdatedAt: createdAt,
	}, nil
}

func applyRuleUpdate(rule api.EdgeRuleResponse, req api.UpdateEdgeRuleRequest, now time.Time) (api.EdgeRuleResponse, error) {
	if req.ExpiresAt != nil && req.ClearExpiresAt {
		return rule, fmt.Errorf("expires_at and clear_expires_at are mutually exclusive")
	}
	if prob := api.ValidateEdgeRuleMetadata(req.Name, req.Description, req.ExpiresAt, now); prob != nil {
		return rule, fmt.Errorf("%s", prob.Detail)
	}
	if req.Match != nil && req.ClearMatch {
		return rule, fmt.Errorf("match and clear_match are mutually exclusive")
	}
	if prob := api.ValidateEdgeRuleMatch(req.Match); prob != nil {
		return rule, fmt.Errorf("%s", prob.Detail)
	}
	switch {
	case req.ClearMatch:
		rule.Match = nil
	case req.Match != nil:
		rule.Match = req.Match
	}
	if req.MatchHost != nil {
		rule.MatchHost = strings.ToLower(*req.MatchHost)
	}
	if req.MatchPath != nil {
		rule.MatchPath = *req.MatchPath
	}
	if req.MatchMethods != nil {
		rule.MatchMethods = *req.MatchMethods
	}
	if req.MatchHeaders != nil {
		rule.MatchHeaders = *req.MatchHeaders
	}
	if req.Priority != nil {
		if *req.Priority < 0 || *req.Priority > 10000 {
			return rule, fmt.Errorf("priority must be in 0..10000 (got %d)", *req.Priority)
		}
		rule.Priority = *req.Priority
	}
	if req.Enabled != nil {
		rule.Enabled = *req.Enabled
	}
	if req.ValidateMode != nil && *req.ValidateMode != "" {
		rule.ValidateMode = *req.ValidateMode
	}
	if req.Action != nil {
		if err := requireJSONObject(*req.Action); err != nil {
			return rule, err
		}
		rule.Action = *req.Action
	}
	if req.Name != nil {
		rule.Name = strings.TrimSpace(*req.Name)
	}
	if req.Description != nil {
		rule.Description = *req.Description
	}
	switch {
	case req.ClearExpiresAt:
		rule.ExpiresAt, rule.Expired = nil, false
	case req.ExpiresAt != nil:
		at := *req.ExpiresAt
		rule.ExpiresAt, rule.Expired = &at, false
	}
	rule.UpdatedAt = now
	return rule, nil
}

func requireJSONObject(raw json.RawMessage) error {
	var obj map[string]json.RawMessage
	if len(bytes.TrimSpace(raw)) == 0 || json.Unmarshal(raw, &obj) != nil {
		return fmt.Errorf("action must be a JSON object")
	}
	return nil
}

// Comparison is a request simulated against the current and the proposed
// rule sets.
type Comparison struct {
	Current     Result   `json:"current"`
	Proposed    Result   `json:"proposed"`
	Changed     bool     `json:"changed"`
	Differences []string `json:"differences,omitempty"`
}

// SimulateProposal runs the same request against rules and against rules
// with the proposal applied, and reports what changes for that request.
func SimulateProposal(input Input, rules []api.EdgeRuleResponse, p Proposal, now time.Time) (Comparison, error) {
	proposed, err := ApplyProposal(rules, p, now)
	if err != nil {
		return Comparison{}, err
	}
	current, err := Simulate(input, rules)
	if err != nil {
		return Comparison{}, err
	}
	after, err := Simulate(input, proposed)
	if err != nil {
		return Comparison{}, err
	}
	diffs := compareSimulations(current.Simulation, after.Simulation)
	diffs = append(diffs, compareRuleRows(current.Rules, after.Rules)...)
	return Comparison{Current: current, Proposed: after, Changed: len(diffs) > 0, Differences: diffs}, nil
}

func compareSimulations(before, after Simulation) []string {
	var out []string
	field := func(name string, b, a any) {
		bj, _ := json.Marshal(b)
		aj, _ := json.Marshal(a)
		if !bytes.Equal(bj, aj) {
			out = append(out, fmt.Sprintf("%s: %s → %s", name, describe(bj), describe(aj)))
		}
	}
	field("outcome", before.Outcome, after.Outcome)
	field("status", before.Status, after.Status)
	field("status_code", before.StatusCode, after.StatusCode)
	field("problem_code", before.ProblemCode, after.ProblemCode)
	field("stopped_at", before.StoppedAt, after.StoppedAt)
	field("final_path", before.FinalPath, after.FinalPath)
	field("location", before.Location, after.Location)
	field("target_app", before.TargetApp, after.TargetApp)
	field("retry_after_seconds", before.RetryAfterSeconds, after.RetryAfterSeconds)
	field("request_headers", before.RequestHeaders, after.RequestHeaders)
	field("response_header_ops", before.ResponseHeaderOps, after.ResponseHeaderOps)
	return out
}

func compareRuleRows(before, after []RuleRow) []string {
	prior := map[string]RuleRow{}
	for _, row := range before {
		prior[row.ID] = row
	}
	var out []string
	seen := map[string]bool{}
	for _, row := range after {
		seen[row.ID] = true
		old, existed := prior[row.ID]
		switch {
		case !existed && row.Status == "skipped":
			// A proposed rule that does not apply to this request does not
			// change what happens to it.
		case !existed:
			out = append(out, fmt.Sprintf("rule %s (%s, new): %s", row.ID, row.Kind, rowState(row)))
		case old.Status != row.Status || old.Outcome != row.Outcome:
			out = append(out, fmt.Sprintf("rule %s (%s): %s → %s", row.ID, row.Kind, rowState(old), rowState(row)))
		}
	}
	for _, row := range before {
		if !seen[row.ID] && row.Status != "skipped" {
			out = append(out, fmt.Sprintf("rule %s (%s, removed): was %s", row.ID, row.Kind, rowState(row)))
		}
	}
	return out
}

func rowState(row RuleRow) string {
	if row.Outcome != "" {
		return row.Status + "/" + row.Outcome
	}
	return row.Status
}

func describe(raw []byte) string {
	s := string(raw)
	if s == `""` || s == "null" || s == "0" {
		return "(none)"
	}
	return s
}
