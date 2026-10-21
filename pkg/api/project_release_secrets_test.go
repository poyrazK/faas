package api

import (
	"strings"
	"testing"
)

func TestProjectReleaseSecretValidation(t *testing.T) {
	id := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	raw, _, err := NormalizeProjectReleaseConfig([]byte(`{"secrets":{"api":{"TOKEN":"` + id + `"}}}`))
	if err != nil || !strings.Contains(string(raw), id) {
		t.Fatal("secret-only references rejected")
	}
	for _, input := range []string{`{"secrets":{"api":{"TOKEN":"plaintext"}}}`, `{"secrets":{"api":null}}`, `{"secrets":{"api":{"bad name":"` + id + `"}}}`, `{"secrets":{"api":{}},"secret_values":{"TOKEN":"private"}}`} {
		if _, _, err := NormalizeProjectReleaseConfig([]byte(input)); err == nil {
			t.Fatal("invalid secret reference accepted")
		}
	}
	for _, input := range []string{`null`, `{"api":{"TOKEN":""}}`, `{"api":{"bad key":"private-value"}}`, `{"api":{"TOKEN":42}}`} {
		_, err := DecodeProjectReleaseSecrets(strings.NewReader(input), MustLimitsFor(PlanPro))
		if err == nil || strings.Contains(err.Error(), "private-value") {
			t.Fatal("invalid secret values accepted or leaked")
		}
	}
}
