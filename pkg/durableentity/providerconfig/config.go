// Package providerconfig selects private storage for the trusted entity operator
// command and opt-in provider qualification. It does not provision buckets or
// configure the platform invocation preview.
package providerconfig

// adr: 712

import (
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/objectstorage"
)

// Selection identifies an existing bucket and its conditional-state provider.
type Selection struct {
	Driver   string
	Bucket   string
	Provider objectstorage.ConditionalStateProvider
}

// Open uses the legacy S3 environment by default. GCS uses process Application
// Default Credentials; getenv only supplies non-secret GCS operator settings.
func Open(getenv func(string) string) (Selection, error) {
	return open(getenv, map[string]objectstorage.Factory{"s3": objectstorage.NewS3, "gcs": objectstorage.NewGCS})
}

func open(getenv func(string) string, factories map[string]objectstorage.Factory) (Selection, error) {
	driver, bucket, backend, err := configuration(getenv)
	if err != nil {
		return Selection{}, err
	}
	provider, err := factories[driver](backend, getenv)
	if err != nil {
		return Selection{}, fmt.Errorf("entity %s provider: %w", driver, err)
	}
	conditional, ok := provider.(objectstorage.ConditionalStateProvider)
	if !ok {
		return Selection{}, errors.New("entity provider requires conditional state support")
	}
	return Selection{Driver: driver, Bucket: bucket, Provider: conditional}, nil
}

func configuration(getenv func(string) string) (string, string, objectstorage.BackendConfig, error) {
	driver, bucket := getenv("GREGALE_ENTITY_PROVIDER"), getenv("GREGALE_ENTITY_BUCKET")
	if driver == "" {
		driver = "s3"
	}
	if driver != "s3" && driver != "gcs" {
		return "", "", objectstorage.BackendConfig{}, errors.New("GREGALE_ENTITY_PROVIDER must be s3 or gcs")
	}
	if bucket == "" {
		return "", "", objectstorage.BackendConfig{}, errors.New("set GREGALE_ENTITY_BUCKET to a dedicated private test bucket")
	}
	backend := objectstorage.BackendConfig{Driver: driver}
	if driver == "gcs" {
		// Do not forward a leftover S3 endpoint or AWS credentials to GCS.
		// The native Google endpoint and ADC path are selected by NewGCS.
		backend.GCSServiceAccount = getenv("GREGALE_ENTITY_GCS_IMPERSONATE_SERVICE_ACCOUNT")
		backend.GCSImpersonateServiceAccount = backend.GCSServiceAccount != ""
		return driver, bucket, backend, nil
	}
	backend.Endpoint, backend.S3Region = getenv("GREGALE_ENTITY_ENDPOINT"), getenv("GREGALE_ENTITY_REGION")
	if backend.Endpoint == "" || backend.S3Region == "" {
		return "", "", objectstorage.BackendConfig{}, errors.New("S3 requires GREGALE_ENTITY_ENDPOINT and GREGALE_ENTITY_REGION")
	}
	backend.PathStyle = true
	backend.AccessKeyEnv, backend.SecretKeyEnv, backend.SessionTokenEnv = "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN"
	return driver, bucket, backend, nil
}
