package platformflags

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// optInsKeyPrefix namespaces the self-serve opt-in rows (ADR 20261021080640).
// It does not share configKeyPrefix ("platform_flag."), so an opt-in row is
// never mistaken for a flag definition.
const optInsKeyPrefix = "platform_flag_opt_ins."

// OptIns are the accounts and organizations that joined one self-serve
// preview. They are stored as their own hot runtime-configuration row per
// flag, so a customer joining never rewrites the operator's definition.
type OptIns struct {
	Accounts []string `json:"accounts,omitempty"`
	Orgs     []string `json:"orgs,omitempty"`
}

// OptInsConfigKey is the runtime-configuration key that stores a flag's
// opt-ins.
func OptInsConfigKey(flagKey string) string { return optInsKeyPrefix + flagKey }

// FlagKeyFromOptInsConfigKey reverses OptInsConfigKey.
func FlagKeyFromOptInsConfigKey(configKey string) (string, bool) {
	key, ok := strings.CutPrefix(configKey, optInsKeyPrefix)
	return key, ok && key != ""
}

// Count is the number of opted-in subjects.
func (o OptIns) Count() int { return len(o.Accounts) + len(o.Orgs) }

// HasAccount reports whether accountID opted in.
func (o OptIns) HasAccount(accountID string) bool {
	return slices.Contains(o.Accounts, canonicalAccount(accountID))
}

// HasOrg reports whether orgID opted in.
func (o OptIns) HasOrg(orgID string) bool {
	return orgID != "" && slices.Contains(o.Orgs, canonicalAccount(orgID))
}

// With returns the opt-ins with one subject added; adding a present subject
// changes nothing.
func (o OptIns) With(id string, org bool) OptIns {
	out := o.clone()
	list := &out.Accounts
	if org {
		list = &out.Orgs
	}
	if id = canonicalAccount(id); !slices.Contains(*list, id) {
		*list = append(*list, id)
	}
	return out
}

// Without returns the opt-ins with one subject removed.
func (o OptIns) Without(id string, org bool) OptIns {
	out := o.clone()
	list := &out.Accounts
	if org {
		list = &out.Orgs
	}
	id = canonicalAccount(id)
	*list = slices.DeleteFunc(*list, func(v string) bool { return v == id })
	if len(*list) == 0 {
		*list = nil
	}
	return out
}

func (o OptIns) clone() OptIns {
	return OptIns{Accounts: slices.Clone(o.Accounts), Orgs: slices.Clone(o.Orgs)}
}

// ParseOptIns decodes and validates a stored or submitted opt-in row.
func ParseOptIns(raw json.RawMessage) (OptIns, error) {
	if len(raw) > api.PlatformFlagsMaxOptInsBytes {
		return OptIns{}, fmt.Errorf("opt-ins exceed %d bytes", api.PlatformFlagsMaxOptInsBytes)
	}
	var optIns OptIns
	if err := decodeStrict(raw, &optIns); err != nil {
		return OptIns{}, fmt.Errorf("opt-ins must be an object with accounts and orgs: %w", err)
	}
	optIns = OptIns{Accounts: normalizeAccounts(optIns.Accounts), Orgs: normalizeAccounts(optIns.Orgs)}
	if optIns.Count() > api.PlatformFlagsMaxOptInSubjects {
		return OptIns{}, fmt.Errorf("at most %d subjects may opt in to one preview", api.PlatformFlagsMaxOptInSubjects)
	}
	if err := validateIDs(optIns.Accounts, api.PlatformFlagsMaxOptInSubjects, "account"); err != nil {
		return OptIns{}, err
	}
	if err := validateIDs(optIns.Orgs, api.PlatformFlagsMaxOptInSubjects, "organization"); err != nil {
		return OptIns{}, err
	}
	return optIns, nil
}

// Marshal encodes opt-ins in their stored form.
func (o OptIns) Marshal() json.RawMessage {
	raw, err := json.Marshal(o)
	if err != nil {
		panic(fmt.Sprintf("platformflags: marshal opt-ins: %v", err))
	}
	return raw
}

// ValidateOptIns refuses organization opt-ins for a flag that is decided
// where the organization is unknown: they could never match.
func ValidateOptIns(spec Spec, optIns OptIns) error {
	if len(optIns.Orgs) > 0 && !spec.Supports(AttributeOrg) {
		return fmt.Errorf("%s is decided where the organization is unknown (%s), so organizations cannot opt in", spec.Key, strings.Join(spec.Consumers, ", "))
	}
	return nil
}
