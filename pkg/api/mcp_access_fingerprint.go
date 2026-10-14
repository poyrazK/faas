package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strings"
	"time"
)

// MCPAccessPolicyExpectation fences the stored policy observed by an audit.
// It is a concurrency condition, not proof of runtime authorization.
type MCPAccessPolicyExpectation struct {
	Resource        string `json:"resource"`
	Fingerprint     string `json:"fingerprint"`
	AccountRevision *int64 `json:"account_revision,omitempty"`
}

func (e MCPAccessPolicyExpectation) Validate() error {
	if e.AccountRevision != nil && *e.AccountRevision <= 0 {
		return fmt.Errorf("expected_mcp_access_policy.account_revision must be positive")
	}
	u, err := url.Parse(e.Resource)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path == "" {
		return fmt.Errorf("expected_mcp_access_policy.resource must be an HTTPS endpoint without credentials, query or fragment")
	}
	b, err := hex.DecodeString(e.Fingerprint)
	if err != nil || len(b) != sha256.Size || e.Fingerprint != strings.ToLower(e.Fingerprint) {
		return fmt.Errorf("expected_mcp_access_policy.fingerprint must be a lowercase SHA-256 digest")
	}
	return nil
}

// MCPAccessRuleOverlaps conservatively includes conditional rules and invalid
// path selectors. It must agree with the audit's coverage of MCP and metadata.
func MCPAccessRuleOverlaps(rule EdgeRuleResponse, resource *url.URL) bool {
	if rule.Kind != "jwt" || !EdgeRuleHostMatches(rule.MatchHost, resource.Host) {
		return false
	}
	for _, endpoint := range []struct{ path, method string }{{resource.Path, "POST"}, {resource.Path, "GET"}, {"/.well-known/oauth-protected-resource" + resource.Path, "GET"}} {
		if len(rule.MatchMethods) > 0 && !slices.Contains(rule.MatchMethods, endpoint.method) {
			continue
		}
		match, err := MatchProtectiveEdgeRulePath(rule.MatchPath, endpoint.path)
		if err != nil || match {
			return true
		}
	}
	return false
}

// MCPAccessPolicyFingerprint includes all potentially overlapping account JWT
// rules, even disabled/expired ones. Metadata unrelated to enforcement is omitted.
// Active is evaluated at observation time, so expiry also invalidates evidence.
func MCPAccessPolicyFingerprint(resource string, rules []EdgeRuleResponse, now time.Time) (string, error) {
	u, err := url.Parse(resource)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid MCP resource")
	}
	type policyRule struct {
		ID, AccountID, AppID, Host, Path, Mode string
		Methods                                []string
		Headers                                map[string]string
		Priority                               int
		Enabled, Active                        bool
		ExpiresAt                              *time.Time
		Match                                  *EdgeRuleMatchExpr
		Action                                 json.RawMessage
	}
	selected := []policyRule{}
	for _, rule := range rules {
		if !MCPAccessRuleOverlaps(rule, u) {
			continue
		}
		var action any
		decoder := json.NewDecoder(strings.NewReader(string(rule.Action)))
		decoder.UseNumber()
		if err := decoder.Decode(&action); err != nil || decoder.Decode(new(any)) != io.EOF {
			return "", fmt.Errorf("invalid JWT action")
		}
		canonical, err := json.Marshal(action)
		if err != nil {
			return "", fmt.Errorf("invalid JWT action")
		}
		methods := append([]string{}, rule.MatchMethods...)
		slices.Sort(methods)
		methods = slices.Compact(methods)
		headers := rule.MatchHeaders
		if headers == nil {
			headers = map[string]string{}
		}
		mode := rule.Mode
		if mode == "" {
			mode = "enforce"
		}
		var expires *time.Time
		if rule.ExpiresAt != nil {
			value := rule.ExpiresAt.UTC()
			expires = &value
		}
		selected = append(selected, policyRule{rule.ID, rule.AccountID, rule.AppID, strings.ToLower(rule.MatchHost), rule.MatchPath, mode, methods, headers, rule.Priority, rule.Enabled, rule.Enabled && mode != "log" && (rule.ExpiresAt == nil || rule.ExpiresAt.After(now)), expires, rule.Match, canonical})
	}
	slices.SortFunc(selected, func(a, b policyRule) int { return strings.Compare(a.ID, b.ID) })
	data, err := json.Marshal(struct {
		Version  int
		Resource string
		Rules    []policyRule
	}{1, resource, selected})
	if err != nil {
		return "", fmt.Errorf("encode MCP policy fingerprint")
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}
