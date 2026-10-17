package api

import (
	"context"
	"errors"
	"net/url"

	"github.com/onebox-faas/faas/pkg/rehearsalobj"
)

func (c *Client) CreateScenarioObjects(ctx context.Context, run string, req rehearsalobj.CreateRequest) (rehearsalobj.Receipt, error) {
	var out rehearsalobj.Receipt
	if e := req.Validate(); e != nil {
		return out, e
	}
	if e := c.do(ctx, "POST", "/v1/dev/test-runs/"+url.PathEscape(run)+"/objects", req, &out); e != nil {
		return out, e
	}
	if out.Version != 1 || out.CollectorID == "" || out.RunID != run || out.AppID == "" || out.ResourceID == "" || out.FixtureSHA256 != req.Spec.FixtureSHA256 || out.ContractSHA256 != req.Spec.ContractSHA256() || out.InitialSHA256 != req.Spec.FixtureSHA256 {
		return rehearsalobj.Receipt{}, errors.New("server did not acknowledge the exact object fixture")
	}
	return out, nil
}
func (c *Client) SealScenarioObjects(ctx context.Context, run, deployment string) (rehearsalobj.Evidence, error) {
	var out rehearsalobj.Evidence
	if e := c.do(ctx, "POST", "/v1/dev/test-runs/"+url.PathEscape(run)+"/objects/seal", rehearsalobj.SealRequest{DeploymentID: deployment}, &out); e != nil {
		return out, e
	}
	if out.Validate() != nil || out.RunID != run || out.DeploymentID != deployment {
		return out, errors.New("server did not return complete object evidence for this execution")
	}
	return out, nil
}
func (c *Client) DeleteScenarioObjects(ctx context.Context, run string) error {
	return c.do(ctx, "DELETE", "/v1/dev/test-runs/"+url.PathEscape(run)+"/objects", nil, nil)
}
