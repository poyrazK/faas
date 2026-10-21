package main_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"net/http"
	"strings"
	"testing"
)

func TestSecretReferenceCLIExplicitEnvironmentTransport(t *testing.T) {
	for _, environment := range []string{"production", "a", "1", "qa", "12"} {
		for _, tc := range []struct {
			op, method, suffix, argument, response string
			status                                 int
		}{
			{"list", http.MethodGet, "", "", `{"environment_id":"env","environment":"production","references":{"URL":"secret:DATABASE"},"count":1,"quota":20}`, 200},
			{"set", http.MethodPut, "/URL", "URL=secret:DATABASE", `{"environment_id":"env","environment":"production","key":"URL","reference":"secret:DATABASE"}`, 200},
			{"unset", http.MethodDelete, "/URL", "URL", "", 204},
		} {
			t.Run(environment+"/"+tc.op, func(t *testing.T) {
				main.ResetJSONOut(t)
				f := main.AuthedFakeAPI(t, strings.ReplaceAll(tc.response, `"environment":"production"`, `"environment":"`+environment+`"`), tc.status)
				args := []string{"refs", tc.op, "--app", "shop-api", "--environment", environment}
				if tc.argument != "" {
					args = append(args, tc.argument)
				}
				if main.CmdSecrets(args) != 0 {
					t.Fatal("reference command failed")
				}
				expectedPath, expectedQuery := "/v1/apps/shop-api/secret-references"+tc.suffix, "environment="+environment
				if tc.op == "list" {
					// Human listing also checks source metadata in this exact environment.
					expectedPath, expectedQuery = "/v1/apps/shop-api/secrets", "scope="+environment
				}
				if (*f.SawMethod()) != tc.method || (*f.SawPath()) != expectedPath || (*f.SawQuery()) != expectedQuery {
					t.Fatalf("transport: %s %s", (*f.SawMethod()), (*f.SawPath()))
				}
				if tc.op == "set" {
					var body map[string]string
					if err := json.Unmarshal((*f.SawBody()), &body); err != nil || body["reference"] != "secret:DATABASE" {
						t.Fatalf("source names: %+v %v", body, err)
					}
				}
			})
		}
	}
}

func TestSecretReferenceCLIRejectsImplicitOrInvalidIntent(t *testing.T) {
	for _, args := range [][]string{
		{"list", "--app", "shop-api"},
		{"list", "--app", "shop-api", "--environment", "default"},
		{"list", "--app", "shop-api", "--environment", "__all__"},
		{"set", "--app", "shop-api", "--environment", "production", "URL=plaintext"},
		{"set", "--app", "shop-api", "--environment", "production", "URL=secret:bad-key"},
		{"list", "--app", "shop-api", "--environment", "production", "URL"},
		{"set", "--app", "shop-api", "--environment", "production", "URL=secret:DATABASE", "unexpected"},
		{"unset", "--app", "shop-api", "--environment", "production", "URL", "unexpected"},
	} {
		main.ResetJSONOut(t)
		f := main.AuthedFakeAPI(t, `{}`, 200)
		if main.CmdSecretReferences(args) == 0 || (*f.SawMethod()) != "" {
			t.Fatalf("invalid input reached API: %v", args)
		}
	}
}
