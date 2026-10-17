package main

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/meter"
)

type gatedSuspensionCommands struct {
	client *api.InternalMailSuspensionClient
	on     func() bool
}

func configureSuspensionCommands(ctx context.Context, getenv func(string) string) (meter.SuspensionCommands, error) {
	if getenv(internalmail.AccountSuspendedOutboxEnableEnv) != "1" {
		return nil, nil
	}
	on := func() bool {
		return internalmail.AccountSuspendedOutboxEnabled(getenv) && internalmail.ProducersEnabled(getenv) && internalmail.SchedulingEnabled(getenv)
	}
	if !on() {
		return nil, errors.New("meterd: suspension mail requires master, producer, scheduling and outbox gates")
	}
	token, err := api.LoadInternalMailSuspensionToken(getenv(api.InternalMailSuspensionTokenPathEnv))
	if err != nil {
		return nil, err
	}
	client, err := api.NewInternalMailSuspensionClient(safeDeployInternalBaseURL(getenv), token)
	if err != nil {
		return nil, err
	}
	if err := client.Probe(ctx); err != nil {
		return nil, err
	}
	return &gatedSuspensionCommands{client: client, on: on}, nil
}

func (c *gatedSuspensionCommands) Suspend(ctx context.Context, id string, at time.Time) (api.InternalMailSuspensionResponse, error) {
	if !c.on() {
		return api.InternalMailSuspensionResponse{}, errors.New("suspension command disabled")
	}
	result, err := c.client.Suspend(ctx, id, at)
	if !c.on() {
		return api.InternalMailSuspensionResponse{}, errors.New("suspension command disabled")
	}
	return result, err
}

func (c *gatedSuspensionCommands) Complete(ctx context.Context, id, intentID, leaseID string) error {
	if !c.on() {
		return errors.New("suspension command disabled")
	}
	return c.client.Complete(ctx, id, intentID, leaseID)
}
