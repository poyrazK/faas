package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

// Read projections are separate from the admission and membership contracts.
type ProductCustomerOverviewStore interface {
	GetProductCustomerWorkSummary(context.Context, string, string, string) (api.ProductCustomerWorkSummary, error)
}
