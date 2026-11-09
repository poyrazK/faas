package postgresfixture

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	mp "github.com/onebox-faas/faas/pkg/managedpostgres"
)

// Found on the n2 check of ADR-20261106105015: an API or gregale.yaml
// `create:` fills the plan's storage limit and restore window, which the
// fixture refused ("point-in-time restore is not offered here", then
// "storage limit ... exceeds the 1073741824 byte maximum"). Every plan's
// defaults must be admitted; restores themselves stay unsupported.
func TestCapabilitiesAdmitEveryPlanDefault(t *testing.T) {
	provider := &Provider{major: 16}
	capabilities := provider.Capabilities()
	if err := capabilities.Validate(); err != nil {
		t.Fatalf("fixture capabilities invalid: %v", err)
	}
	for _, plan := range api.Plans {
		limits, ok := api.ManagedPostgresLimitsFor(plan)
		if !ok {
			continue
		}
		spec := mp.Spec{Region: "us-east-1", PostgresMajor: 16, Class: mp.ClassDevelopment, Availability: mp.AvailabilitySingleZone,
			ScaleToZero: true, StorageLimitBytes: limits.StorageLimitBytes, RestoreWindowSeconds: limits.RestoreWindowSeconds}
		if err := capabilities.Supports(spec); err != nil {
			t.Errorf("%s defaults refused: %v", plan, err)
		}
	}
	if _, err := provider.Restore(t.Context(), mp.RestoreRequest{}); !errors.Is(err, mp.ErrUnsupported) {
		t.Fatalf("restore = %v, want ErrUnsupported (retention is only recorded)", err)
	}
}
