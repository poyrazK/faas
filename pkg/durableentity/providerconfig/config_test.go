package providerconfig

// adr: 712

import (
	"errors"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/objectstorage"
)

type conditionalFixture struct {
	objectstorage.Provider
	objectstorage.ConditionalStateProvider
}

func TestProviderSelectionKeepsCredentialsAndEndpointsSeparate(t *testing.T) {
	for _, test := range []struct {
		name   string
		driver string
		target string
		want   objectstorage.BackendConfig
	}{
		{"legacy S3", "", "ignored@example.iam.gserviceaccount.com", objectstorage.BackendConfig{Driver: "s3", Endpoint: "https://s3.invalid", S3Region: "us-east-1", PathStyle: true, AccessKeyEnv: "AWS_ACCESS_KEY_ID", SecretKeyEnv: "AWS_SECRET_ACCESS_KEY", SessionTokenEnv: "AWS_SESSION_TOKEN"}},
		{"explicit S3", "s3", "", objectstorage.BackendConfig{Driver: "s3", Endpoint: "https://s3.invalid", S3Region: "us-east-1", PathStyle: true, AccessKeyEnv: "AWS_ACCESS_KEY_ID", SecretKeyEnv: "AWS_SECRET_ACCESS_KEY", SessionTokenEnv: "AWS_SESSION_TOKEN"}},
		{"GCS ADC", "gcs", "", objectstorage.BackendConfig{Driver: "gcs"}},
		{"GCS impersonation", "gcs", "entities@example.iam.gserviceaccount.com", objectstorage.BackendConfig{Driver: "gcs", GCSServiceAccount: "entities@example.iam.gserviceaccount.com", GCSImpersonateServiceAccount: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := map[string]string{"GREGALE_ENTITY_PROVIDER": test.driver, "GREGALE_ENTITY_BUCKET": "private", "GREGALE_ENTITY_ENDPOINT": "https://s3.invalid", "GREGALE_ENTITY_REGION": "us-east-1", "GREGALE_ENTITY_GCS_IMPERSONATE_SERVICE_ACCOUNT": test.target, "AWS_ACCESS_KEY_ID": "fixture-key"}
			fixture, calls := &conditionalFixture{}, 0
			factory := func(config objectstorage.BackendConfig, getenv func(string) string) (objectstorage.Provider, error) {
				calls++
				if !reflect.DeepEqual(config, test.want) || getenv("AWS_ACCESS_KEY_ID") != "fixture-key" {
					t.Fatal("selected provider received the wrong configuration")
				}
				return fixture, nil
			}
			factories := map[string]objectstorage.Factory{test.want.Driver: factory}
			selection, err := open(func(key string) string { return env[key] }, factories)
			if err != nil || calls != 1 || selection.Driver != test.want.Driver || selection.Bucket != "private" || selection.Provider != fixture {
				t.Fatalf("selection = %+v, calls = %d, error = %v", selection, calls, err)
			}
		})
	}
}

func TestInvalidConfigurationDoesNotConstructAProvider(t *testing.T) {
	for _, test := range []struct {
		name string
		env  map[string]string
	}{
		{"unknown driver", map[string]string{"GREGALE_ENTITY_PROVIDER": "typo", "GREGALE_ENTITY_BUCKET": "private"}},
		{"missing S3 bucket", map[string]string{"GREGALE_ENTITY_ENDPOINT": "https://s3.invalid", "GREGALE_ENTITY_REGION": "auto"}},
		{"missing GCS bucket", map[string]string{"GREGALE_ENTITY_PROVIDER": "gcs"}},
		{"missing S3 endpoint", map[string]string{"GREGALE_ENTITY_BUCKET": "private", "GREGALE_ENTITY_REGION": "auto"}},
		{"missing S3 region", map[string]string{"GREGALE_ENTITY_BUCKET": "private", "GREGALE_ENTITY_ENDPOINT": "https://s3.invalid"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			factory := func(objectstorage.BackendConfig, func(string) string) (objectstorage.Provider, error) {
				t.Fatal("invalid settings reached the credential/provider constructor")
				return nil, nil
			}
			_, err := open(func(key string) string { return test.env[key] }, map[string]objectstorage.Factory{"s3": factory, "gcs": factory})
			if err == nil {
				t.Fatal("invalid configuration was accepted")
			}
		})
	}
}

func TestProviderConstructionFailsClosed(t *testing.T) {
	sentinel := errors.New("fixture credential failure")
	getenv := func(key string) string {
		return map[string]string{"GREGALE_ENTITY_PROVIDER": "gcs", "GREGALE_ENTITY_BUCKET": "private"}[key]
	}
	_, err := open(getenv, map[string]objectstorage.Factory{"gcs": func(objectstorage.BackendConfig, func(string) string) (objectstorage.Provider, error) {
		return nil, sentinel
	}})
	if !errors.Is(err, sentinel) {
		t.Fatal("provider construction error was not preserved", err)
	}
	_, err = open(getenv, map[string]objectstorage.Factory{"gcs": func(objectstorage.BackendConfig, func(string) string) (objectstorage.Provider, error) {
		return struct{ objectstorage.Provider }{}, nil
	}})
	if err == nil {
		t.Fatal("provider without conditional state support was accepted")
	}
}
