package storagefixture

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/objectstorage"
)

func TestProviderConfigAllowsPublicBucketProvisioning(t *testing.T) {
	provider := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(provider.Close)
	gateway := NewGateway(t, "127.0.0.1:0")
	config := ProviderConfig(provider.URL, gateway.URL, filepath.Join(t.TempDir(), "usage.json"))
	registry, err := objectstorage.NewRegistry(config, func(string) string { return "owned-native-fixture" }, map[string]objectstorage.Factory{"s3": objectstorage.NewS3})
	if err != nil || !registry.CanProvision("us-east-1") || registry.PublicEndpoint != gateway.URL {
		t.Fatal("native fixture disabled public bucket creation or misdirected compute credentials")
	}
	config.Backends[0].UsageReportsPath = ""
	registry, err = objectstorage.NewRegistry(config, func(string) string { return "owned-native-fixture" }, map[string]objectstorage.Factory{"s3": objectstorage.NewS3})
	if err != nil || registry.CanProvision("us-east-1") {
		t.Fatal("fixture bypassed the production accounting provisioning gate")
	}
}
