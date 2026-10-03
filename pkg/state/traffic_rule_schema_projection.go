// adr: 375
package state

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// Measure simple raw JSON strings without repeatedly marshaling their bytes.
// The regular JSON encoder remains authoritative for escaped/structured input.
// Replacing only the copied projection's schema with null lets both enclosing
// projections account for the same exact HTML and Unicode escape allowance.
func compactMemTrafficRuleSchema(rule EdgeRule) (EdgeRule, int64) {
	if rule.Action.Validate == nil {
		return rule, 0
	}
	raw := rule.Action.Validate.Schema
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return rule, 0
	}
	value := string(raw[1 : len(raw)-1])
	if !utf8.ValidString(value) || strings.ContainsAny(value, "\"\\") {
		return rule, 0
	}
	for i := range len(value) {
		if value[i] < 0x20 {
			return rule, 0
		}
	}
	size := int64(len(raw))
	for _, escaped := range []string{"&", "<", ">"} {
		size += 5 * int64(strings.Count(value, escaped))
	}
	for _, escaped := range []string{"\u2028", "\u2029"} {
		size += 3 * int64(strings.Count(value, escaped))
	}
	validate := *rule.Action.Validate
	validate.Schema = json.RawMessage("null")
	rule.Action.Validate = &validate
	return rule, size - 4
}
