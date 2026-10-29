package capgate

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/platformflags"
)

// Settings are the request fields and declarations that turn a capability on
// without a route of their own: an app field, an edge rule kind, a function
// runtime, a gregale.yaml block (ADR-20261023115741). Each belongs to one
// capability in the catalog; a setting whose capability is off for the
// caller is refused.

// SettingCapability returns the capability that owns a setting. A
// value-qualified setting ("app.app_protocol=grpc") is looked up exactly,
// then by its field ("app.app_protocol"); a setting no capability owns is
// ungated.
func (g *Gate) SettingCapability(setting string) (string, bool) {
	if capability, ok := g.settings[setting]; ok {
		return capability, true
	}
	if field, _, qualified := strings.Cut(setting, "="); qualified {
		capability, ok := g.settings[field]
		return capability, ok
	}
	return "", false
}

// RefusedSetting returns the first setting whose capability is off for the
// subject, with that capability. ok is false when every setting is allowed.
func (g *Gate) RefusedSetting(settings []string, subject platformflags.Subject) (setting, capability string, ok bool) {
	for _, setting := range settings {
		capability, owned := g.SettingCapability(setting)
		if owned && !g.enabled(capability, subject) {
			g.hidden.WithLabelValues(capability, "setting").Inc()
			return setting, capability, true
		}
	}
	return "", "", false
}

// Name is a capability's catalog name, or its id when unknown.
func (g *Gate) Name(capability string) string {
	if name := g.names[capability]; name != "" {
		return name
	}
	return capability
}

// EdgeRuleSetting is the setting an edge rule of kind turns on.
func EdgeRuleSetting(kind string) string { return "edge_rule." + strings.TrimSpace(kind) }

// RuntimeSetting is the setting a function runtime turns on.
func RuntimeSetting(runtime string) string { return "runtime." + strings.TrimSpace(runtime) }

// WildcardDomainSetting is the setting a wildcard custom domain turns on.
const WildcardDomainSetting = "domain.wildcard"

// AppSettings lists the settings an app create or update request turns on:
// every field it sets to a non-zero value that differs from current. Strings
// are value-qualified ("app.visibility=internal"). current is the app as the
// API reports it, or nil for a create; an unchanged value is not a new
// setting, so resubmitting a form never trips a capability that was already
// in use.
func AppSettings(request, current any) []string {
	req, cur := jsonFields(request), jsonFields(current)
	var out []string
	for key, raw := range req {
		if !enabling(raw) {
			continue
		}
		if was, ok := cur[key]; ok && sameJSON(was, raw) {
			continue
		}
		out = append(out, qualified("app."+key, raw))
	}
	sort.Strings(out)
	return out
}

// ManifestSettings lists the settings a gregale.yaml declares: one per
// non-empty top-level block, one per trigger kind, the function runtimes,
// scaling targets and schedules, and the lifecycle block's app fields.
func ManifestSettings(m *gregalemanifest.Manifest) []string {
	if m == nil {
		return nil
	}
	set := map[string]bool{}
	v := reflect.ValueOf(*m)
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		key := yamlKey(t.Field(i))
		if key == "" || v.Field(i).IsZero() || (v.Field(i).Kind() == reflect.Slice && v.Field(i).Len() == 0) {
			continue
		}
		switch key {
		case "triggers", "lifecycle":
		default:
			set["manifest."+key] = true
		}
	}
	for _, trigger := range m.Triggers {
		set["manifest.triggers."+string(trigger.Kind)] = true
	}
	if m.Lifecycle != nil {
		for _, setting := range AppSettings(m.Lifecycle, nil) {
			set[setting] = true
		}
	}
	if m.Scaling != nil && (m.Scaling.Target != nil || len(m.Scaling.Targets) > 0 || len(m.Scaling.Schedules) > 0) {
		set["manifest.scaling.targets"] = true
	}
	if m.Function != nil && m.Function.Runtime != "" {
		set[RuntimeSetting(m.Function.Runtime)] = true
	}
	for _, function := range m.Functions {
		if function.Runtime != "" {
			set[RuntimeSetting(function.Runtime)] = true
		}
	}
	out := make([]string, 0, len(set))
	for setting := range set {
		out = append(out, setting)
	}
	sort.Strings(out)
	return out
}

// jsonFields marshals v and returns its top-level JSON object fields; for a
// struct with yaml tags only, it uses the yaml keys.
func jsonFields(v any) map[string]json.RawMessage {
	if v == nil {
		return nil
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	if rv.Kind() == reflect.Struct && !hasJSONTags(rv.Type()) {
		out := map[string]json.RawMessage{}
		for i := 0; i < rv.NumField(); i++ {
			if key := yamlKey(rv.Type().Field(i)); key != "" {
				if raw, err := json.Marshal(rv.Field(i).Interface()); err == nil {
					out[key] = raw
				}
			}
		}
		return out
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out map[string]json.RawMessage
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return out
}

func hasJSONTags(t reflect.Type) bool {
	for i := 0; i < t.NumField(); i++ {
		if _, ok := t.Field(i).Tag.Lookup("json"); ok {
			return true
		}
	}
	return false
}

func yamlKey(f reflect.StructField) string {
	tag := f.Tag.Get("yaml")
	name, _, _ := strings.Cut(tag, ",")
	if name == "-" {
		return ""
	}
	return name
}

// enabling reports a JSON value that turns something on: anything but null,
// false, zero, an empty string, an empty list or an empty object.
func enabling(raw json.RawMessage) bool {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		return x != ""
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}

func sameJSON(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return bytes.Equal(a, b)
	}
	return reflect.DeepEqual(x, y)
}

func qualified(setting string, raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil && s != "" {
		return setting + "=" + strings.ToLower(s)
	}
	return setting
}
