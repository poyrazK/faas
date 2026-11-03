package api_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPrivateServiceHTTPSTransportProblem(t *testing.T) {
	for _, tc := range []struct {
		transport api.ServiceBindingTransport
		enabled   bool
		refused   bool
	}{
		{api.ServiceBindingTransportHTTPS, false, true},
		{api.ServiceBindingTransportHTTPS, true, false},
		{api.ServiceBindingTransportHTTP, false, false},
		{"", false, false},
	} {
		problem := api.PrivateServiceHTTPSTransportProblem(tc.transport, tc.enabled)
		if (problem != nil) != tc.refused {
			t.Fatalf("transport %q enabled %t: problem = %v, want refused %t", tc.transport, tc.enabled, problem, tc.refused)
		}
		if problem != nil && !strings.Contains(problem.Detail, "private-service-https") {
			t.Fatalf("problem detail %q does not name the capability", problem.Detail)
		}
	}
}
