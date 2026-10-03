package routerequirements

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

func jsonDigest(value any) string {
	body, _ := json.Marshal(value)
	return fmt.Sprintf("%x", sha256.Sum256(body))
}

func configurationDigest(context Context, planName string) string {
	type ruleState struct {
		ID, AppID, AccountID, Host, Path, Kind, ValidateMode string
		Methods                                              []string
		Priority                                             int
		Enabled                                              bool
		HeadersSHA256, ActionSHA256                          string
	}
	rules := make([]ruleState, 0, len(context.Rules))
	for _, rule := range context.Rules {
		methods := append([]string(nil), rule.MatchMethods...)
		sort.Strings(methods)
		rules = append(rules, ruleState{ID: rule.ID, AppID: rule.AppID, AccountID: rule.AccountID,
			Host: rule.MatchHost, Path: rule.MatchPath, Kind: rule.Kind, ValidateMode: rule.ValidateMode,
			Methods: methods, Priority: rule.Priority, Enabled: rule.Enabled,
			HeadersSHA256: jsonDigest(rule.MatchHeaders), ActionSHA256: actionDigest(rule.Action)})
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })
	return jsonDigest(struct {
		AppID, App, Host, Plan, Unavailable, ConsumerAuthMode string
		RequestTimeoutS                                       int
		MaintenanceMode                                       bool
		Limits                                                api.AppEffectiveLimits
		Rules                                                 []ruleState
	}{context.App.ID, context.App.Slug, context.Host, planName, context.Unavailable, context.App.ConsumerAuthMode,
		context.App.RequestTimeoutS, context.App.MaintenanceMode, context.App.EffectiveLimits, rules})
}

func actionDigest(body []byte) string {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if decoder.Decode(&value) == nil {
		return jsonDigest(value)
	}
	return fmt.Sprintf("%x", sha256.Sum256(body))
}
