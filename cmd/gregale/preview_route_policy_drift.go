package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

type previewRoutePolicyDriftEvidence struct {
	Status        string `json:"status"`
	Scope         string `json:"scope"`
	Reason        string `json:"reason,omitempty"`
	ChangedRoutes int    `json:"changed_routes"`
	UnknownRoutes int    `json:"unknown_routes"`
}

type previewRoutePolicyDrift struct {
	Status  string                         `json:"status"`
	Reason  string                         `json:"reason,omitempty"`
	Changes []previewRoutePolicyRuleChange `json:"changes"`
}

type previewRoutePolicyRuleChange struct {
	Change        string                   `json:"change"`
	ChangedFields []string                 `json:"changed_fields,omitempty"`
	Before        *previewPolicyRuleSource `json:"before,omitempty"`
	After         *previewPolicyRuleSource `json:"after,omitempty"`
}

// Policy actions and match-header values are intentionally excluded from
// shareable preview reports. Their canonical hashes are used only in memory
// to detect changes.
type previewPolicyRuleSource struct {
	ID                   string   `json:"rule_id"`
	Kind                 string   `json:"kind"`
	Priority             int      `json:"priority"`
	Enabled              bool     `json:"enabled"`
	MatchPath            string   `json:"match_path"`
	MatchMethods         []string `json:"match_methods"`
	HostScoped           bool     `json:"host_scoped"`
	HeaderConditionCount int      `json:"header_condition_count"`
}

type previewPolicyRuleIdentity struct {
	base []api.EdgeRuleResponse
	new  []api.EdgeRuleResponse
}

func attachPreviewRoutePolicyDrift(report *previewRouteReport, baseline, candidate []api.EdgeRuleResponse, baselineErr, candidateErr error) {
	report.PolicyDrift = previewRoutePolicyDriftEvidence{Status: "unavailable", Scope: "current_app_pair"}
	if baselineErr != nil {
		report.PolicyDrift.Reason = "baseline:" + previewReportReadReason(baselineErr)
		return
	}
	if candidateErr != nil {
		report.PolicyDrift.Reason = "candidate:" + previewReportReadReason(candidateErr)
		return
	}

	report.PolicyDrift.Status = "available"
	report.PolicyDrift.Reason = "current_app_configuration"
	for index := range report.Routes {
		row := &report.Routes[index]
		changes, complete := diffPreviewRoutePolicy(row, baseline, candidate)
		if !complete {
			row.PolicyDrift = &previewRoutePolicyDrift{Status: "unknown", Reason: "invalid_or_uncomparable_rule_configuration", Changes: changes}
			report.PolicyDrift.UnknownRoutes++
			continue
		}
		if len(changes) > 0 {
			row.PolicyDrift = &previewRoutePolicyDrift{Status: "changed", Changes: changes}
			report.PolicyDrift.ChangedRoutes++
		} else {
			row.PolicyDrift = &previewRoutePolicyDrift{Status: "unchanged", Changes: []previewRoutePolicyRuleChange{}}
		}
	}
	if report.PolicyDrift.UnknownRoutes > 0 {
		report.PolicyDrift.Status = "partial"
		report.PolicyDrift.Reason = "some_rule_actions_could_not_be_compared"
	} else if report.Contract.Status != "available" {
		report.PolicyDrift.Status = "partial"
		report.PolicyDrift.Reason = "captured_route_inventory_unavailable"
	}
	report.Notes = append(report.Notes,
		"Policy drift compares current parent and preview app rule configuration, not deployment-time snapshots or every possible request context; rule actions and header values are redacted.")
}

func diffPreviewRoutePolicy(route *previewReportRoute, baseline, candidate []api.EdgeRuleResponse) ([]previewRoutePolicyRuleChange, bool) {
	baseRules, complete := previewMatchingPolicyRules(*route, baseline)
	if !complete {
		return nil, false
	}
	candidateRules, complete := previewMatchingPolicyRules(*route, candidate)
	if !complete {
		return nil, false
	}
	groups := map[string]*previewPolicyRuleIdentity{}
	for _, rule := range baseRules {
		key, ok := previewPolicyRuleKey(rule)
		if !ok {
			return nil, false
		}
		group := groups[key]
		if group == nil {
			group = &previewPolicyRuleIdentity{}
			groups[key] = group
		}
		group.base = append(group.base, rule)
	}
	for _, rule := range candidateRules {
		key, ok := previewPolicyRuleKey(rule)
		if !ok {
			return nil, false
		}
		group := groups[key]
		if group == nil {
			group = &previewPolicyRuleIdentity{}
			groups[key] = group
		}
		group.new = append(group.new, rule)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	changes := make([]previewRoutePolicyRuleChange, 0)
	for _, key := range keys {
		group := groups[key]
		sort.Slice(group.base, func(i, j int) bool { return previewPolicyRuleOrder(group.base[i], group.base[j]) })
		sort.Slice(group.new, func(i, j int) bool { return previewPolicyRuleOrder(group.new[i], group.new[j]) })
		paired := min(len(group.base), len(group.new))
		for i := 0; i < paired; i++ {
			fields, ok := previewPolicyRuleChangedFields(group.base[i], group.new[i])
			if !ok {
				return nil, false
			}
			if len(fields) > 0 {
				changes = append(changes, previewRoutePolicyRuleChange{Change: "modified", ChangedFields: fields,
					Before: previewPolicyRuleSourceOf(group.base[i]), After: previewPolicyRuleSourceOf(group.new[i])})
			}
		}
		for _, rule := range group.base[paired:] {
			changes = append(changes, previewRoutePolicyRuleChange{Change: "removed", Before: previewPolicyRuleSourceOf(rule)})
		}
		for _, rule := range group.new[paired:] {
			changes = append(changes, previewRoutePolicyRuleChange{Change: "added", After: previewPolicyRuleSourceOf(rule)})
		}
	}
	sort.Slice(changes, func(i, j int) bool {
		left, right := previewPolicyChangeOrder(changes[i]), previewPolicyChangeOrder(changes[j])
		return left < right
	})
	return changes, true
}

func previewMatchingPolicyRules(route previewReportRoute, rules []api.EdgeRuleResponse) ([]api.EdgeRuleResponse, bool) {
	path := previewPolicySamplePath(route.Path)
	method := strings.ToUpper(route.Method)
	matched := make([]api.EdgeRuleResponse, 0)
	for _, rule := range rules {
		if len(rule.MatchMethods) > 0 && !previewPolicyMethodMatches(rule.MatchMethods, method) {
			continue
		}
		if rule.MatchPath != "" && rule.MatchPath != "*" {
			ok, err := api.MatchEdgeRulePath(rule.MatchPath, path)
			if err != nil {
				return nil, false
			}
			if !ok {
				continue
			}
		}
		matched = append(matched, rule)
	}
	sort.Slice(matched, func(i, j int) bool { return previewPolicyRuleOrder(matched[i], matched[j]) })
	return matched, true
}

func previewPolicySamplePath(route string) string {
	segments := strings.Split(route, "/")
	for index, segment := range segments {
		if len(segment) > 2 && strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			segments[index] = "route-parameter"
		}
	}
	return strings.Join(segments, "/")
}

func previewPolicyMethodMatches(methods []string, method string) bool {
	for _, candidate := range methods {
		if strings.EqualFold(strings.TrimSpace(candidate), method) {
			return true
		}
	}
	return false
}

func previewPolicyRuleOrder(left, right api.EdgeRuleResponse) bool {
	if left.Priority != right.Priority {
		return left.Priority < right.Priority
	}
	if left.Kind != right.Kind {
		return left.Kind < right.Kind
	}
	return left.ID < right.ID
}

func previewPolicyRuleKey(rule api.EdgeRuleResponse) (string, bool) {
	methodSet := make(map[string]bool, len(rule.MatchMethods))
	for _, method := range rule.MatchMethods {
		method = strings.ToUpper(strings.TrimSpace(method))
		if method != "" {
			methodSet[method] = true
		}
	}
	methods := make([]string, 0, len(methodSet))
	for method := range methodSet {
		methods = append(methods, method)
	}
	sort.Strings(methods)
	type headerSelector struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	headerSelectors := make([]headerSelector, 0, len(rule.MatchHeaders))
	headerNames := make(map[string]bool, len(rule.MatchHeaders))
	for name, value := range rule.MatchHeaders {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" || headerNames[name] {
			return "", false
		}
		headerNames[name] = true
		headerSelectors = append(headerSelectors, headerSelector{Name: name, Value: value})
	}
	sort.Slice(headerSelectors, func(i, j int) bool { return headerSelectors[i].Name < headerSelectors[j].Name })
	headerBytes, err := json.Marshal(headerSelectors)
	if err != nil {
		return "", false
	}
	parts := []string{rule.Kind, strings.ToLower(strings.TrimSpace(rule.MatchHost)), rule.MatchPath,
		strings.Join(methods, ","), string(headerBytes)}
	return strings.Join(parts, "\x00"), true
}

func previewPolicyRuleChangedFields(before, after api.EdgeRuleResponse) ([]string, bool) {
	fields := make([]string, 0, 4)
	if before.Priority != after.Priority {
		fields = append(fields, "priority")
	}
	if before.Enabled != after.Enabled {
		fields = append(fields, "enabled")
	}
	if before.ValidateMode != after.ValidateMode {
		fields = append(fields, "validate_mode")
	}
	beforeAction, beforeOK := previewPolicyActionDigest(before.Action)
	afterAction, afterOK := previewPolicyActionDigest(after.Action)
	if !beforeOK || !afterOK {
		return nil, false
	}
	if beforeAction != afterAction {
		fields = append(fields, "action")
	}
	return fields, true
}

func previewPolicyActionDigest(action json.RawMessage) ([32]byte, bool) {
	if len(strings.TrimSpace(string(action))) == 0 {
		return [32]byte{}, false
	}
	decoder := json.NewDecoder(strings.NewReader(string(action)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return [32]byte{}, false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return [32]byte{}, false
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return [32]byte{}, false
	}
	return sha256.Sum256(canonical), true
}

func previewPolicyRuleSourceOf(rule api.EdgeRuleResponse) *previewPolicyRuleSource {
	methods := make([]string, 0, len(rule.MatchMethods))
	for _, method := range rule.MatchMethods {
		methods = append(methods, strings.ToUpper(strings.TrimSpace(method)))
	}
	sort.Strings(methods)
	return &previewPolicyRuleSource{
		ID: rule.ID, Kind: rule.Kind, Priority: rule.Priority, Enabled: rule.Enabled,
		MatchPath: rule.MatchPath, MatchMethods: methods, HostScoped: strings.TrimSpace(rule.MatchHost) != "" && rule.MatchHost != "*",
		HeaderConditionCount: len(rule.MatchHeaders),
	}
}

func previewPolicyChangeOrder(change previewRoutePolicyRuleChange) string {
	part := func(rule *previewPolicyRuleSource) string {
		if rule == nil {
			return ""
		}
		return fmt.Sprintf("%s\x00%05d\x00%s\x00%s", rule.Kind, rule.Priority, rule.MatchPath, rule.ID)
	}
	return change.Change + "\x00" + part(change.Before) + "\x00" + part(change.After)
}

func previewReportHasPolicyDrift(report previewRouteReport) bool {
	if report.PolicyDrift.Status != "available" {
		return true
	}
	for _, route := range report.Routes {
		if route.PolicyDrift == nil || route.PolicyDrift.Status != "unchanged" {
			return true
		}
	}
	return false
}
