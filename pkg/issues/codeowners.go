package issues

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

// CodeownersImport translates the supported literal-path subset into Gregale's
// prospective ownership policy. RuleLines identify original lines in priority order.
type CodeownersImport struct {
	Rules     []api.IssueOwnershipRule `json:"rules"`
	RuleLines []int                    `json:"rule_lines"`
}

// CodeownerTarget accepts a legacy account UUID string or an explicit account/team target.
type CodeownerTarget struct {
	AccountID string `json:"account_id,omitempty"`
	TeamID    string `json:"team_id,omitempty"`
}

func (t *CodeownerTarget) UnmarshalJSON(raw []byte) error {
	var id string
	if len(raw) > 0 && raw[0] == '"' {
		if err := json.Unmarshal(raw, &id); err != nil {
			return err
		}
		t.AccountID = id
		return nil
	}
	type target CodeownerTarget
	var in target
	if err := json.Unmarshal(raw, &in); err != nil {
		return err
	}
	*t = CodeownerTarget(in)
	return nil
}

// CompileCodeowners preserves account-only bindings for existing callers.
func CompileCodeowners(contents, sourceRoot string, bindings map[string]string) (CodeownersImport, error) {
	targets := map[string]CodeownerTarget{}
	for owner, id := range bindings {
		targets[owner] = CodeownerTarget{AccountID: id}
	}
	return CompileCodeownersTargets(contents, sourceRoot, targets)
}

// CompileCodeownersTargets never fetches repository or account data. Every owner token
// must have an explicit account or team binding; the first listed owner is selected.
// Reverse line order preserves CODEOWNERS' last-matching-line precedence.
func CompileCodeownersTargets(contents, sourceRoot string, bindings map[string]CodeownerTarget) (CodeownersImport, error) {
	out := CodeownersImport{Rules: []api.IssueOwnershipRule{}, RuleLines: []int{}}
	if len(contents) > api.IssueCodeownersMaxBytes || !utf8.ValidString(contents) {
		return out, fmt.Errorf("CODEOWNERS must be valid UTF-8 within %d bytes", api.IssueCodeownersMaxBytes)
	}
	root, err := api.NormalizeIssueSourcePathPrefix(sourceRoot)
	if err != nil {
		return out, fmt.Errorf("invalid deployed source root: %w", err)
	}
	if len(bindings) > api.IssueCodeownersMaxBindings {
		return out, fmt.Errorf("too many owner bindings")
	}
	owners := map[string]CodeownerTarget{}
	for raw, id := range bindings {
		key := strings.ToLower(strings.TrimSpace(raw))
		account, team, err := api.NormalizeIssueAssignment(id.AccountID, id.TeamID)
		if key == "" || !strings.Contains(key, "@") || strings.ContainsAny(key, " \t\r\n") || err != nil || account == "" && team == "" {
			return out, fmt.Errorf("owner bindings must map handles or emails to one nonzero account or team UUID")
		}
		if previous, ok := owners[key]; ok && previous != (CodeownerTarget{AccountID: account, TeamID: team}) {
			return out, fmt.Errorf("conflicting case-insensitive owner bindings")
		}
		owners[key] = CodeownerTarget{AccountID: account, TeamID: team}
	}
	lines := strings.Split(contents, "\n")
	if len(lines) > api.IssueCodeownersMaxLines {
		return out, fmt.Errorf("too many CODEOWNERS lines")
	}
	for i, line := range lines {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		for j, field := range fields {
			if strings.HasPrefix(field, "#") {
				fields = fields[:j]
				break
			}
		}
		if len(fields) < 2 {
			return out, fmt.Errorf("line %d: ownerless entries cannot be represented by this ownership policy", i+1)
		}
		pattern := fields[0]
		prefix := root
		if pattern != "*" && pattern != "/" && pattern != "/**" {
			if strings.Contains(pattern, "://") || strings.ContainsAny(pattern, `*?[]!\#`) || !strings.HasPrefix(pattern, "/") && !strings.Contains(strings.TrimSuffix(pattern, "/"), "/") {
				return out, fmt.Errorf("line %d: unsupported pattern; use a root default or literal root-anchored paths", i+1)
			}
			for _, part := range strings.Split(pattern, "/") {
				if part == ".." || part == "." {
					return out, fmt.Errorf("line %d: traversal or dot segments are unsupported", i+1)
				}
			}
			prefix, err = api.NormalizeIssueSourcePathPrefix(path.Join(root, strings.TrimPrefix(pattern, "/")))
			if err != nil {
				return out, fmt.Errorf("line %d: invalid or oversized source path", i+1)
			}
		}
		selected := CodeownerTarget{}
		for j, owner := range fields[1:] {
			account, ok := owners[strings.ToLower(owner)]
			if !ok {
				return out, fmt.Errorf("line %d: every listed owner requires an explicit account or team binding", i+1)
			}
			if j == 0 {
				selected = account
			}
		}
		out.Rules = append(out.Rules, api.IssueOwnershipRule{SourcePathPrefix: prefix, AssigneeAccountID: selected.AccountID, AssigneeTeamID: selected.TeamID})
		out.RuleLines = append(out.RuleLines, i+1)
		if len(out.Rules) > api.IssueOwnershipRulesMax {
			return out, fmt.Errorf("CODEOWNERS exceeds the %d-rule ownership policy limit", api.IssueOwnershipRulesMax)
		}
	}
	for i, j := 0, len(out.Rules)-1; i < j; i, j = i+1, j-1 {
		out.Rules[i], out.Rules[j] = out.Rules[j], out.Rules[i]
		out.RuleLines[i], out.RuleLines[j] = out.RuleLines[j], out.RuleLines[i]
	}
	return out, nil
}
