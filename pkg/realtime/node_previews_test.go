package realtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type nodePreviewReaders struct{}

func (nodePreviewReaders) ReadChannelHistory(context.Context, string, string, int64, int) (state.ManagedRealtimeChannelHistory, error) {
	return state.ManagedRealtimeChannelHistory{}, nil
}
func (nodePreviewReaders) ReadSubscriptionTicket(context.Context, string, string) (state.RealtimeSubscriptionTicket, error) {
	return state.RealtimeSubscriptionTicket{}, nil
}
func (nodePreviewReaders) ReadRealtimeAccess(context.Context, string, string) (state.ManagedRealtimeAccess, error) {
	return state.ManagedRealtimeAccess{}, nil
}

// The endpoint inventory tells apid which previews this node serves
// (ADR 20261021080640, amendment of 2026-11-09): a switch counts only with
// what the feature reads, and tickets need resume.
func TestEndpointInventoryReportsNodePreviews(t *testing.T) {
	readers := nodePreviewReaders{}
	for _, tc := range []struct {
		name string
		cfg  Config
		want []string
	}{
		{"none", Config{}, []string{}},
		{"switches without readers", Config{ResumePreview: true, TicketPreview: true, AccessPreview: true}, []string{}},
		{"tickets without resume", Config{TicketPreview: true, TicketReader: readers, OccupancyPreview: true}, []string{NodePreviewOccupancy}},
		{"all", Config{ResumePreview: true, HistoryReader: readers, TicketPreview: true, TicketReader: readers, AccessPreview: true, AccessReader: readers, OccupancyPreview: true},
			[]string{NodePreviewAccess, NodePreviewOccupancy, NodePreviewResume, NodePreviewTickets}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewManager(tc.cfg, nil)
			defer m.Close()
			server := httptest.NewServer(m.HTTPHandler())
			defer server.Close()
			client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
			_, previews, err := client.EndpointsWithPreviews(context.Background())
			if err != nil || previews == nil || !slices.Equal(previews, tc.want) {
				t.Fatalf("previews = %v (%v), want %v", previews, err, tc.want)
			}
		})
	}
}

// A node that predates the header reports nothing, which is not "none".
func TestEndpointInventoryWithoutPreviewHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`["ep"]`))
	}))
	defer server.Close()
	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	ids, previews, err := client.EndpointsWithPreviews(context.Background())
	if err != nil || len(ids) != 1 || previews != nil {
		t.Fatalf("ids %v previews %v err %v; want the IDs and nil previews", ids, previews, err)
	}
}
