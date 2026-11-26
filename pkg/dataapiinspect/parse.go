package dataapiinspect

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/dataapiinspectioncommand"
)

// ParseObservation accepts only the complete, bounded platform-probe format.
// It validates content, not task provenance, trust, freshness, or identity.
func ParseObservation(data []byte, table string) (Observation, error) {
	var out Observation
	bad := func() (Observation, error) { return Observation{}, ErrUnavailable }
	if len(data) == 0 || len(data) > 65536 || !utf8.Valid(data) || !tableName.MatchString(table) {
		return bad()
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if uniqueJSON(dec, 0) != nil {
		return bad()
	}
	if _, err := dec.Token(); err != io.EOF {
		return bad()
	}
	root, ok := object(data, "version", "role", "relation", "columns", "policies", "triggers", "target_digest", "trust_bundle_sha256")
	if !ok {
		return bad()
	}
	role, ok := object(root["role"], "name", "database", "superuser", "bypass_rls")
	if !ok || !bools(role, "superuser", "bypass_rls") {
		return bad()
	}
	rel, ok := object(root["relation"], "name", "kind", "rls", "force_rls", "owner_member", "row_security_active", "schema_usage", "can_select", "can_insert", "can_update", "can_delete")
	if !ok || !bools(rel, "rls", "force_rls", "owner_member", "row_security_active", "schema_usage", "can_select", "can_insert", "can_update", "can_delete") {
		return bad()
	}
	for _, spec := range []struct {
		field       string
		keys, flags []string
		min         int
	}{
		{"columns", []string{"name", "generated", "identity", "can_select", "can_insert", "can_update"}, []string{"generated", "identity", "can_select", "can_insert", "can_update"}, 1},
		{"policies", []string{"name", "command", "permissive", "applies_to_role", "has_using", "has_check"}, []string{"permissive", "applies_to_role", "has_using", "has_check"}, 0},
		{"triggers", []string{"name", "enabled", "type"}, nil, 0},
	} {
		var rows []json.RawMessage
		if json.Unmarshal(root[spec.field], &rows) != nil || rows == nil || len(rows) < spec.min || len(rows) > 128 {
			return bad()
		}
		for _, raw := range rows {
			row, ok := object(raw, spec.keys...)
			if !ok || !bools(row, spec.flags...) {
				return bad()
			}
		}
	}
	if json.Unmarshal(data, &out) != nil || out.Version != 1 || out.Relation.Name != table || !slices.Contains([]string{"r", "p"}, out.Relation.Kind) || !validCatalogName(out.Role.Name) || !validCatalogName(out.Role.Database) || !dataapiinspectioncommand.ValidTrustBundleFingerprint(out.TargetDigest) || !dataapiinspectioncommand.ValidTrustBundleFingerprint(out.TrustBundleSHA256) {
		return bad()
	}
	seen := make(map[string]bool)
	for _, c := range out.Columns {
		if !validCatalogName(c.Name) || seen[c.Name] {
			return bad()
		}
		seen[c.Name] = true
	}
	clear(seen)
	for _, p := range out.Policies {
		if !validCatalogName(p.Name) || seen[p.Name] || !slices.Contains([]string{"r", "a", "w", "d", "*"}, p.Command) {
			return bad()
		}
		seen[p.Name] = true
	}
	clear(seen)
	for _, trigger := range out.Triggers {
		if !validCatalogName(trigger.Name) || seen[trigger.Name] || !slices.Contains([]string{"O", "D", "R", "A"}, trigger.Enabled) || trigger.Type < 1 || trigger.Type > 255 {
			return bad()
		}
		seen[trigger.Name] = true
	}
	return out, nil
}

func validCatalogName(s string) bool {
	return s != "" && len(s) <= 63 && utf8.ValidString(s) && !strings.ContainsRune(s, '\x00')
}

func object(data []byte, keys ...string) (map[string]json.RawMessage, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || len(fields) != len(keys) {
		return nil, false
	}
	for _, key := range keys {
		if _, found := fields[key]; !found {
			return nil, false
		}
	}
	return fields, true
}
func bools(fields map[string]json.RawMessage, keys ...string) bool {
	for _, key := range keys {
		v := bytes.TrimSpace(fields[key])
		if !bytes.Equal(v, []byte("true")) && !bytes.Equal(v, []byte("false")) {
			return false
		}
	}
	return true
}

// uniqueJSON rejects duplicate keys at every level and excessive nesting.
func uniqueJSON(dec *json.Decoder, depth int) error {
	if depth > 16 {
		return ErrUnavailable
	}
	token, err := dec.Token()
	if err != nil {
		return err
	}
	delim, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return ErrUnavailable
			}
			seen[name] = true
			if err := uniqueJSON(dec, depth+1); err != nil {
				return err
			}
		}
		end, err := dec.Token()
		if err != nil || end != json.Delim('}') {
			return ErrUnavailable
		}
	case '[':
		for dec.More() {
			if err := uniqueJSON(dec, depth+1); err != nil {
				return err
			}
		}
		end, err := dec.Token()
		if err != nil || end != json.Delim(']') {
			return ErrUnavailable
		}
	default:
		return ErrUnavailable
	}
	return nil
}
