package billing

// Input guards on object-storage billing (ADR-20261027093220) that the weekly
// mutation run (make test-mutation-billing) showed no test noticed changing.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A line-item policy needs a provider, a shadow or live mode, and an
// effective date on a UTC month boundary; any one missing disables it.
func TestObjectStorageLineItemPolicyValid(t *testing.T) {
	month := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	good := ObjectStorageLineItemPolicy{Provider: "polar", Mode: MeterDeliveryLive, EffectiveFrom: month}
	if !good.valid() {
		t.Fatal("a complete policy is invalid")
	}
	cases := map[string]func(p *ObjectStorageLineItemPolicy){
		"no provider":       func(p *ObjectStorageLineItemPolicy) { p.Provider = "" },
		"off mode":          func(p *ObjectStorageLineItemPolicy) { p.Mode = "" },
		"no effective date": func(p *ObjectStorageLineItemPolicy) { p.EffectiveFrom = time.Time{} },
		"not UTC":           func(p *ObjectStorageLineItemPolicy) { p.EffectiveFrom = month.In(time.FixedZone("UTC+2", 2*3600)) },
		"mid-month":         func(p *ObjectStorageLineItemPolicy) { p.EffectiveFrom = month.Add(24 * time.Hour) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := good
			mutate(&p)
			if p.valid() {
				t.Fatalf("policy %+v is valid", p)
			}
		})
	}
}

type noObjectStorageAccounts struct{}

func (noObjectStorageAccounts) ListAllAccounts(context.Context) ([]state.Account, error) {
	return nil, nil
}

// Finalization refuses a missing store, account or account source, and an
// invalid price or allowance, before reading anything.
func TestFinalizeObjectStorageRejectsInvalidRequests(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	pricing := api.ObjectStoragePricing{Currency: "USD", StorageMillicentsPerGiBMonth: 1000}
	badPricing := api.ObjectStoragePricing{Currency: "usd"}
	allowance := api.ObjectStorageAllowance{}
	badAllowance := api.ObjectStorageAllowance{Requests: -1}
	period := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)

	const single = "invalid object storage finalization request"
	singles := map[string]func() error{
		"no store": func() error {
			_, err := FinalizeObjectStoragePeriod(ctx, nil, "acct", pricing, allowance, period, now)
			return err
		},
		"no account": func() error {
			_, err := FinalizeObjectStoragePeriod(ctx, store, "", pricing, allowance, period, now)
			return err
		},
		"bad pricing": func() error {
			_, err := FinalizeObjectStoragePeriod(ctx, store, "acct", badPricing, allowance, period, now)
			return err
		},
		"bad allowance": func() error {
			_, err := FinalizeObjectStoragePeriod(ctx, store, "acct", pricing, badAllowance, period, now)
			return err
		},
	}
	for name, call := range singles {
		if err := call(); err == nil || !strings.Contains(err.Error(), single) {
			t.Fatalf("%s: err = %v, want %q", name, err, single)
		}
	}

	const runner = "invalid object storage period runner"
	runs := map[string]func() error{
		"no accounts": func() error {
			_, err := FinalizeObjectStoragePeriods(ctx, nil, store, pricing, period, now, nil)
			return err
		},
		"no store": func() error {
			_, err := FinalizeObjectStoragePeriods(ctx, noObjectStorageAccounts{}, nil, pricing, period, now, nil)
			return err
		},
	}
	for name, call := range runs {
		if err := call(); err == nil || !strings.Contains(err.Error(), runner) {
			t.Fatalf("%s: err = %v, want %q", name, err, runner)
		}
	}
}
