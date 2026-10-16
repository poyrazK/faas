package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type testScenarioConsumerLookup interface {
	LookupScenarioTestConsumer(context.Context, string, string, api.ScenarioTestConsumerRequest) (api.ScenarioTestConsumerReceipt, error)
}
type testScenarioConsumerEnsure interface {
	EnsureScenarioTestConsumer(context.Context, string, string, api.ScenarioTestConsumerRequest) (api.ScenarioTestConsumerReceipt, error)
}

type testScenarioConsumerRevoke interface {
	RevokeScenarioTestConsumer(context.Context, string, string, api.ScenarioTestConsumerRequest, string) (api.ScenarioTestConsumerReceipt, error)
}

func verifiedTestConsumerReceipt(receipt api.ScenarioTestConsumerReceipt, run, name string, req api.ScenarioTestConsumerRequest, expected string) (api.APIConsumerResponse, error) {
	intent, err := api.ScenarioTestConsumerIntent(run, name, req)
	if err != nil || intent != expected || (receipt.SchemaVersion != 1 && receipt.SchemaVersion != 2) || receipt.IntentSHA256 != intent ||
		!receipt.ExpiresAt.Equal(req.ExpiresAt.UTC().Truncate(time.Microsecond)) || receipt.Consumer == nil || receipt.Cancelled {
		return api.APIConsumerResponse{}, errors.New("consumer creation receipt is missing or differs from intent; inspect manually")
	}
	c := *receipt.Consumer
	id, err := uuid.Parse(c.ID)
	if err != nil || id.String() != c.ID || c.AppID != req.AppID || c.ExternalRef != "gregale-test-"+run+"-"+name || c.Name != "Scenario "+name || c.ManagedByPlatformTenant ||
		(c.Status != "active" && c.Status != "revoked") {
		return api.APIConsumerResponse{}, errors.New("consumer creation receipt identity changed")
	}
	return c, nil
}

func readTestCampaignConsumer(ctx context.Context, client testCampaignFaultClient, run, name string, c testCampaignFaultConsumer) (api.APIConsumerResponse, error) {
	if c.CreationIntentSHA256 == "" {
		return client.GetAPIConsumer(ctx, c.AppSlug, c.ID)
	}
	lookup, ok := client.(testScenarioConsumerLookup)
	if !ok {
		return api.APIConsumerResponse{}, errors.New("consumer intent lookup capability unavailable")
	}
	req := api.ScenarioTestConsumerRequest{AppID: c.AppID, ExpiresAt: c.ExpiresAt}
	receipt, err := lookup.LookupScenarioTestConsumer(ctx, run, name, req)
	if err != nil {
		// Unsupported routes (including 404) cannot become consumer absence.
		return api.APIConsumerResponse{}, fmt.Errorf("consumer intent lookup failed: %v", err)
	}
	if validTestConsumerCancellation(receipt, c) {
		return api.APIConsumerResponse{}, errTestConsumerCancelled
	}
	if receipt.SchemaVersion == 2 && !receipt.Cancelled && receipt.Consumer == nil && receipt.IntentSHA256 == c.CreationIntentSHA256 && receipt.ExpiresAt.Equal(c.ExpiresAt.UTC().Truncate(time.Microsecond)) && c.ID == "" {
		return api.APIConsumerResponse{}, errTestConsumerPending
	}
	live, err := verifiedTestConsumerReceipt(receipt, run, name, req, c.CreationIntentSHA256)
	if err != nil {
		// A saved immutable ID permits a separate absence check. An unknown
		// ID with no receipt remains uncertain.
		if c.ID != "" && (receipt.SchemaVersion == 1 || receipt.SchemaVersion == 2) && receipt.IntentSHA256 == c.CreationIntentSHA256 && receipt.Consumer == nil && receipt.ExpiresAt.Equal(c.ExpiresAt.UTC().Truncate(time.Microsecond)) {
			_, missing := client.GetAPIConsumer(ctx, c.AppSlug, c.ID)
			if isNotFound(missing) {
				return api.APIConsumerResponse{}, missing
			}
		}
		return api.APIConsumerResponse{}, err
	}
	if c.ID != "" && c.ID != live.ID {
		return api.APIConsumerResponse{}, errors.New("consumer receipt returned another saved identity")
	}
	return live, nil
}

func revokeTestCampaignConsumer(ctx context.Context, client testCampaignFaultClient, run, name string, c testCampaignFaultConsumer) (api.APIConsumerResponse, error) {
	if c.CreationIntentSHA256 == "" {
		return client.RevokeAPIConsumer(ctx, c.AppSlug, c.ID)
	}
	revoker, ok := client.(testScenarioConsumerRevoke)
	if !ok {
		return api.APIConsumerResponse{}, errors.New("consumer intent revocation capability unavailable")
	}
	req := api.ScenarioTestConsumerRequest{AppID: c.AppID, ExpiresAt: c.ExpiresAt}
	receipt, err := revoker.RevokeScenarioTestConsumer(ctx, run, name, req, c.ID)
	if err != nil {
		return api.APIConsumerResponse{}, err
	}
	live, err := verifiedTestConsumerReceipt(receipt, run, name, req, c.CreationIntentSHA256)
	if err != nil {
		return api.APIConsumerResponse{}, err
	}
	if live.ID != c.ID {
		return api.APIConsumerResponse{}, errors.New("consumer revocation returned another identity")
	}
	return live, nil
}

var errTestConsumerCancelled = errors.New("consumer creation cancelled")
var errTestConsumerPending = errors.New("consumer creation unresolved")

type testScenarioConsumerCancel interface {
	CancelScenarioTestConsumer(context.Context, string, string, api.ScenarioTestConsumerRequest) (api.ScenarioTestConsumerReceipt, error)
}

func validTestConsumerCancellation(r api.ScenarioTestConsumerReceipt, c testCampaignFaultConsumer) bool {
	return r.SchemaVersion == 2 && r.Cancelled && r.IntentSHA256 == c.CreationIntentSHA256 && r.ExpiresAt.Equal(c.ExpiresAt.UTC().Truncate(time.Microsecond)) && r.Consumer == nil
}

func cancelTestCampaignConsumer(ctx context.Context, client testCampaignFaultClient, run, name string, c testCampaignFaultConsumer) error {
	cancel, ok := client.(testScenarioConsumerCancel)
	if !ok {
		return errors.New("consumer intent cancellation capability unavailable")
	}
	req := api.ScenarioTestConsumerRequest{AppID: c.AppID, ExpiresAt: c.ExpiresAt}
	receipt, err := cancel.CancelScenarioTestConsumer(ctx, run, name, req)
	if err != nil {
		return err
	}
	// When creation won the race, require the full receipt and revoked status.
	if receipt.Consumer != nil {
		if !receipt.Cancelled {
			return errors.New("consumer cancellation fence missing")
		}
		consumer := *receipt.Consumer
		receipt.Cancelled = false
		if _, err := verifiedTestConsumerReceipt(receipt, run, name, req, c.CreationIntentSHA256); err != nil || consumer.Status != "revoked" {
			return errors.New("cancelled consumer revocation unconfirmed")
		}
		receipt.Consumer = nil
		receipt.Cancelled = true
	}
	if !validTestConsumerCancellation(receipt, c) {
		return errors.New("consumer cancellation was not confirmed")
	}
	return nil
}
