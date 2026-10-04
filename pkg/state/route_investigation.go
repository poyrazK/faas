package state

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrRouteInvestigationPlan = errors.New("route investigation requires request telemetry")

type RouteHealthInvestigationStore interface {
	GetRouteHealthInvestigation(context.Context, string, string, string, api.RouteHealthInvestigationOptions) (api.RouteHealthInvestigation, error)
}
