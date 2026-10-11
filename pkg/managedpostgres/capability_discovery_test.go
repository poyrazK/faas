package managedpostgres

import (
	"context"
	"errors"
	"testing"
)

func TestCapabilityDiscoveryDoesNotCallProviderOrChangePlacement(t *testing.T) {
	provider := &fakeProvider{capabilities: testCapabilities()}
	registry := testRegistry(t, provider, nil)
	service, err := NewService(registry, NewMemoryStore(), ServiceOptions{
		ProvisioningEnabled: func() bool { return true },
		ProvisioningAllowed: func(_ context.Context, account string) bool { return account == "canary" },
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, account := range []string{"canary", "outside-canary"} {
		d, err := service.DiscoverCapabilities(context.Background(), account, "")
		if err != nil || d.Region != "us-east-1" || d.DatabaseLimit != 2 || d.ProvisioningEnabled != (account == "canary") {
			t.Fatalf("discovery=%+v err=%v", d, err)
		}
		d.Capabilities.CredentialAccess[0] = CredentialMigration
		d.Capabilities.PostgresMajors[0] = 99
	}
	backend, err := registry.Default("us-east-1")
	if err != nil || backend.Capabilities.CredentialAccess[0] != CredentialReadWrite || backend.Capabilities.PostgresMajors[0] == 99 {
		t.Fatal("discovery mutated placement capabilities", err)
	}
	if provider.provisionCalls != 0 || provider.inspectCalls != 0 || provider.deleteCalls != 0 || provider.restoreCalls != 0 {
		t.Fatal("capability discovery contacted the provider")
	}
	if _, err := service.DiscoverCapabilities(context.Background(), "canary", "unconfigured"); !errors.Is(err, ErrUnsupported) {
		t.Fatal("unknown placement accepted", err)
	}
}

func TestCapabilityDiscoveryKeepsUnsupportedReadOnlyAbsent(t *testing.T) {
	capabilities := testCapabilities()
	capabilities.CredentialAccess = []CredentialAccess{CredentialReadWrite}
	provider := &fakeProvider{capabilities: capabilities}
	service := testService(t, testRegistry(t, provider, nil), NewMemoryStore())
	d, err := service.DiscoverCapabilities(context.Background(), "account", "")
	if err != nil || contains(d.Capabilities.CredentialAccess, CredentialReadOnly) {
		t.Fatal("discovery advertised an unsupported credential contract", err)
	}
}
