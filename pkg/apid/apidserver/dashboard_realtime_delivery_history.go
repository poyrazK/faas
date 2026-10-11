package apidserver

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"net/url"
)

func (s *server) loadRealtimeInspectorDeliveryHistory(r *http.Request, data *dashboard.RealtimeInspectorData) {
	if data.DeliverySequence == "" {
		return
	}
	store, ok := s.store.(state.RealtimeConsumerGroupDeliveryHistoryStore)
	if !ok {
		data.DeliveryHistoryError = "Delivery history is unavailable."
		return
	}
	after := "0"
	if values, exists := r.URL.Query()["delivery_after_id"]; exists {
		if len(values) != 1 {
			data.DeliveryHistoryError = "Choose one history cursor."
			return
		}
		after = values[0]
	}
	history, err := store.ReadRealtimeConsumerGroupDeliveryHistory(r.Context(), data.EndpointID, data.Channel, data.GroupName, data.DeliverySequence, after, api.RealtimeInspectorSampleMax)
	if err != nil {
		data.DeliveryHistoryError = "Delivery sequence or history cursor is invalid, or history is unavailable."
		return
	}
	data.DeliveryHistory = &history
	if history.HasMore {
		values := url.Values{"endpoint_id": {data.EndpointID}, "channel": {data.Channel}, "group": {data.GroupName}, "after": {data.After}, "delivery_sequence": {data.DeliverySequence}, "delivery_after_id": {history.NextAfterID}}
		data.DeliveryHistoryNextURL = "/dashboard/apps/" + url.PathEscape(data.Slug) + "/realtime?" + values.Encode()
	}
}
