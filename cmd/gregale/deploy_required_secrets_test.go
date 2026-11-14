package main_test

import (
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMissingRequiredSecretNames(t *testing.T) {
	secrets := api.AppSecretListResponse{Secrets: []api.AppSecretResponse{
		{Key: "DATABASE_URL", Scope: "default"},
		{Key: "STRIPE_KEY", Scope: "staging"},
	}}
	env := api.AppEnvListResponse{Env: []api.AppEnvResponse{{Key: "LOG_LEVEL"}}}
	for _, tc := range []struct {
		name     string
		required []string
		scope    string
		want     []string
	}{
		{"all present", []string{"DATABASE_URL", "LOG_LEVEL"}, "", nil},
		{"missing sorted and deduplicated", []string{"REDIS_URL", "API_TOKEN", "REDIS_URL", "DATABASE_URL"}, "", []string{"API_TOKEN", "REDIS_URL"}},
		{"secret of another scope does not count", []string{"STRIPE_KEY"}, "default", []string{"STRIPE_KEY"}},
		{"secret of the deploy scope counts", []string{"STRIPE_KEY"}, "staging", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := main.MissingRequiredSecretNames(tc.required, tc.scope, secrets, env)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("missing = %v, want %v", got, tc.want)
			}
		})
	}
}
