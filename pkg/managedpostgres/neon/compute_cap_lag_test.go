// adr: 20261025094915 — amendment 2026-11-04: a database's counter lag is its
// largest size for the time Neon's counters trail use.
package neon

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestCounterLagUnitSecondsIsTheLargestSizeForTheLag(t *testing.T) {
	provider := &Provider{}
	for class, want := range map[managedpostgres.ServiceClass]int64{
		managedpostgres.ClassDevelopment: 1800,
		managedpostgres.ClassBurstable:   3600,
		managedpostgres.ClassProduction:  7200,
		"unknown":                        7200,
	} {
		if got := provider.CounterLagUnitSeconds(managedpostgres.Spec{Class: class}); got != want {
			t.Errorf("%s: %d; want %d", class, got, want)
		}
	}
}
