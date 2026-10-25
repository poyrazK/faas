package gregalemanifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// adr: 20261019102657 — realtime endpoints are declared by slug, and realtime
// triggers may name one instead of an endpoint ID.
func TestValidateRealtimeEndpoints(t *testing.T) {
	valid := func() *Manifest {
		return &Manifest{
			Realtime: &RealtimeConfig{Endpoints: []RealtimeEndpoint{
				{Slug: "chat", Auth: "shop-auth", AllowedOrigins: []string{"https://shop.example.com"},
					ConsumerGroups: []RealtimeConsumerGroup{{Channel: "orders", Name: "billing"}}},
				{Slug: "feed", Auth: RealtimeAuthNone, Callbacks: &RealtimeCallbacks{Message: "/realtime/message"}},
			}},
			Triggers: []Trigger{{Kind: TriggerKindRealtime, App: "orders-fn", Slug: "orders",
				Config: map[string]any{"endpoint": "chat", "channel": "orders", "group": "billing"}}},
		}
	}
	cases := []struct {
		name    string
		mutate  func(*Manifest)
		wantErr string
	}{
		{"valid", func(*Manifest) {}, ""},
		{"bad slug", func(m *Manifest) { m.Realtime.Endpoints[0].Slug = "Chat" }, "slug must be"},
		{"duplicate slug", func(m *Manifest) { m.Realtime.Endpoints[1].Slug = "chat" }, "declared twice"},
		{"no auth", func(m *Manifest) { m.Realtime.Endpoints[0].Auth = "" }, "set auth"},
		{"auth and oidc", func(m *Manifest) {
			m.Realtime.Endpoints[0].OIDC = &RealtimeOIDC{Issuer: "https://id.example", JWKSURL: "https://id.example/jwks"}
		}, "not both"},
		{"callbacks with an Auth app", func(m *Manifest) {
			m.Realtime.Endpoints[0].Callbacks = &RealtimeCallbacks{Connect: "/c"}
		}, "remove callbacks"},
		{"relative callback", func(m *Manifest) { m.Realtime.Endpoints[1].Callbacks.Message = "message" }, "starting with /"},
		{"oidc", func(m *Manifest) {
			m.Realtime.Endpoints[1] = RealtimeEndpoint{Slug: "feed", OIDC: &RealtimeOIDC{Issuer: "https://id.example",
				JWKSURL: "https://id.example/jwks", Algorithms: []string{"RS256"}}}
		}, ""},
		{"duplicate group", func(m *Manifest) {
			m.Realtime.Endpoints[0].ConsumerGroups = append(m.Realtime.Endpoints[0].ConsumerGroups, RealtimeConsumerGroup{Channel: "orders", Name: "billing"})
		}, "declared twice"},
		{"channel rules", func(m *Manifest) {
			m.Realtime.Endpoints[0].Channels = []RealtimeChannelRule{{Pattern: "org:{org_id}:room:*", Read: true}}
		}, ""},
		{"channel rules need tokens", func(m *Manifest) {
			m.Realtime.Endpoints[1].Channels = []RealtimeChannelRule{{Pattern: "room:*", Read: true}}
		}, "verified tokens"},
		{"bad channel rule", func(m *Manifest) {
			m.Realtime.Endpoints[0].Channels = []RealtimeChannelRule{{Pattern: "a*b", Read: true}}
		}, "only end"},
		{"trigger names an undeclared endpoint", func(m *Manifest) { m.Triggers[0].Config["endpoint"] = "other" }, "not declared"},
		{"trigger names endpoint and endpoint_id", func(m *Manifest) {
			m.Triggers[0].Config["endpoint_id"] = "6f1c2a4e-1d9b-4c7a-9a39-0b8d6f0f4c11"
		}, "not both"},
		{"several functions need app", func(m *Manifest) {
			m.Functions = []FunctionSpec{{Name: "a"}, {Name: "b"}}
		}, "app is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := valid()
			tc.mutate(m)
			err := m.validateRealtime()
			if err == nil && len(m.Triggers) > 0 {
				err = m.Triggers[0].validateRealtime(0)
			}
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("validate = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("validate = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestRealtimeSectionLoadsFromYAML(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte(`
realtime:
  endpoints:
    - slug: chat
      auth: shop-auth
      allowed_origins: [https://shop.example.com]
      max_connections: 500
      consumer_groups:
        - channel: orders
          name: billing
          lease_seconds: 120
`), 0o600); err != nil {
		t.Fatal(err)
	}
	m, found, err := Load(dir)
	if err != nil || !found {
		t.Fatalf("Load = %v %v", found, err)
	}
	endpoints, managed := m.Realtime.EndpointsFor("shop", "shop")
	if !managed || len(endpoints) != 1 || endpoints[0].Auth != "shop-auth" || endpoints[0].MaxConnections != 500 ||
		len(endpoints[0].ConsumerGroups) != 1 || *endpoints[0].ConsumerGroups[0].LeaseSeconds != 120 {
		t.Fatalf("endpoints = %+v managed=%v", endpoints, managed)
	}
	if other, _ := m.Realtime.EndpointsFor("other", "shop"); len(other) != 0 {
		t.Fatalf("endpoints for another app = %+v", other)
	}
}
