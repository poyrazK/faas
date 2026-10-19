package api

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestNormalizeProjectEnvironmentConfigCanonicalizesAndHashes(t *testing.T) {
	first, firstHash, err := NormalizeProjectEnvironmentConfig([]byte(` { "replicas": 2, "region": "eu" } `))
	if err != nil {
		t.Fatal(err)
	}
	second, secondHash, err := NormalizeProjectEnvironmentConfig([]byte(`{"region":"eu","replicas":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) || firstHash != secondHash {
		t.Fatalf("canonical config differs: %s/%s %s/%s", first, firstHash, second, secondHash)
	}
	if string(first) != `{"region":"eu","replicas":2}` {
		t.Fatalf("canonical config = %s", first)
	}
}

func TestNormalizeProjectEnvironmentConfigRejectsSecretsAndNonObjects(t *testing.T) {
	for _, raw := range []string{`{"api_token":"hidden"}`, `[]`, `"value"`} {
		if _, _, err := NormalizeProjectEnvironmentConfig([]byte(raw)); err == nil {
			t.Fatalf("NormalizeProjectEnvironmentConfig(%s) accepted invalid input", raw)
		}
	}
}

func TestValidProjectEnvironmentConfigHash(t *testing.T) {
	_, valid, err := NormalizeProjectEnvironmentConfig([]byte(`{"region":"eu"}`))
	if err != nil || !ValidProjectEnvironmentConfigHash(valid) {
		t.Fatalf("valid config hash %q rejected: %v", valid, err)
	}
	for _, invalid := range []string{"", "not-a-hash", "A" + valid[1:], valid[:63]} {
		if ValidProjectEnvironmentConfigHash(invalid) {
			t.Errorf("invalid config hash %q accepted", invalid)
		}
	}
}

// ADR-1136: only env-shaped top-level keys with scalar values are delivered.
func TestProjectEnvironmentConfigEnv(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values string
		want   map[string]string
	}{
		{name: "empty document", values: ``, want: map[string]string{}},
		{name: "empty object", values: `{}`, want: map[string]string{}},
		{name: "scalars", values: `{"LOG_LEVEL":"info","WORKERS":4,"RATIO":0.5,"BIG":1e3,"DEBUG":false,"ON":true}`,
			want: map[string]string{"LOG_LEVEL": "info", "WORKERS": "4", "RATIO": "0.5", "BIG": "1e3", "DEBUG": "false", "ON": "true"}},
		{name: "string keeps its text", values: `{"ORDERS_URL":"http://orders.svc.gregale:8080","EMPTY":"","QUOTED":"say \"hi\""}`,
			want: map[string]string{"ORDERS_URL": "http://orders.svc.gregale:8080", "EMPTY": "", "QUOTED": `say "hi"`}},
		{name: "document-only keys", values: `{"region":"eu","feature.flag":true,"Mixed_Case":"x","_LEADING":"x","9LIVES":"x","DASH-KEY":"x","OK":"y"}`,
			want: map[string]string{"OK": "y"}},
		{name: "document-only values", values: `{"LIMITS":{"cpu":2},"HOSTS":["a"],"UNSET":null,"NAME":"svc"}`,
			want: map[string]string{"NAME": "svc"}},
	} {
		got, err := ProjectEnvironmentConfigEnv(json.RawMessage(tc.values))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
	if _, err := ProjectEnvironmentConfigEnv(json.RawMessage(`[1,2]`)); err == nil {
		t.Fatal("a non-object document was accepted")
	}
}
