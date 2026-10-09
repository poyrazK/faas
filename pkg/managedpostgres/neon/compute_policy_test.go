// adr: 624 — update only idle policy on the existing pinned primary.
package neon

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestComputePolicyAdoptsLostResponseWithoutChangingCapacity(t *testing.T) {
	for _, target := range []bool{false, true} {
		f := &resizeHTTPFixture{policy: true, lostResponse: true}
		if target {
			f.suspendTimeout = -1
		}
		provider := resizeHTTPProvider(t, f)
		request := resizeHTTPrequest()
		request.PreviousSpec.ScaleToZero = !target
		request.Spec = request.PreviousSpec
		request.Spec.ScaleToZero = target
		if _, err := provider.Update(t.Context(), request); !errors.Is(err, managedpostgres.ErrUnavailable) {
			t.Fatal("lost response", err)
		}
		observed, err := provider.Update(t.Context(), request)
		if err != nil || observed.Status != managedpostgres.ProviderStatusReady || observed.Spec != request.Spec || f.maximum != 2 || f.patches != 1 {
			t.Fatal("recovery", observed, err, f.patches)
		}
		if _, err := provider.Update(t.Context(), request); err != nil || f.patches != 1 {
			t.Fatal("replay", err)
		}
	}
}

func TestComputePolicyWaitsAndRejectsDriftBeforeMutation(t *testing.T) {
	for _, fault := range []string{"pending", "delayed", "default", "duplicate", "storage", "mixed"} {
		t.Run(fault, func(t *testing.T) {
			f := &resizeHTTPFixture{policy: true}
			switch fault {
			case "pending":
				f.pending = true
			case "delayed":
				f.delayed = true
			case "default":
				f.changedDefault = true
			case "duplicate":
				f.duplicatePrimary = true
			case "storage":
				f.drift = true
			}
			provider := resizeHTTPProvider(t, f)
			request := resizeHTTPrequest()
			request.Spec = request.PreviousSpec
			request.Spec.ScaleToZero = false
			if fault == "mixed" {
				request.Spec.Class = managedpostgres.ClassDevelopment
			}
			observed, err := provider.Update(t.Context(), request)
			if fault == "pending" || fault == "delayed" {
				if err != nil || observed.Status != managedpostgres.ProviderStatusPending {
					t.Fatal(observed, err)
				}
			} else if err == nil {
				t.Fatal("unsafe mutation accepted")
			}
			if fault != "delayed" && f.patches != 0 {
				t.Fatal("mutation despite conflict")
			}
		})
	}
}
