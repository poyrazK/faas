// adr: 935
package durable

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/state"
)

type inspectionDeliveryStore struct {
	state.Store
	row state.AppWebhookDelivery
	err error
}

func (s inspectionDeliveryStore) AppWebhookDeliveryByID(context.Context, string) (state.AppWebhookDelivery, error) {
	return s.row, s.err
}

func TestEntityInspectionScopesSeparateDeliveryObservation(t *testing.T) {
	id := durableentity.ID{AccountID: "account", AppID: "app", EnvironmentID: "environment", TenantID: "tenant", Namespace: "ns", Key: "key"}
	payload, _ := json.Marshal(map[string]any{"entity": id, "message_id": "head"})
	row := state.AppWebhookDelivery{ID: "head", AccountID: id.AccountID, AppID: id.AppID, Payload: payload, Status: state.AppWebhookDeliverySucceeded}
	s := &Service{Host: fakeHost{store: inspectionDeliveryStore{Store: state.NewMemStore(), row: row}}}
	if out := s.durableEntityHeadDelivery(t.Context(), id, "head"); out == nil || out.Status != "succeeded" {
		t.Fatal(out)
	}
	id.TenantID = "other"
	if out := s.durableEntityHeadDelivery(t.Context(), id, "head"); out == nil || out.Status != "unknown" {
		t.Fatal("foreign scope leaked status", out)
	}
	if out := s.durableEntityHeadDelivery(t.Context(), id, ""); out != nil {
		t.Fatal("empty outbox implied transport status", out)
	}
}
