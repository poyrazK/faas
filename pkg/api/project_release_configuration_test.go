package api

import (
	"strings"
	"testing"
)

func TestNormalizeProjectReleaseConfig(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"variables", `{"env":{"api":{"MODE":"new","LOG_LEVEL":"info"}}}`, true},
		{"empty replacement", `{"env":{"api":{}}}`, true},
		{"unknown fields", `{"env":{"api":{}},"ram_mb":512}`, false},
		{"secret key", `{"env":{"api":{"API_TOKEN":"do-not-print"}}}`, false},
		{"array", `[]`, false}, {"numeric value", `{"env":{"api":{"MODE":3}}}`, false},
		{"invalid key", `{"env":{"api":{"lower-case":"x"}}}`, false},
		{"unknown workload shape", `{"env":{"api":null}}`, false},
		{"trailing document", `{"env":{"api":{}}} {}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, hash, err := NormalizeProjectReleaseConfig([]byte(tc.raw))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t err=%v", tc.valid, err)
			}
			if tc.valid && (!ValidProjectEnvironmentConfigHash(hash) || len(raw) == 0) {
				t.Fatal("missing canonical request identity")
			}
			if err != nil && strings.Contains(err.Error(), "do-not-print") {
				t.Fatal("values leaked in validation")
			}
		})
	}
	first, hash, err := NormalizeProjectReleaseConfig([]byte(`{"env":{"api":{"MODE":"new","LOG_LEVEL":"info"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	second, other, err := NormalizeProjectReleaseConfig([]byte(`{ "env": {"api": {"LOG_LEVEL":"info","MODE":"new"}}}`))
	if err != nil || string(first) != string(second) || hash != other {
		t.Fatal("configuration identity changed with JSON ordering")
	}
}
