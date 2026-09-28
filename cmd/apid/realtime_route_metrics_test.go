package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestManagedRealtimeChannelRouteMetricsRegisterAndRecord(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := newManagedRealtimeChannelRouteMetrics(registry, "apid")
	metrics.publish("targeted", 3)
	metrics.rebuildCheck("started")
	metrics.reconcilePass("complete", 1.25)
	metrics.nodeSnapshot("success")

	// Reusing the registry should reuse the existing collectors rather than
	// failing registration or splitting observations across duplicate metrics.
	reused := newManagedRealtimeChannelRouteMetrics(registry, "apid")
	reused.publish("targeted", 2)

	wantFamilies := []string{
		"apid_realtime_channel_route_publish_decisions_total",
		"apid_realtime_channel_route_publish_recipients",
		"apid_realtime_channel_route_rebuild_checks_total",
		"apid_realtime_channel_route_reconcile_passes_total",
		"apid_realtime_channel_route_reconcile_duration_seconds",
		"apid_realtime_channel_route_node_snapshots_total",
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather route metrics: %v", err)
	}
	gotFamilies := make(map[string]*dto.MetricFamily, len(families))
	for _, family := range families {
		gotFamilies[family.GetName()] = family
	}
	for _, name := range wantFamilies {
		if gotFamilies[name] == nil {
			t.Errorf("metric family %q was not registered", name)
		}
	}

	decision := managedRealtimeRouteMetricWithLabel(t, gotFamilies[wantFamilies[0]], "decision", "targeted")
	if got := decision.GetCounter().GetValue(); got != 2 {
		t.Errorf("targeted decisions = %v, want 2", got)
	}
	recipients := managedRealtimeRouteMetricWithLabel(t, gotFamilies[wantFamilies[1]], "decision", "targeted")
	if got := recipients.GetHistogram().GetSampleCount(); got != 2 {
		t.Errorf("targeted recipient observations = %d, want 2", got)
	}
	if got := recipients.GetHistogram().GetSampleSum(); got != 5 {
		t.Errorf("targeted recipient sum = %v, want 5", got)
	}
	if got := managedRealtimeRouteMetricWithLabel(t, gotFamilies[wantFamilies[2]], "outcome", "started").GetCounter().GetValue(); got != 1 {
		t.Errorf("started rebuild checks = %v, want 1", got)
	}
	if got := managedRealtimeRouteMetricWithLabel(t, gotFamilies[wantFamilies[3]], "outcome", "complete").GetCounter().GetValue(); got != 1 {
		t.Errorf("complete reconciliation passes = %v, want 1", got)
	}
	if got := managedRealtimeRouteMetricWithLabel(t, gotFamilies[wantFamilies[5]], "outcome", "success").GetCounter().GetValue(); got != 1 {
		t.Errorf("successful node snapshots = %v, want 1", got)
	}
	if metrics.reconcileDuration == nil || reused.reconcileDuration == nil {
		t.Fatal("reconciliation duration histogram was not registered")
	}
}

func TestLeasedRealtimeOwnerPublishRecipientsRecordsRoutingDecisionMetrics(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	activeNodes, err := store.ActiveComputeNodes(ctx)
	if err != nil {
		t.Fatalf("list active nodes: %v", err)
	}
	if len(activeNodes) == 0 {
		t.Fatal("NewMemStore should provide at least one active local node")
	}
	generation, err := store.CurrentManagedRealtimeChannelRouteGeneration(ctx)
	if err != nil {
		t.Fatalf("read route generation: %v", err)
	}
	targetID := activeNodes[0].ID
	for _, node := range activeNodes {
		var routes []state.ManagedRealtimeChannelRoute
		if node.ID == targetID {
			routes = []state.ManagedRealtimeChannelRoute{{
				EndpointID: "endpoint",
				Channel:    "updates",
				NodeID:     node.ID,
			}}
		}
		if err := store.ReplaceManagedRealtimeChannelRoutes(ctx, node.ID, generation, routes); err != nil {
			t.Fatalf("record %s route snapshot: %v", node.ID, err)
		}
	}

	registry := prometheus.NewRegistry()
	owner := newLeasedRealtimeOwner(store, store, "", nil, nil)
	owner.channelRoutingEnabled = true
	owner.channelRoutes = store
	owner.channelRouteMetrics = newManagedRealtimeChannelRouteMetrics(registry, "apid")

	if got := owner.publishRecipients(ctx, "endpoint", "updates", activeNodes); len(got) != 1 || got[0].ID != targetID {
		t.Fatalf("targeted recipients = %v, want only %s", got, targetID)
	}
	if got := owner.publishRecipients(ctx, "endpoint", "empty", activeNodes); len(got) != 0 {
		t.Fatalf("empty-channel recipients = %v, want none", got)
	}

	unreadyNode, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "unready-route-snapshot", Active: true})
	if err != nil {
		t.Fatalf("create unready active node: %v", err)
	}
	activeNodes, err = store.ActiveComputeNodes(ctx)
	if err != nil {
		t.Fatalf("list active nodes with unready node: %v", err)
	}
	got := owner.publishRecipients(ctx, "endpoint", "updates", activeNodes)
	if len(got) != 2 || got[0].ID != targetID || got[1].ID != unreadyNode.ID {
		t.Fatalf("unready-fallback recipients = %v, want target %s plus unready node %s", got, targetID, unreadyNode.ID)
	}

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather routing decision metrics: %v", err)
	}
	var decisions, recipients *dto.MetricFamily
	for _, family := range families {
		switch family.GetName() {
		case "apid_realtime_channel_route_publish_decisions_total":
			decisions = family
		case "apid_realtime_channel_route_publish_recipients":
			recipients = family
		}
	}
	for label, want := range map[string]float64{
		"targeted":         1,
		"no_subscribers":   1,
		"unready_fallback": 1,
	} {
		metric := managedRealtimeRouteMetricWithLabel(t, decisions, "decision", label)
		if got := metric.GetCounter().GetValue(); got != want {
			t.Errorf("%s decisions = %v, want %v", label, got, want)
		}
		metric = managedRealtimeRouteMetricWithLabel(t, recipients, "decision", label)
		if got := metric.GetHistogram().GetSampleCount(); got != 1 {
			t.Errorf("%s recipient observations = %d, want 1", label, got)
		}
	}
}

func TestReconcileManagedRealtimeChannelRoutesRecordsPassAndSnapshotMetrics(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	activeNodes, err := store.ActiveComputeNodes(ctx)
	if err != nil {
		t.Fatalf("list active nodes: %v", err)
	}
	if len(activeNodes) == 0 {
		t.Fatal("NewMemStore should provide at least one active local node")
	}
	registry := prometheus.NewRegistry()
	owner := newLeasedRealtimeOwner(store, store, activeNodes[0].ID, &fakeRealtimeNode{}, nil)
	owner.channelRoutingEnabled = true
	owner.channelRoutes = store
	owner.channelRouteMetrics = newManagedRealtimeChannelRouteMetrics(registry, "apid")
	srv := newServer(store, discardLogger(), "gregale.dev", noopNotifier{})

	if err := srv.reconcileManagedRealtimeChannelRoutes(ctx, owner); err != nil {
		t.Fatalf("reconcile route snapshot: %v", err)
	}

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather reconciliation metrics: %v", err)
	}
	familyByName := make(map[string]*dto.MetricFamily, len(families))
	for _, family := range families {
		familyByName[family.GetName()] = family
	}
	if got := managedRealtimeRouteMetricWithLabel(t, familyByName["apid_realtime_channel_route_reconcile_passes_total"], "outcome", "complete").GetCounter().GetValue(); got != 1 {
		t.Errorf("complete reconciliation passes = %v, want 1", got)
	}
	if got := managedRealtimeRouteMetricWithLabel(t, familyByName["apid_realtime_channel_route_node_snapshots_total"], "outcome", "success").GetCounter().GetValue(); got != float64(len(activeNodes)) {
		t.Errorf("successful node snapshots = %v, want %d", got, len(activeNodes))
	}
	duration := familyByName["apid_realtime_channel_route_reconcile_duration_seconds"]
	if duration == nil || len(duration.GetMetric()) != 1 || duration.GetMetric()[0].GetHistogram().GetSampleCount() != 1 {
		t.Fatalf("reconciliation duration metric = %v, want one observation", duration)
	}
}

func managedRealtimeRouteMetricWithLabel(t *testing.T, family *dto.MetricFamily, labelName, labelValue string) *dto.Metric {
	t.Helper()
	if family == nil {
		t.Fatalf("metric family for label %s=%q is nil", labelName, labelValue)
	}
	for _, metric := range family.GetMetric() {
		for _, label := range metric.GetLabel() {
			if label.GetName() == labelName && label.GetValue() == labelValue {
				return metric
			}
		}
	}
	t.Fatalf("metric %s with label %s=%q was not found", family.GetName(), labelName, labelValue)
	return nil
}
