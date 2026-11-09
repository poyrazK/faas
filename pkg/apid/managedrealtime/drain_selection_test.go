package managedrealtime

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// A drain must say which connections or how many. Before, a body with only a
// reason closed every connection up to the default limit, and all=true
// silently ignored a limit.
func TestManagedRealtimeDrainSelection(t *testing.T) {
	tests := []struct {
		name    string
		request api.ManagedRealtimeDrainRequest
		wantErr bool
	}{
		{name: "reason only", request: api.ManagedRealtimeDrainRequest{Reason: "r"}, wantErr: true},
		{name: "reason only dry run", request: api.ManagedRealtimeDrainRequest{Reason: "r", DryRun: true}, wantErr: true},
		{name: "explicit limit", request: api.ManagedRealtimeDrainRequest{Reason: "r", Limit: 10}},
		{name: "channel", request: api.ManagedRealtimeDrainRequest{Reason: "r", Channel: "room"}},
		{name: "principal", request: api.ManagedRealtimeDrainRequest{Reason: "r", Principal: "user"}},
		{name: "connection ids", request: api.ManagedRealtimeDrainRequest{Reason: "r", ConnectionIDs: []string{"rt_a"}}},
		{name: "all", request: api.ManagedRealtimeDrainRequest{Reason: "r", All: true}},
		{name: "all with a filter", request: api.ManagedRealtimeDrainRequest{Reason: "r", All: true, Channel: "room"}},
		{name: "all with limit", request: api.ManagedRealtimeDrainRequest{Reason: "r", All: true, Limit: 1}, wantErr: true},
		{name: "all with connection ids", request: api.ManagedRealtimeDrainRequest{Reason: "r", All: true, ConnectionIDs: []string{"rt_a"}}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := managedRealtimeDrainSelectionProblem(tt.request); (got != nil) != tt.wantErr {
				t.Fatalf("problem = %+v, want error %v", got, tt.wantErr)
			}
		})
	}
}
