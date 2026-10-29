package main

// manifest_realtime_triggers.go — `kind: realtime` triggers in gregale.yaml
// (ADR-1322). A deploy reconciles the deployed app's realtime triggers by
// slug, inside the same rollback transaction as its cron triggers.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

// manifestRealtimeTriggerClient is the API surface realtime trigger
// reconciliation needs.
type manifestRealtimeTriggerClient interface {
	GetApp(ctx context.Context, slug string) (api.AppResponse, error)
	GetTriggers(ctx context.Context, appID string, kind api.TriggerKind) ([]api.Trigger, error)
	PostTriggers(ctx context.Context, req api.CreateTriggerRequest) (api.Trigger, error)
	PatchTriggersId(ctx context.Context, id string, req api.UpdateTriggerRequest) (api.Trigger, error)
	DeleteTriggersId(ctx context.Context, id string) error
}

type desiredRealtimeTrigger struct {
	slug      string
	enabled   bool
	cfg       api.RealtimeTriggerConfig
	delivery  string
	batchSize int
}

// manifestRealtimeTriggers returns the realtime triggers declared for slug
// and whether the manifest manages them. As for crons, declaring one opts
// the app into replacement semantics and an explicit `triggers: []` clears it.
func manifestRealtimeTriggers(m *gregalemanifest.Manifest, slug string, endpoints map[string]string) ([]desiredRealtimeTrigger, bool, error) {
	manage := m.Triggers != nil && len(m.Triggers) == 0
	var out []desiredRealtimeTrigger
	for _, t := range m.Triggers {
		if t.Kind != gregalemanifest.TriggerKindRealtime || t.App != slug {
			continue
		}
		manage = true
		config := t.Config
		// An endpoint named by slug is one this deploy reconciled
		// (ADR-20261019102657).
		if ref, ok := t.Config["endpoint"].(string); ok {
			id, found := endpoints[ref]
			if !found {
				return nil, false, fmt.Errorf("realtime trigger %q: endpoint %q is not declared for %s", t.Slug, ref, slug)
			}
			config = maps.Clone(t.Config)
			delete(config, "endpoint")
			config["endpoint_id"] = id
		}
		raw, err := json.Marshal(config)
		if err != nil {
			return nil, false, fmt.Errorf("realtime trigger %q config: %w", t.Slug, err)
		}
		var cfg api.RealtimeTriggerConfig
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, false, fmt.Errorf("realtime trigger %q config: %w", t.Slug, err)
		}
		out = append(out, desiredRealtimeTrigger{slug: t.Slug, enabled: t.IsEnabled(), cfg: cfg,
			delivery: realtimeTriggerDelivery(t.Delivery), batchSize: realtimeTriggerBatchSize(t.Delivery, t.BatchSizeMax)})
	}
	return out, manage, nil
}

// reconcileManifestRealtimeTriggers creates, updates and removes slug's
// realtime triggers to match the manifest, recording an undo step for each
// change in txn. The binding (endpoint, channel, group) is immutable, so a
// changed binding is a remove and an add; deleting a trigger keeps its group's
// messages, so a rollback can recreate it.
func reconcileManifestRealtimeTriggers(ctx context.Context, client any, slug string, m *gregalemanifest.Manifest, txn *manifestCronTransaction, endpoints map[string]string) error {
	desired, manage, err := manifestRealtimeTriggers(m, slug, endpoints)
	if err != nil || !manage {
		return err
	}
	rt, ok := client.(manifestRealtimeTriggerClient)
	if !ok {
		if len(desired) == 0 {
			return nil // nothing declared; a client without the trigger API has nothing to clear
		}
		return errors.New("realtime trigger reconciliation is unavailable")
	}
	app, err := rt.GetApp(ctx, slug)
	if err != nil {
		return fmt.Errorf("read app %s: %w", slug, err)
	}
	existing, err := rt.GetTriggers(ctx, app.ID, api.TriggerKindRealtime)
	if err != nil {
		return fmt.Errorf("list realtime triggers: %w", err)
	}
	current := make(map[string]api.Trigger, len(existing))
	for _, t := range existing {
		current[t.Slug] = t
	}
	r := realtimeTriggerReconcile{client: rt, appID: app.ID, txn: txn}
	for _, want := range desired {
		have, found := current[want.slug]
		delete(current, want.slug)
		if err := r.apply(ctx, want, have, found); err != nil {
			return err
		}
	}
	for _, slug := range slices.Sorted(maps.Keys(current)) {
		if err := r.remove(ctx, current[slug]); err != nil {
			return err
		}
	}
	return nil
}

type realtimeTriggerReconcile struct {
	client manifestRealtimeTriggerClient
	appID  string
	txn    *manifestCronTransaction
}

func (r realtimeTriggerReconcile) apply(ctx context.Context, want desiredRealtimeTrigger, have api.Trigger, found bool) error {
	if !found {
		return r.create(ctx, want)
	}
	var haveCfg api.RealtimeTriggerConfig
	_ = json.Unmarshal(have.Config, &haveCfg)
	if haveCfg.EndpointID != want.cfg.EndpointID || haveCfg.Channel != want.cfg.Channel || haveCfg.Group != want.cfg.Group {
		if err := r.remove(ctx, have); err != nil {
			return err
		}
		return r.create(ctx, want)
	}
	before := desiredRealtimeTrigger{slug: have.Slug, enabled: have.Enabled, cfg: haveCfg,
		delivery: realtimeTriggerDelivery(have.Delivery), batchSize: max(have.BatchSizeMax, 1)}
	if haveCfg.Concurrency() == want.cfg.Concurrency() && have.Enabled == want.enabled &&
		before.delivery == want.delivery && before.batchSize == want.batchSize {
		return nil
	}
	if err := r.patch(ctx, have.ID, want); err != nil {
		return fmt.Errorf("update realtime trigger %q: %w", want.slug, err)
	}
	r.txn.steps = append(r.txn.steps, manifestCronRollbackStep{
		description: "restore realtime trigger " + want.slug,
		undo:        func(ctx context.Context) error { return r.patch(ctx, have.ID, before) },
	})
	return nil
}

// patch applies concurrency, enablement, delivery and batch size together:
// the server only accepts a batch size above one with batch delivery.
func (r realtimeTriggerReconcile) patch(ctx context.Context, id string, want desiredRealtimeTrigger) error {
	raw, _ := json.Marshal(map[string]int{"max_concurrency": want.cfg.MaxConcurrency})
	enabled, delivery, batchSize := want.enabled, want.delivery, want.batchSize
	_, err := r.client.PatchTriggersId(ctx, id, api.UpdateTriggerRequest{Config: raw, Enabled: &enabled, Delivery: &delivery, BatchSizeMax: &batchSize})
	return err
}

// realtimeTriggerDelivery defaults an omitted manifest delivery to record.
func realtimeTriggerDelivery(delivery string) string {
	if delivery == "" {
		return api.TriggerDeliveryRecord
	}
	return delivery
}

// realtimeTriggerBatchSize mirrors the server: one message with record
// delivery, otherwise the declared size or ten.
func realtimeTriggerBatchSize(delivery string, declared int) int {
	if realtimeTriggerDelivery(delivery) != api.TriggerDeliveryBatch {
		return 1
	}
	if declared > 0 {
		return declared
	}
	return api.RealtimeTriggerDefaultBatchSize
}

func (r realtimeTriggerReconcile) create(ctx context.Context, want desiredRealtimeTrigger) error {
	raw, _ := json.Marshal(want.cfg)
	enabled := want.enabled
	delivery, batchSize := want.delivery, want.batchSize
	created, err := r.client.PostTriggers(ctx, api.CreateTriggerRequest{AppID: r.appID, Kind: api.TriggerKindRealtime,
		Slug: want.slug, Enabled: &enabled, Config: raw, Delivery: &delivery, BatchSizeMax: &batchSize})
	if err != nil {
		return fmt.Errorf("create realtime trigger %q: %w", want.slug, err)
	}
	r.txn.steps = append(r.txn.steps, manifestCronRollbackStep{
		description: "remove realtime trigger " + want.slug,
		undo:        func(ctx context.Context) error { return r.client.DeleteTriggersId(ctx, created.ID) },
	})
	return nil
}

func (r realtimeTriggerReconcile) remove(ctx context.Context, have api.Trigger) error {
	if err := r.client.DeleteTriggersId(ctx, have.ID); err != nil {
		return fmt.Errorf("remove realtime trigger %q: %w", have.Slug, err)
	}
	r.txn.steps = append(r.txn.steps, manifestCronRollbackStep{
		description: "recreate realtime trigger " + have.Slug,
		undo: func(ctx context.Context) error {
			enabled, delivery, batchSize := have.Enabled, realtimeTriggerDelivery(have.Delivery), max(have.BatchSizeMax, 1)
			_, err := r.client.PostTriggers(ctx, api.CreateTriggerRequest{AppID: r.appID, Kind: api.TriggerKindRealtime,
				Slug: have.Slug, Enabled: &enabled, Config: have.Config, Delivery: &delivery, BatchSizeMax: &batchSize})
			return err
		},
	})
	return nil
}
