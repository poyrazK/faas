package capgate

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/productcap"
)

func settingsCatalog() productcap.Catalog {
	return productcap.Catalog{Version: 1, Capabilities: []productcap.Capability{
		{ID: "streaming", Name: "Streaming", Maturity: productcap.MaturityGA, Settings: []string{"app.streaming_enabled", "app.app_protocol=http2"}},
		{ID: "grpc", Name: "gRPC", Maturity: productcap.MaturityPreview, Parent: "streaming", Settings: []string{"app.app_protocol=grpc"}},
		{ID: "tracing", Name: "Tracing", Maturity: productcap.MaturityPreview, Settings: []string{"app.tracing", "manifest.tracing"}},
	}}
}

func TestSettingCapabilityPrefersTheValue(t *testing.T) {
	g := New(settingsCatalog(), nil)
	for setting, want := range map[string]string{
		"app.app_protocol=grpc":  "grpc",
		"app.app_protocol=http2": "streaming",
		"app.app_protocol=http1": "",
		"app.streaming_enabled":  "streaming",
		"app.tracing=on":         "tracing",
		"app.unknown":            "",
	} {
		if got, _ := g.SettingCapability(setting); got != want {
			t.Errorf("SettingCapability(%q) = %q, want %q", setting, got, want)
		}
	}
}

func TestRefusedSettingFollowsTierAndParent(t *testing.T) {
	g := New(settingsCatalog(), nil)
	subject := platformflags.Subject{AccountID: account}
	if _, _, refused := g.RefusedSetting([]string{"app.streaming_enabled", "app.app_protocol=http1"}, subject); refused {
		t.Fatal("GA and unowned settings must pass")
	}
	setting, capability, refused := g.RefusedSetting([]string{"app.streaming_enabled", "app.app_protocol=grpc"}, subject)
	if !refused || setting != "app.app_protocol=grpc" || capability != "grpc" || g.Name(capability) != "gRPC" {
		t.Fatalf("RefusedSetting = %q %q %v, want the preview gRPC setting refused", setting, capability, refused)
	}
}

func TestAppSettingsListsOnlyNewNonZeroValues(t *testing.T) {
	off := false
	zero := 0
	protocol := "grpc"
	req := api.UpdateAppRequest{Tracing: nil, StreamingEnabled: &off, MinInstances: &zero, AppProtocol: &protocol}
	got := AppSettings(req, nil)
	if !slices.Equal(got, []string{"app.app_protocol=grpc"}) {
		t.Fatalf("AppSettings = %v, want only the non-zero gRPC protocol", got)
	}
	current := map[string]any{"app_protocol": "grpc"}
	if got := AppSettings(req, current); len(got) != 0 {
		t.Fatalf("AppSettings with an unchanged value = %v, want none", got)
	}
}

func TestManifestSettings(t *testing.T) {
	one := 1
	m := &gregalemanifest.Manifest{
		Triggers:  []gregalemanifest.Trigger{{Kind: gregalemanifest.TriggerKindCron}, {Kind: gregalemanifest.TriggerKindKafka}},
		Functions: []gregalemanifest.FunctionSpec{{Name: "a", Runtime: "go124"}},
		Scaling:   &gregalemanifest.ScalingConfig{MinInstances: &one, Schedules: []gregalemanifest.ScalingSchedule{{}}},
		Lifecycle: &gregalemanifest.LifecycleConfig{},
	}
	got := ManifestSettings(m)
	for _, want := range []string{"manifest.triggers.cron", "manifest.triggers.kafka", "manifest.functions", "runtime.go124", "manifest.scaling", "manifest.scaling.targets"} {
		if !slices.Contains(got, want) {
			t.Errorf("ManifestSettings = %v, missing %q", got, want)
		}
	}
	if slices.Contains(got, "manifest.triggers") || slices.Contains(got, "manifest.lifecycle") {
		t.Errorf("ManifestSettings = %v: triggers and lifecycle are listed by kind and field, not as blocks", got)
	}
}

// ungatedAppFields are app request fields that belong to the core app (or are
// gated elsewhere, as runtime is by RuntimeSetting) rather than to a capability.
var ungatedAppFields = map[string]string{
	"slug": "core app identity", "type": "core app class", "runtime": "gated by runtime.<runtime>",
	"ram_mb": "every app sets memory", "idle_timeout_s": "scale-to-zero is always on", "request_timeout_s": "core request handling",
	"pause": "core app control", "ports": "core HTTP ports", "favicon": "static edge answer that avoids wakes",
	"robots_txt": "static edge answer that avoids wakes", "head_wakes": "wake avoidance", "crawler_policy": "only crawler_policy=block is gated",
	"deploy_settings_condition": "request precondition", "expected_service_binding_fingerprint": "request precondition",
	"overflow_node": "operator placement preference", "visibility": "only visibility=internal is gated",
	"execution_mode": "only worker and job modes are gated", "release_policy": "only release_policy=safe is gated",
	"app_protocol": "http1 is the default; http2 and grpc are gated", "eviction_priority": "only reserved is gated",
}

// ungatedManifestKeys are gregale.yaml keys that are not capability blocks.
var ungatedManifestKeys = map[string]string{
	"schema_version": "file format", "hosting": "internal frontend hosting, operator-gated",
	"triggers": "gated per trigger kind", "lifecycle": "gated per app field",
}

// TestEverySettingHasACapability fails when an app field, edge rule kind,
// function runtime or gregale.yaml block has no capability to release it and
// no reason to stay ungated (ADR-20261023115741).
func TestEverySettingHasACapability(t *testing.T) {
	catalog, err := productcap.Load()
	if err != nil {
		t.Fatal(err)
	}
	g := New(catalog, nil)
	owned := func(setting string) bool {
		if _, ok := g.SettingCapability(setting); ok {
			return true
		}
		for s := range g.settings {
			if strings.HasPrefix(s, setting+"=") {
				return true
			}
		}
		return false
	}
	var missing []string
	for _, v := range []any{api.UpdateAppRequest{}, api.CreateAppRequest{}} {
		rt := reflect.TypeOf(v)
		for i := 0; i < rt.NumField(); i++ {
			name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
			if name == "" || name == "-" {
				continue
			}
			if _, ok := ungatedAppFields[name]; !ok && !owned("app."+name) {
				missing = append(missing, "app."+name)
			}
		}
	}
	types, err := os.ReadFile(filepath.Join("..", "..", "state", "types.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range regexp.MustCompile(`EdgeRuleKind[A-Za-z]+\s+EdgeRuleKind = "([a-z_]+)"`).FindAllStringSubmatch(string(types), -1) {
		if !owned(EdgeRuleSetting(m[1])) {
			missing = append(missing, EdgeRuleSetting(m[1]))
		}
	}
	for _, runtime := range api.FunctionRuntimes {
		if !owned(RuntimeSetting(runtime)) {
			missing = append(missing, RuntimeSetting(runtime))
		}
	}
	mt := reflect.TypeOf(gregalemanifest.Manifest{})
	for i := 0; i < mt.NumField(); i++ {
		key := yamlKey(mt.Field(i))
		if key == "" {
			continue
		}
		if _, ok := ungatedManifestKeys[key]; !ok && !owned("manifest."+key) {
			missing = append(missing, "manifest."+key)
		}
	}
	for _, kind := range []gregalemanifest.TriggerKind{gregalemanifest.TriggerKindCron, gregalemanifest.TriggerKindKafka, gregalemanifest.TriggerKindNATS, gregalemanifest.TriggerKindRedisStreams, gregalemanifest.TriggerKindSQSCompat} {
		if !owned("manifest.triggers." + string(kind)) {
			missing = append(missing, "manifest.triggers."+string(kind))
		}
	}
	slices.Sort(missing)
	missing = slices.Compact(missing)
	if len(missing) > 0 {
		t.Fatalf("%d settings have no capability; add each to a capability's settings in pkg/productcap/catalog.json or to the ungated lists here with a reason:\n%s", len(missing), strings.Join(missing, "\n"))
	}
}
