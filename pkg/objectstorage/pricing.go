package objectstorage

import (
	"errors"
	"math/big"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	bytesPerGiB        int64 = 1 << 30
	storageHoursMonth  int64 = 730
	requestsPerMillion int64 = 1_000_000
)

var (
	ErrInvalidPricing  = errors.New("object storage: invalid pricing")
	ErrPricingOverflow = errors.New("object storage: pricing result overflows int64")
)

// CalculateCharge converts normalized cumulative usage into a customer-facing
// estimate. It uses integer arithmetic throughout. Storage is priced in
// GiB-months using the conventional 730-hour month; requests are priced per
// million and egress per GiB. Each component is rounded up independently to
// the nearest millicent.
func CalculateCharge(pricing api.ObjectStoragePricing, usage api.ObjectStorageUsage) (api.ObjectStorageCharge, error) {
	if !pricing.Valid() {
		return api.ObjectStorageCharge{}, ErrInvalidPricing
	}
	for _, v := range []int64{usage.StoredByteHours, usage.RequestCount, usage.EgressBytes} {
		if v < 0 {
			return api.ObjectStorageCharge{}, ErrInvalidPricing
		}
	}

	storage, err := ceilMulDiv(usage.StoredByteHours, pricing.StorageMillicentsPerGiBMonth, bytesPerGiB*storageHoursMonth)
	if err != nil {
		return api.ObjectStorageCharge{}, err
	}
	requests, err := ceilMulDiv(usage.RequestCount, pricing.RequestsMillicentsPerMillion, requestsPerMillion)
	if err != nil {
		return api.ObjectStorageCharge{}, err
	}
	egress, err := ceilMulDiv(usage.EgressBytes, pricing.EgressMillicentsPerGiB, bytesPerGiB)
	if err != nil {
		return api.ObjectStorageCharge{}, err
	}
	total, err := addCharges(storage, requests, egress)
	if err != nil {
		return api.ObjectStorageCharge{}, err
	}
	return api.ObjectStorageCharge{
		Currency:           pricing.Currency,
		StorageMillicents:  storage,
		RequestsMillicents: requests,
		EgressMillicents:   egress,
		TotalMillicents:    total,
	}, nil
}

// ceilMulDiv computes ceil(a*b/divisor) without an intermediate int64
// overflow. The configured bounds are intentionally generous enough for real
// deployments, so using big.Int here keeps the money path auditable instead
// of relying on architecture-dependent overflow behavior.
func ceilMulDiv(a, b, divisor int64) (int64, error) {
	if a < 0 || b < 0 || divisor <= 0 {
		return 0, ErrInvalidPricing
	}
	if a == 0 || b == 0 {
		return 0, nil
	}
	n := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	n.Add(n, new(big.Int).Sub(big.NewInt(divisor), big.NewInt(1)))
	n.Quo(n, big.NewInt(divisor))
	if !n.IsInt64() {
		return 0, ErrPricingOverflow
	}
	return n.Int64(), nil
}

func addCharges(values ...int64) (int64, error) {
	var total big.Int
	for _, value := range values {
		if value < 0 {
			return 0, ErrInvalidPricing
		}
		total.Add(&total, big.NewInt(value))
	}
	if !total.IsInt64() {
		return 0, ErrPricingOverflow
	}
	return total.Int64(), nil
}
