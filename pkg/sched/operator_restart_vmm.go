package sched

import (
	"context"
	"fmt"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

type operatorRestartVMM interface {
	OperatorRestartGeneration(context.Context, string, string) (string, error)
	ExecuteOperatorRestartDestroy(context.Context, string, string, string) error
}

type operatorRestartNode interface {
	OperatorRestartGeneration(context.Context, string) (string, error)
	ExecuteOperatorRestartDestroy(context.Context, string, string) error
}

func (c *VMMClient) OperatorRestartGeneration(ctx context.Context, instance string) (string, error) {
	response, err := c.cli.GetOperatorRestartGeneration(ctx, &vmmdpb.GetOperatorRestartGenerationRequest{Instance: instance})
	if err != nil {
		return "", fmt.Errorf("get restart VM generation: %w", err)
	}
	if response.GetGeneration() == "" {
		return "", fmt.Errorf("vmmd returned an empty restart VM generation")
	}
	return response.GetGeneration(), nil
}

func (c *VMMClient) ExecuteOperatorRestartDestroy(ctx context.Context, intentID, token string) error {
	response, err := c.cli.ExecuteOperatorRestartDestroy(ctx, &vmmdpb.ExecuteOperatorRestartDestroyRequest{IntentId: intentID, ClaimToken: token})
	if err != nil {
		return fmt.Errorf("execute operator restart: %w", err)
	}
	if !response.GetAcknowledged() {
		return fmt.Errorf("restart teardown outcome is uncertain (phase %s)", response.GetPhase())
	}
	return nil
}

func (r *VMMRouter) OperatorRestartGeneration(ctx context.Context, nodeID, instance string) (string, error) {
	client, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return "", err
	}
	restart, ok := client.(operatorRestartNode)
	if !ok {
		return "", fmt.Errorf("vmmd client lacks durable operator restart support")
	}
	return restart.OperatorRestartGeneration(ctx, instance)
}

func (r *VMMRouter) ExecuteOperatorRestartDestroy(ctx context.Context, nodeID, intentID, token string) error {
	client, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return err
	}
	restart, ok := client.(operatorRestartNode)
	if !ok {
		return fmt.Errorf("vmmd client lacks durable operator restart support")
	}
	return restart.ExecuteOperatorRestartDestroy(ctx, intentID, token)
}
