package storagefixture

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

// ProviderConfig enables the same provider-report provisioning boundary as the
// portable API fixture. Accounting is seeded in the disposable catalog before
// issuing storage operations; an absent report path would disable bucket creation.
func ProviderConfig(provider, endpoint, reportsPath string) objectstorage.Config {
	return objectstorage.Config{DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "e2e-s3"},
		PublicEndpoint: endpoint, PublicRegion: "us-east-1", MaxBucketsPerApp: 10, MaxUploadBytes: api.MaxObjectUploadBytes,
		Accounting: &api.ObjectStoragePolicy{MaxAccountBytes: 1 << 30, MaxBucketBytes: 1 << 30, MaxAccountKeys: 1000,
			MaxMonthlyCostMillicents: 1 << 30, MaxMonthlyRequests: 1000, MaxMonthlyEgressBytes: 1 << 30,
			MaxMonthlyAuthorizations: 1000, MaxReportAgeSeconds: 3600},
		Backends: []objectstorage.BackendConfig{{ID: "e2e-s3", Driver: "s3", Region: "us-east-1", Namespace: "native-storage",
			Endpoint: provider, S3Region: "us-east-1", PathStyle: true, AllowHTTP: true, UsageReportsPath: reportsPath,
			AccessKeyEnv: "FAAS_E2E_S3_ACCESS_KEY", SecretKeyEnv: "FAAS_E2E_S3_SECRET_KEY"}}}
}
