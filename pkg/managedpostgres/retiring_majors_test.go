package managedpostgres

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// A retiring major stays supported for the databases that have it and is
// closed to new ones.
func TestRetiringMajorsCloseOnlyNewDatabases(t *testing.T) {
	capabilities := testCapabilities()
	capabilities.RetiringMajors = []int{16}
	if err := capabilities.Validate(); err != nil {
		t.Fatal(err)
	}
	existing := testSpec()
	existing.PostgresMajor = 16
	if err := capabilities.Supports(existing); err != nil {
		t.Fatalf("an existing PostgreSQL 16 database lost support: %v", err)
	}
	err := capabilities.SupportsNewDatabase(existing)
	if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "retiring") || !strings.Contains(err.Error(), "offered: 17") {
		t.Fatalf("new PostgreSQL 16 database = %v", err)
	}
	if err := capabilities.SupportsNewDatabase(testSpec()); err != nil {
		t.Fatalf("new PostgreSQL 17 database = %v", err)
	}
	if got := capabilities.NewDatabaseMajors(); !slices.Equal(got, []int{17}) {
		t.Fatalf("new database majors = %v", got)
	}

	for name, retiring := range map[string][]int{"not offered": {15}, "every major": {16, 17}, "duplicate": {16, 16}} {
		invalid := testCapabilities()
		invalid.RetiringMajors = retiring
		if !errors.Is(invalid.Validate(), ErrInvalid) {
			t.Errorf("%s: retiring %v validated", name, retiring)
		}
	}
}

func TestCreateAndDiscoveryLeaveOutRetiringMajors(t *testing.T) {
	provider := &fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady, inspectStatus: ProviderStatusReady}
	provider.capabilities.RetiringMajors = []int{16}
	service := testService(t, testRegistry(t, provider, nil), NewMemoryStore())
	spec := testSpec()
	spec.PostgresMajor = 16
	if _, err := service.Create(context.Background(), CreateRequest{AccountID: "account", Name: "orders", Spec: spec}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("create on a retiring major = %v", err)
	}
	discovery, err := service.DiscoverCapabilities(context.Background(), "account", "")
	if err != nil || !slices.Equal(discovery.Capabilities.PostgresMajors, []int{17}) || discovery.Capabilities.RetiringMajors != nil {
		t.Fatalf("discovery = %+v, %v", discovery.Capabilities, err)
	}
}
