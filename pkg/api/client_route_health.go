package api

import (
	"context"
	"errors"
	"net/url"
)

type RouteHealthReportOptions struct {
	Customers bool
	// CustomerGroupBy is tenant (default) or consumer.
	CustomerGroupBy string
	CustomerDetails bool
}

func (o RouteHealthReportOptions) Validate() error {
	if o.CustomerGroupBy != "" && o.CustomerGroupBy != "tenant" && o.CustomerGroupBy != "consumer" {
		return errors.New("customer_group_by must be tenant or consumer")
	}
	if !o.Customers && (o.CustomerDetails || o.CustomerGroupBy != "") {
		return errors.New("customer options require customers=true")
	}
	return nil
}

func (c *Client) GetRouteHealthGate(ctx context.Context, slug string) (RouteHealthGate, error) {
	var out RouteHealthGate
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/route-health/gate", nil, &out)
	return out, err
}
func (c *Client) SetRouteHealthGate(ctx context.Context, slug string, request SetRouteHealthGateRequest) (RouteHealthGate, error) {
	var out RouteHealthGate
	err := c.do(ctx, "PUT", "/v1/apps/"+url.PathEscape(slug)+"/route-health/gate", request, &out)
	return out, err
}
func (c *Client) GetRouteHealthReport(ctx context.Context, slug, deploymentID string) (RouteHealthReport, error) {
	return c.GetRouteHealthReportWithOptions(ctx, slug, deploymentID, RouteHealthReportOptions{})
}

func (c *Client) GetRouteHealthReportWithOptions(ctx context.Context, slug, deploymentID string, opts RouteHealthReportOptions) (RouteHealthReport, error) {
	var out RouteHealthReport
	if err := opts.Validate(); err != nil {
		return out, err
	}
	path := "/v1/apps/" + url.PathEscape(slug) + "/route-health/deployments/" + url.PathEscape(deploymentID)
	if opts.Customers {
		q := url.Values{"customers": {"true"}}
		if opts.CustomerGroupBy != "" {
			q.Set("customer_group_by", opts.CustomerGroupBy)
		}
		if opts.CustomerDetails {
			q.Set("customer_details", "true")
		}
		path += "?" + q.Encode()
	}
	err := c.do(ctx, "GET", path, nil, &out)
	return out, err
}
