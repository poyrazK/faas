package main_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

type addBucketCapabilitiesStub struct {
	response api.CapabilitiesResponse
	err      error
}

func (s addBucketCapabilitiesStub) GetCapabilities(context.Context) (api.CapabilitiesResponse, error) {
	return s.response, s.err
}

func TestAddBucketBindingUnavailable(t *testing.T) {
	planDenied := api.CapabilitiesResponse{Capabilities: []api.CapabilityStatus{{Key: "object-storage-compute", Plans: []string{"hobby", "pro", "scale"},
		UnavailableReason: api.CapabilityUnavailablePlan, UnavailableDetail: "This feature is not included in your current plan."}}}
	enabled := api.CapabilitiesResponse{Capabilities: []api.CapabilityStatus{{Key: "object-storage-compute", Enabled: true}}}
	for _, tc := range []struct {
		name    string
		stub    addBucketCapabilitiesStub
		blocked bool
	}{
		{name: "free plan refused before create", stub: addBucketCapabilitiesStub{response: planDenied}, blocked: true},
		{name: "entitled", stub: addBucketCapabilitiesStub{response: enabled}},
		{name: "capability list unreadable", stub: addBucketCapabilitiesStub{err: errors.New("offline")}},
		{name: "older server without the capability", stub: addBucketCapabilitiesStub{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := main.AddBucketBindingUnavailable(context.Background(), tc.stub)
			if (err != nil) != tc.blocked {
				t.Fatalf("err = %v, blocked = %t", err, tc.blocked)
			}
			if tc.blocked && (!strings.Contains(err.Error(), "hobby, pro, scale") || !strings.Contains(err.Error(), "No bucket was created")) {
				t.Fatalf("message = %q", err)
			}
		})
	}
}
