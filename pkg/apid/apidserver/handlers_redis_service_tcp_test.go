// adr: 576 — managed Redis is reached only over private service TCP.
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// TestRedisRefusedWithoutPrivateServiceTCP pins the agent eval of 2026-11-23:
// a release converge dropped the private TCP listener on every node, yet
// managed Redis stayed listed and `redis create` made a cache that no app
// could reach. With private_service_tcp_enabled switched off, the capability
// is unavailable and create is refused before anything is made. The switch
// defaults on.
func TestRedisRefusedWithoutPrivateServiceTCP(t *testing.T) {
	for _, tc := range []struct {
		name    string
		set     *bool
		enabled bool
	}{
		{name: "default", enabled: true},
		{name: "on", set: new(true), enabled: true},
		{name: "off", set: new(false)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := apidserver.SetupSecrets(t, api.PlanPro)
			if tc.set != nil {
				manager := apidserver.NewRuntimeConfigManager(func(string) string { return "" })
				if err := manager.Apply("private_service_tcp_enabled", json.RawMessage(strconv.FormatBool(*tc.set))); err != nil {
					t.Fatal(err)
				}
				(*e.S()).WithRuntimeConfigManager(manager)
			}

			capsRec := e.Do(t, http.MethodGet, "/v1/capabilities", nil, nil)
			var caps api.CapabilitiesResponse
			if err := json.Unmarshal(capsRec.Body.Bytes(), &caps); err != nil {
				t.Fatalf("capabilities %d: %v", capsRec.Code, err)
			}
			i := slices.IndexFunc(caps.Capabilities, func(c api.CapabilityStatus) bool { return c.Key == api.ManagedValkeyCapability })
			if i < 0 || caps.Capabilities[i].Enabled != tc.enabled {
				t.Fatalf("managed-redis capability = %+v; want enabled=%v", caps.Capabilities, tc.enabled)
			}

			rec := e.Do(t, http.MethodPost, "/v1/redis", api.CreateRedisCacheRequest{Name: "sessions", MemoryMB: 256}, nil)
			want := http.StatusCreated
			if !tc.enabled {
				want = http.StatusNotImplemented
			}
			if rec.Code != want {
				t.Fatalf("create = %d %s; want %d", rec.Code, rec.Body.String(), want)
			}
			_, err := (*e.Store()).AppBySlug(t.Context(), "sessions")
			if exists := err == nil; exists != tc.enabled {
				t.Fatalf("cache app exists=%v (err %v); want %v", exists, err, tc.enabled)
			}
		})
	}
}
