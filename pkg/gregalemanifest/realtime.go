package gregalemanifest

import (
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// RealtimeConfig declares an app's realtime endpoints in gregale.yaml
// (ADR-20261019102657). A deploy reconciles the slugged endpoints of each app
// it names: it creates and updates them, and disables (never deletes) one the
// manifest no longer declares, so its retained history and consumer groups
// survive and a rollback can enable it again.
type RealtimeConfig struct {
	Endpoints []RealtimeEndpoint `yaml:"endpoints" toml:"endpoints"`
}

// RealtimeEndpoint is one managed realtime endpoint, named by slug within
// its app.
type RealtimeEndpoint struct {
	// App is the app the endpoint belongs to; it defaults to the deployed
	// app and is required when the manifest deploys several functions.
	App  string `yaml:"app,omitempty" toml:"app"`
	Slug string `yaml:"slug" toml:"slug"`
	// Auth names a Gregale Auth app whose users connect with their access
	// tokens; the Auth app then authorizes channels. "none" accepts any
	// client. Leave it empty to use OIDC.
	Auth string        `yaml:"auth,omitempty" toml:"auth"`
	OIDC *RealtimeOIDC `yaml:"oidc,omitempty" toml:"oidc"`
	// Callbacks are paths on the app that receive connect, message and
	// disconnect events. Not allowed with Auth, whose callbacks are the
	// Auth app's.
	Callbacks               *RealtimeCallbacks      `yaml:"callbacks,omitempty" toml:"callbacks"`
	AllowedOrigins          []string                `yaml:"allowed_origins,omitempty" toml:"allowed_origins"`
	MaxConnections          int                     `yaml:"max_connections,omitempty" toml:"max_connections"`
	MaxMessageBytes         int64                   `yaml:"max_message_bytes,omitempty" toml:"max_message_bytes"`
	MaxConnectionAgeSeconds int64                   `yaml:"max_connection_age_seconds,omitempty" toml:"max_connection_age_seconds"`
	Enabled                 *bool                   `yaml:"enabled,omitempty" toml:"enabled"`
	ConsumerGroups          []RealtimeConsumerGroup `yaml:"consumer_groups,omitempty" toml:"consumer_groups"`
}

// RealtimeOIDC trusts an external identity provider's JWTs.
type RealtimeOIDC struct {
	Issuer     string   `yaml:"issuer" toml:"issuer"`
	JWKSURL    string   `yaml:"jwks_url" toml:"jwks_url"`
	Audience   []string `yaml:"audience,omitempty" toml:"audience"`
	Algorithms []string `yaml:"algorithms,omitempty" toml:"algorithms"`
}

// RealtimeCallbacks are the app paths realtimed calls.
type RealtimeCallbacks struct {
	Connect    string `yaml:"connect,omitempty" toml:"connect"`
	Message    string `yaml:"message,omitempty" toml:"message"`
	Disconnect string `yaml:"disconnect,omitempty" toml:"disconnect"`
}

// RealtimeConsumerGroup is a consumer group on one of the endpoint's
// channels. A deploy creates missing groups and never deletes one, because a
// group holds undelivered messages.
type RealtimeConsumerGroup struct {
	Channel      string `yaml:"channel" toml:"channel"`
	Name         string `yaml:"name" toml:"name"`
	LeaseSeconds *int   `yaml:"lease_seconds,omitempty" toml:"lease_seconds"`
	MaxAttempts  *int   `yaml:"max_attempts,omitempty" toml:"max_attempts"`
}

// RealtimeAuthNone declares an endpoint that accepts any client.
const RealtimeAuthNone = "none"

// EndpointsFor returns the endpoints declared for app, and whether the
// manifest manages that app's realtime endpoints at all. defaultApp is the
// app a single-function deploy creates.
func (c *RealtimeConfig) EndpointsFor(app, defaultApp string) ([]RealtimeEndpoint, bool) {
	if c == nil {
		return nil, false
	}
	out := []RealtimeEndpoint{}
	for _, e := range c.Endpoints {
		if e.app(defaultApp) == app {
			out = append(out, e)
		}
	}
	return out, true
}

func (e RealtimeEndpoint) app(defaultApp string) string {
	if e.App != "" {
		return e.App
	}
	return defaultApp
}

// validateRealtime checks the realtime section and the realtime triggers
// that name an endpoint by slug.
func (m *Manifest) validateRealtime() error {
	declared := map[string]bool{} // app + "/" + slug; app "" is the deployed app
	if m.Realtime != nil {
		if len(m.Functions) > 1 {
			for i, e := range m.Realtime.Endpoints {
				if e.App == "" {
					return fmt.Errorf("realtime.endpoints[%d]: app is required when the manifest deploys several functions", i)
				}
			}
		}
		for i, e := range m.Realtime.Endpoints {
			if err := e.validate(); err != nil {
				return fmt.Errorf("realtime.endpoints[%d]: %w", i, err)
			}
			key := e.App + "/" + e.Slug
			if declared[key] {
				return fmt.Errorf("realtime.endpoints[%d]: slug %q is declared twice for the same app", i, e.Slug)
			}
			declared[key] = true
		}
	}
	for i, t := range m.Triggers {
		if t.Kind != TriggerKindRealtime {
			continue
		}
		ref, ok := t.Config["endpoint"].(string)
		if !ok {
			continue
		}
		if !declared[t.App+"/"+ref] && !declared["/"+ref] {
			return fmt.Errorf("trigger[%d]: endpoint %q is not declared under realtime.endpoints for app %s", i, ref, t.App)
		}
	}
	return nil
}

func (e RealtimeEndpoint) validate() error {
	if err := api.ValidateRealtimeEndpointSlug(e.Slug); err != nil {
		return err
	}
	switch {
	case e.Auth != "" && e.OIDC != nil:
		return fmt.Errorf("set auth or oidc, not both")
	case e.Auth != "" && e.Auth != RealtimeAuthNone && e.Callbacks != nil:
		return fmt.Errorf("callbacks are the Auth app's when auth names one; remove callbacks")
	case e.Auth == "" && e.OIDC == nil:
		return fmt.Errorf("set auth (an Auth app, or %q) or oidc", RealtimeAuthNone)
	}
	if e.OIDC != nil {
		if err := api.ValidateRealtimeAuth(api.RealtimeAuthModeOIDCJWT, false, e.OIDC.Issuer, e.OIDC.JWKSURL, e.OIDC.Audience, e.OIDC.Algorithms, nil); err != nil {
			return err
		}
	}
	if e.Callbacks != nil {
		for name, path := range map[string]string{"connect": e.Callbacks.Connect, "message": e.Callbacks.Message, "disconnect": e.Callbacks.Disconnect} {
			if path != "" && !strings.HasPrefix(path, "/") {
				return fmt.Errorf("callbacks.%s must be a path starting with /", name)
			}
		}
	}
	if err := api.ValidateRealtimeOrigins(e.AllowedOrigins); err != nil {
		return err
	}
	seen := map[string]bool{}
	for i, g := range e.ConsumerGroups {
		if g.Channel == "" || g.Name == "" {
			return fmt.Errorf("consumer_groups[%d]: channel and name are required", i)
		}
		key := g.Channel + "\x00" + g.Name
		if seen[key] {
			return fmt.Errorf("consumer_groups[%d]: %s/%s is declared twice", i, g.Channel, g.Name)
		}
		seen[key] = true
	}
	return nil
}
