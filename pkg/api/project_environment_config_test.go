package api

import "testing"

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
