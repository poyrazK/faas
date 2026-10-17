package main

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/meter"
)

type gatedQuotaWarningCommands struct {
	client *api.InternalMailQuotaWarningClient
	on     func() bool
}

func configureQuotaWarningCommands(ctx context.Context, getenv func(string) string) (meter.QuotaWarningCommands, error) {
	if getenv(internalmail.QuotaWarningOutboxEnableEnv) != "1" {
		return nil, nil
	}
	on := func() bool {
		return internalmail.QuotaWarningOutboxEnabled(getenv) && internalmail.ProducersEnabled(getenv) && internalmail.SchedulingEnabled(getenv)
	}
	if !on() {
		return nil, errors.New("meterd: quota warning mail requires master, producer, scheduling and outbox gates")
	}
	token, err := api.LoadInternalMailQuotaWarningToken(getenv(api.InternalMailQuotaWarningTokenPathEnv))
	if err != nil {
		return nil, err
	}
	client, err := api.NewInternalMailQuotaWarningClient(safeDeployInternalBaseURL(getenv), token)
	if err != nil {
		return nil, err
	}
	if err := client.Probe(ctx); err != nil {
		return nil, err
	}
	return &gatedQuotaWarningCommands{client: client, on: on}, nil
}

func (c *gatedQuotaWarningCommands) Warn(ctx context.Context, id string, day time.Time, plan api.Plan) (api.InternalMailQuotaWarningResponse, error) {
	if !c.on() {
		return api.InternalMailQuotaWarningResponse{}, errors.New("quota warning command disabled")
	}
	result, err := c.client.Warn(ctx, id, day, plan)
	if !c.on() {
		return api.InternalMailQuotaWarningResponse{}, errors.New("quota warning command disabled")
	}
	return result, err
}

func (c *gatedQuotaWarningCommands) Complete(ctx context.Context, id, intentID, leaseID string) error {
	if !c.on() {
		return errors.New("quota warning command disabled")
	}
	return c.client.Complete(ctx, id, intentID, leaseID)
}
