package labels_test

import (
	"errors"
	"fmt"
	"maps"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/labels"
)

func errorCode(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		return ""
	}
	var le *labels.Error
	if !errors.As(err, &le) {
		t.Fatalf("error %v is not a *labels.Error", err)
	}
	return le.Code
}

func TestValidateKey(t *testing.T) {
	tests := []struct {
		key  string
		code string
	}{
		{"team", ""},
		{"Team_1.x-y", ""},
		{"a", ""},
		{"app.kubernetes.io/name", ""},
		{"gregale.dev/imported-from", ""},
		{strings.Repeat("a", api.LabelKeyNameMax), ""},
		{strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "/x", ""},
		{"", labels.CodeInvalidKey},
		{"/name", labels.CodeInvalidKey},
		{"example.com/", labels.CodeInvalidKey},
		{"-team", labels.CodeInvalidKey},
		{"team-", labels.CodeInvalidKey},
		{"te am", labels.CodeInvalidKey},
		{"tëam", labels.CodeInvalidKey},
		{"a/b/c", labels.CodeInvalidKey},
		{"Example.com/name", labels.CodeInvalidKey},
		{"example..com/name", labels.CodeInvalidKey},
		{"-example.com/name", labels.CodeInvalidKey},
		{"exa_mple.com/name", labels.CodeInvalidKey},
		{strings.Repeat("a", 64) + ".com/name", labels.CodeInvalidKey},
		{strings.Repeat("a", api.LabelKeyNameMax+1), labels.CodeInvalidKey},
		{strings.Repeat("a.", 127) + "a/name", labels.CodeInvalidKey},
	}
	for _, tc := range tests {
		t.Run(tc.key, func(t *testing.T) {
			if got := errorCode(t, labels.ValidateKey(tc.key)); got != tc.code {
				t.Errorf("ValidateKey(%q) code = %q, want %q", tc.key, got, tc.code)
			}
		})
	}
}

func TestValidateValue(t *testing.T) {
	tests := []struct {
		value string
		code  string
	}{
		{"", ""},
		{"billing", ""},
		{"v1.2_3-rc", ""},
		{strings.Repeat("v", api.LabelValueMax), ""},
		{strings.Repeat("v", api.LabelValueMax+1), labels.CodeInvalidValue},
		{"-billing", labels.CodeInvalidValue},
		{"billing.", labels.CodeInvalidValue},
		{"bill ing", labels.CodeInvalidValue},
		{"a/b", labels.CodeInvalidValue},
	}
	for _, tc := range tests {
		t.Run(tc.value, func(t *testing.T) {
			if got := errorCode(t, labels.ValidateValue("k", tc.value)); got != tc.code {
				t.Errorf("ValidateValue(%q) code = %q, want %q", tc.value, got, tc.code)
			}
		})
	}
}

func TestReserved(t *testing.T) {
	tests := map[string]bool{
		"gregale.dev/imported-from":       true,
		"build.gregale.dev/source":        true,
		"gregale.dev":                     false,
		"notgregale.dev/x":                false,
		"gregale.dev.example.com/x":       false,
		"team":                            false,
		"app.kubernetes.io/name":          false,
		"gregale.dev-imported/from":       false,
		"a.b.gregale.dev/deeply-reserved": true,
	}
	for key, want := range tests {
		if got := labels.Reserved(key); got != want {
			t.Errorf("Reserved(%q) = %v, want %v", key, got, want)
		}
	}
}

func manyLabels(n int) map[string]string {
	out := make(map[string]string, n)
	for i := range n {
		out[fmt.Sprintf("k%d", i)] = "v"
	}
	return out
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		code   string
	}{
		{"nil", nil, ""},
		{"reserved keys pass", map[string]string{"gregale.dev/imported-from": "kubernetes", "team": "billing"}, ""},
		{"at the limit", manyLabels(api.LabelsPerResourceMax), ""},
		{"over the limit", manyLabels(api.LabelsPerResourceMax + 1), labels.CodeLimitExceeded},
		{"bad key", map[string]string{"bad key": "v"}, labels.CodeInvalidKey},
		{"bad value", map[string]string{"team": "bad value"}, labels.CodeInvalidValue},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := errorCode(t, labels.Validate(tc.labels)); got != tc.code {
				t.Errorf("code = %q, want %q", got, tc.code)
			}
		})
	}
}

func TestLimitErrorCarriesLimitAndObserved(t *testing.T) {
	err := labels.Validate(manyLabels(api.LabelsPerResourceMax + 3))
	var le *labels.Error
	if !errors.As(err, &le) {
		t.Fatalf("err = %v", err)
	}
	if le.Limit != api.LabelsPerResourceMax || le.Observed != api.LabelsPerResourceMax+3 {
		t.Errorf("limit/observed = %d/%d", le.Limit, le.Observed)
	}
}

func TestApply(t *testing.T) {
	current := map[string]string{"team": "billing", "tier": "web", "gregale.dev/imported-from": "kubernetes"}
	tests := []struct {
		name   string
		change labels.Change
		want   map[string]string
		code   string
	}{
		{
			name:   "set and remove together",
			change: labels.Change{Set: map[string]string{"team": "payments", "env": "prod"}, Remove: []string{"tier"}},
			want:   map[string]string{"team": "payments", "env": "prod", "gregale.dev/imported-from": "kubernetes"},
		},
		{
			name:   "empty change keeps everything",
			change: labels.Change{},
			want:   current,
		},
		{
			name:   "removing an absent key changes nothing",
			change: labels.Change{Remove: []string{"missing"}},
			want:   current,
		},
		{
			name:   "setting the current value changes nothing",
			change: labels.Change{Set: map[string]string{"team": "billing"}},
			want:   current,
		},
		{
			name:   "empty value is a value",
			change: labels.Change{Set: map[string]string{"canary": ""}},
			want:   map[string]string{"team": "billing", "tier": "web", "canary": "", "gregale.dev/imported-from": "kubernetes"},
		},
		{name: "reserved set", change: labels.Change{Set: map[string]string{"gregale.dev/x": "y"}}, code: labels.CodeReservedKey},
		{name: "reserved remove", change: labels.Change{Remove: []string{"gregale.dev/imported-from"}}, code: labels.CodeReservedKey},
		{name: "set and removed", change: labels.Change{Set: map[string]string{"tier": "api"}, Remove: []string{"tier"}}, code: labels.CodeInvalidChange},
		{name: "bad remove key", change: labels.Change{Remove: []string{"bad key"}}, code: labels.CodeInvalidKey},
		{name: "bad value", change: labels.Change{Set: map[string]string{"team": "a b"}}, code: labels.CodeInvalidValue},
		{name: "too many removes", change: labels.Change{Remove: make([]string, api.LabelsPerResourceMax+1)}, code: labels.CodeLimitExceeded},
		{name: "result over the limit", change: labels.Change{Set: manyLabels(api.LabelsPerResourceMax - 2)}, code: labels.CodeLimitExceeded},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := maps.Clone(current)
			got, err := labels.Apply(current, tc.change)
			if code := errorCode(t, err); code != tc.code {
				t.Fatalf("code = %q, want %q (err %v)", code, tc.code, err)
			}
			if !maps.Equal(current, before) {
				t.Errorf("Apply modified current: %v", current)
			}
			if tc.code == "" && !maps.Equal(got, tc.want) {
				t.Errorf("Apply = %v, want %v", got, tc.want)
			}
		})
	}
}
