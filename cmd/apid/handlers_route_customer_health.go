package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func routeCustomerHealthOptions(r *http.Request) (api.RouteHealthReportOptions, error) {
	opts := api.RouteHealthReportOptions{}
	q := r.URL.Query()
	for _, entry := range []struct {
		name   string
		target *bool
	}{{"customers", &opts.Customers}, {"customer_details", &opts.CustomerDetails}} {
		if values, ok := q[entry.name]; ok {
			if len(values) != 1 {
				return opts, errors.New("customer options must not be repeated")
			}
			value, err := strconv.ParseBool(values[0])
			if err != nil {
				return opts, errors.New("customers and customer_details must be booleans")
			}
			*entry.target = value
		}
	}
	if values, ok := q["customer_group_by"]; ok {
		if len(values) != 1 || values[0] == "" {
			return opts, errors.New("supply one customer_group_by value")
		}
		opts.CustomerGroupBy = values[0]
	}
	if err := opts.Validate(); err != nil {
		return opts, err
	}
	if opts.Customers && opts.CustomerGroupBy == "" {
		opts.CustomerGroupBy = "tenant"
	}
	return opts, nil
}

func (s *server) readRouteHealthReport(ctx context.Context, store state.RouteHealthStore, accountID, appID, deploymentID string, opts api.RouteHealthReportOptions) (api.RouteHealthReport, error) {
	if !opts.Customers {
		return store.GetRouteHealthReport(ctx, accountID, appID, deploymentID)
	}
	customers, ok := s.store.(state.RouteCustomerHealthStore)
	if !ok {
		return api.RouteHealthReport{}, errors.New("customer route health unavailable")
	}
	return customers.GetRouteHealthReportWithCustomers(ctx, accountID, appID, deploymentID, opts.CustomerGroupBy, opts.CustomerDetails)
}
