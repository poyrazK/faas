package faas

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetCapabilitiesPreservesAvailabilityReasonsAndOlderResponses(t *testing.T) {
	t.Parallel()
	for _, reason := range []string{"", "plan_not_entitled", "runtime_unavailable"} {
		t.Run(reason, func(t *testing.T) {
			explanation := ""
			if reason != "" {
				explanation = fmt.Sprintf(`,"unavailable_reason":%q,"unavailable_detail":"Customer guidance"`, reason)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/capabilities" || r.Header.Get("Authorization") != "Bearer fixture-token" {
					t.Errorf("unexpected capability request: %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"registry_version":1,"plan":"free","capabilities":[{"key":"object-storage","enabled":false%s}]}`, explanation)
			}))
			defer server.Close()
			client, err := NewClient(server.URL, "fixture-token")
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.GetCapabilities(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(response.Capabilities) != 1 {
				t.Fatalf("unexpected response: %+v", response)
			}
			capability := response.Capabilities[0]
			if capability.Enabled || capability.UnavailableReason != reason {
				t.Fatalf("SDK lost availability: %+v", capability)
			}
			if (capability.UnavailableDetail == "") != (reason == "") {
				t.Fatalf("SDK lost optional guidance: %+v", capability)
			}
		})
	}
}

func TestGetCapabilitiesConditionalParking(t *testing.T) {
	for _, tc := range []struct {
		field string
		want  bool
	}{{field: ""}, {field: `,"conditional_parking":false`}, {field: `,"conditional_parking":true`, want: true}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"registry_version":1,"plan":"pro","capabilities":[]%s}`, tc.field)
		}))
		client, err := NewClient(server.URL, "fixture-token")
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.GetCapabilities(SDKTestContext(t))
		server.Close()
		if err != nil || response.ConditionalParking != tc.want {
			t.Fatalf("response=%+v err=%v", response, err)
		}
	}
}
