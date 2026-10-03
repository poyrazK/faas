package api

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strconv"
)

type RouteHealthInvestigationOptions struct {
	Method, Path, CustomerGroupBy, CustomerID string
	StatusCode                                int
}

func (o RouteHealthInvestigationOptions) Validate() error {
	if o.StatusCode != 0 && !slices.Contains([]int{401, 403, 404, 422, 429}, o.StatusCode) {
		return errors.New("status_code must be 0 (all 5xx) or 401, 403, 404, 422, 429")
	}
	if o.CustomerID == "" {
		if o.CustomerGroupBy != "" {
			return errors.New("customer_group_by requires customer_id")
		}
	} else {
		canonical, _ := regexp.MatchString(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`, o.CustomerID)
		if !canonical || o.CustomerGroupBy != "" && o.CustomerGroupBy != "tenant" && o.CustomerGroupBy != "consumer" {
			return errors.New("supply a canonical customer UUID and tenant or consumer dimension")
		}
	}
	return nil
}

func (o RouteHealthInvestigationOptions) Selection() RouteHealthInvestigationSelection {
	s := RouteHealthInvestigationSelection{Method: o.Method, Path: o.Path, StatusCode: o.StatusCode, CustomerGroupBy: o.CustomerGroupBy, CustomerID: o.CustomerID}
	if s.CustomerID != "" && s.CustomerGroupBy == "" {
		s.CustomerGroupBy = "tenant"
	}
	return s
}

func (c *Client) GetRouteHealthInvestigation(ctx context.Context, slug, deploymentID string, opts RouteHealthInvestigationOptions) (RouteHealthInvestigation, error) {
	var out RouteHealthInvestigation
	if err := opts.Validate(); err != nil {
		return out, err
	}
	s := opts.Selection()
	q := url.Values{"method": {s.Method}, "path": {s.Path}, "status_code": {strconv.Itoa(s.StatusCode)}}
	if s.CustomerID != "" {
		q.Set("customer_group_by", s.CustomerGroupBy)
		q.Set("customer_id", s.CustomerID)
	}
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/route-health/deployments/"+url.PathEscape(deploymentID)+"/investigation?"+q.Encode(), nil, &out)
	return out, err
}
