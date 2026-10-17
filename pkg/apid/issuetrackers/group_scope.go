package issuetrackers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// GroupScopeFingerprint ignores traffic counts and lifecycle but tracks original
// member identities and split-rule edits that change the source's meaning.
func GroupScopeFingerprint(detail api.IssueDetail) string {
	members := []string{detail.Issue.ID}
	for _, m := range detail.GroupMembers {
		if m.ID != detail.Issue.ID {
			members = append(members, m.ID)
		}
	}
	sort.Strings(members)
	type ruleScope struct {
		ID        string
		Enabled   bool
		UpdatedAt time.Time
	}
	rules := make([]ruleScope, 0, len(detail.SplitRules))
	for _, r := range detail.SplitRules {
		rules = append(rules, ruleScope{r.ID, r.Enabled, r.UpdatedAt.UTC()})
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })
	data, _ := json.Marshal(struct {
		Members         []string
		Rules           []ruleScope
		GroupingVersion int
		Fingerprint     string
	}{members, rules, detail.Issue.GroupingVersion, detail.Issue.Fingerprint})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
