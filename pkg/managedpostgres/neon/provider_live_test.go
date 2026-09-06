package neon

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// TestLiveProviderLifecycle is deliberately opt-in because it creates a paid
// upstream resource. Use only an isolated Neon organization. The cleanup is
// registered immediately after Neon accepts the project.
func TestLiveProviderLifecycle(t *testing.T) {
	if os.Getenv("FAAS_NEON_LIVE_TEST") != "1" {
		t.Skip("set FAAS_NEON_LIVE_TEST=1 to run the Neon qualification test")
	}
	organizationID := os.Getenv("FAAS_NEON_TEST_ORG_ID")
	regionID := os.Getenv("FAAS_NEON_TEST_REGION_ID")
	apiKey := os.Getenv("FAAS_NEON_TEST_API_KEY")
	if organizationID == "" || regionID == "" || apiKey == "" {
		t.Fatal("FAAS_NEON_TEST_ORG_ID, FAAS_NEON_TEST_REGION_ID, and FAAS_NEON_TEST_API_KEY are required")
	}
	config := managedpostgres.BackendConfig{
		Region: "qualification", Namespace: organizationID,
		Settings: map[string]string{
			settingRegionID: regionID, settingDatabaseName: "gregale",
			settingMaxStorageBytes: "1073741824", settingMaxRestoreWindow: "0",
		},
		SecretEnv: map[string]string{apiKeySecret: "FAAS_NEON_TEST_API_KEY"},
	}
	providerInterface, err := New(config, os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	provider := providerInterface.(*Provider)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	resourceID := uuid.NewString()
	providerResourceID := ""
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), time.Minute) //nolint:contextcheck // Cleanup must outlive the test context.
		defer cleanupCancel()
		if providerResourceID == "" {
			providerResourceID, _ = provider.findProject(cleanupContext, provider.projectName(resourceID))
		}
		if providerResourceID != "" {
			_, _ = provider.Delete(cleanupContext, managedpostgres.DeleteRequest{ResourceID: resourceID, ProviderResourceID: providerResourceID, IdempotencyKey: "delete-" + resourceID})
		}
	})
	spec := managedpostgres.Spec{
		Region: "qualification", PostgresMajor: 17, Class: managedpostgres.ClassDevelopment,
		Availability: managedpostgres.AvailabilitySingleZone, ScaleToZero: true,
		StorageLimitBytes: 1 << 30,
	}
	observed, err := provider.Provision(ctx, managedpostgres.ProvisionRequest{ResourceID: resourceID, Spec: spec, IdempotencyKey: "provision-" + resourceID})
	if err != nil {
		t.Fatal(err)
	}
	providerResourceID = observed.ProviderResourceID
	for observed.Status != managedpostgres.ProviderStatusReady {
		if observed.Status == managedpostgres.ProviderStatusFailed {
			t.Fatal("Neon provisioning reached failed state")
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(2 * time.Second):
		}
		observed, err = provider.Inspect(ctx, observed.ProviderResourceID)
		if err != nil {
			t.Fatal(err)
		}
	}
	credentialRequest := managedpostgres.CredentialRequest{
		ProviderResourceID: observed.ProviderResourceID,
		IdentityKey:        "qualification-" + uuid.NewString(),
		Access:             managedpostgres.CredentialReadWrite,
		IdempotencyKey:     "qualification-credentials-" + resourceID,
	}
	material, err := provider.IssueCredentials(ctx, credentialRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := material.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := provider.RevokeCredentials(ctx, credentialRequest); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Delete(ctx, managedpostgres.DeleteRequest{ResourceID: resourceID, ProviderResourceID: observed.ProviderResourceID, IdempotencyKey: "delete-" + resourceID}); err != nil {
		t.Fatal(err)
	}
}
