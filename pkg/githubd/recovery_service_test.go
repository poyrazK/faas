package githubd

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/githubdgrpc"
)

type recoveryDeliveryStoreFake struct {
	items   []WebhookDeliveryRecord
	retried string
}

func (f *recoveryDeliveryStoreFake) ListWebhookDeliveries(context.Context, string, int) ([]WebhookDeliveryRecord, error) {
	return f.items, nil
}

func (f *recoveryDeliveryStoreFake) RetryWebhookDelivery(_ context.Context, id string) (bool, error) {
	f.retried = id
	return true, nil
}

type recoveryCheckStoreFake struct {
	items   []CheckUpdateRecord
	retried string
}

func (f *recoveryCheckStoreFake) ListCheckUpdates(context.Context, string, int) ([]CheckUpdateRecord, error) {
	return f.items, nil
}

func (f *recoveryCheckStoreFake) RetryCheckUpdate(_ context.Context, id string) (bool, error) {
	f.retried = id
	return true, nil
}

func TestRecoveryServiceProjectsAndRetriesQueues(t *testing.T) {
	now := time.Now().UTC()
	deliveries := &recoveryDeliveryStoreFake{items: []WebhookDeliveryRecord{{DeliveryID: "delivery-1", Status: "dead", UpdatedAt: now}}}
	checks := &recoveryCheckStoreFake{items: []CheckUpdateRecord{{DeploymentID: "deployment-1", Status: "dead", UpdatedAt: now}}}
	svc := NewRecoveryService(githubdgrpc.UnimplementedService{}, deliveries, checks)
	items, err := svc.ListRecoveryQueueItems(context.Background(), "dead", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(items.Deliveries) != 1 || items.Deliveries[0].DeliveryID != "delivery-1" || len(items.CheckUpdates) != 1 {
		t.Fatalf("items = %+v", items)
	}
	if retried, err := svc.RetryWebhookDelivery(context.Background(), "delivery-1"); err != nil || !retried {
		t.Fatalf("retry delivery = %t, %v", retried, err)
	}
	if retried, err := svc.RetryCheckUpdate(context.Background(), "deployment-1"); err != nil || !retried {
		t.Fatalf("retry check = %t, %v", retried, err)
	}
	if deliveries.retried != "delivery-1" || checks.retried != "deployment-1" {
		t.Fatalf("retry targets = %q, %q", deliveries.retried, checks.retried)
	}
}
