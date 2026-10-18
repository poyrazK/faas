package main

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/meter"
)

type gatedNonpaymentDeletionCommands struct {
	client *api.InternalMailNonpaymentDeletionClient
	on     func() bool
}

func configureNonpaymentDeletionCommands(ctx context.Context, getenv func(string) string) (meter.NonpaymentDeletionCommands, error) {
	if getenv(internalmail.NonpaymentDeletionOutboxEnableEnv) != "1" {
		return nil, nil
	}
	on := func() bool {
		return internalmail.NonpaymentDeletionOutboxEnabled(getenv) && internalmail.ProducersEnabled(getenv) && internalmail.SchedulingEnabled(getenv)
	}
	if !on() {
		return nil, errors.New("meterd: non-payment deletion mail requires master, producer, scheduling and outbox gates")
	}
	token, err := api.LoadInternalMailNonpaymentDeletionToken(getenv(api.InternalMailNonpaymentDeletionTokenPathEnv))
	if err != nil {
		return nil, err
	}
	client, err := api.NewInternalMailNonpaymentDeletionClient(safeDeployInternalBaseURL(getenv), token)
	if err != nil {
		return nil, err
	}
	if err := client.Probe(ctx); err != nil {
		return nil, err
	}
	return &gatedNonpaymentDeletionCommands{client: client, on: on}, nil
}

func (c *gatedNonpaymentDeletionCommands) Schedule(ctx context.Context, id string, at time.Time) (api.InternalMailNonpaymentDeletionResponse, error) {
	if !c.on() {
		return api.InternalMailNonpaymentDeletionResponse{}, errors.New("non-payment deletion command disabled")
	}
	result, err := c.client.Schedule(ctx, id, at)
	if !c.on() {
		return api.InternalMailNonpaymentDeletionResponse{}, errors.New("non-payment deletion command disabled")
	}
	return result, err
}

func (c *gatedNonpaymentDeletionCommands) Complete(ctx context.Context, id, intentID, leaseID string) error {
	if !c.on() {
		return errors.New("non-payment deletion command disabled")
	}
	return c.client.Complete(ctx, id, intentID, leaseID)
}
