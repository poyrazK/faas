package sched

import (
	"context"
	"fmt"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

type operatorParkVMM interface {
	OperatorRestartGeneration(context.Context, string, string) (string, error)
	ExecuteOperatorPark(context.Context, string, string, string) error
}

type operatorParkNode interface {
	ExecuteOperatorPark(context.Context, string, string) error
}

func (c *VMMClient) ExecuteOperatorPark(ctx context.Context, id, token string) error {
	response, err := c.cli.ExecuteOperatorPark(ctx, &vmmdpb.ExecuteOperatorParkRequest{IntentId: id, ClaimToken: token})
	if err != nil {
		return fmt.Errorf("execute operator park: %w", err)
	}
	if !response.GetAcknowledged() {
		return fmt.Errorf("park outcome is uncertain (phase %s)", response.GetPhase())
	}
	return nil
}

func (r *VMMRouter) ExecuteOperatorPark(ctx context.Context, nodeID, id, token string) error {
	client, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return err
	}
	park, ok := client.(operatorParkNode)
	if !ok {
		return fmt.Errorf("vmmd client lacks durable operator park support")
	}
	return park.ExecuteOperatorPark(ctx, id, token)
}
