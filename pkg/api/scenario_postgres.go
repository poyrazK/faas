package api

import (
	"context"
	"errors"
	"net/url"

	"github.com/onebox-faas/faas/pkg/rehearsalpg"
)

func (c *Client) CreateScenarioPostgres(ctx context.Context, run string, req rehearsalpg.CreateRequest) (rehearsalpg.Receipt, error) {
	var out rehearsalpg.Receipt
	if req.Spec.Validate() != nil || len(req.SQL) == 0 || len(req.SQL) > rehearsalpg.MaxFixtureBytes || rehearsalpg.HashBytes([]byte(req.SQL)) != req.Spec.FixtureSHA256 {
		return out, errors.New("invalid PostgreSQL fixture")
	}
	if e := c.do(ctx, "POST", "/v1/dev/test-runs/"+url.PathEscape(run)+"/postgres", req, &out); e != nil {
		return out, e
	}
	if out.Version != 1 || out.RunID != run || out.AppID == "" || out.ResourceID == "" || out.FixtureSHA256 != req.Spec.FixtureSHA256 || out.ContractSHA256 != req.Spec.ContractSHA256() || len(out.InitialSHA256) != 64 {
		return rehearsalpg.Receipt{}, errors.New("server did not acknowledge the exact isolated PostgreSQL fixture")
	}
	return out, nil
}

func (c *Client) SealScenarioPostgres(ctx context.Context, run, deployment string) (rehearsalpg.Evidence, error) {
	var out rehearsalpg.Evidence
	if e := c.do(ctx, "POST", "/v1/dev/test-runs/"+url.PathEscape(run)+"/postgres/seal", rehearsalpg.SealRequest{DeploymentID: deployment}, &out); e != nil {
		return out, e
	}
	if out.Validate() != nil || out.RunID != run || out.DeploymentID != deployment {
		return out, errors.New("server did not return complete PostgreSQL evidence for this execution")
	}
	return out, nil
}
func (c *Client) DeleteScenarioPostgres(ctx context.Context, run string) error {
	return c.do(ctx, "DELETE", "/v1/dev/test-runs/"+url.PathEscape(run)+"/postgres", nil, nil)
}
