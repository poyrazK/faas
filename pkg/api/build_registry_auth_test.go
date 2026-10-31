package api

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestDockerConfigJSON(t *testing.T) {
	body, err := DockerConfigJSON([]RegistryLogin{
		{Registry: "registry-1.docker.io", Username: "hub", Password: "hub-pw"},
		{Registry: "ghcr.io", Username: "octo", Password: "gh:pw"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Auths map[string]struct {
			Auth string `json:"auth"`
		} `json:"auths"`
	}
	if err := json.Unmarshal(body, &config); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"https://index.docker.io/v1/": "hub:hub-pw",
		"ghcr.io":                     "octo:gh:pw",
	} {
		got, err := base64.StdEncoding.DecodeString(config.Auths[key].Auth)
		if err != nil || string(got) != want {
			t.Errorf("auths[%s] = %q (%v), want %q", key, got, err, want)
		}
	}
	if len(config.Auths) != 2 {
		t.Fatalf("auths = %v, want two entries", config.Auths)
	}
}
