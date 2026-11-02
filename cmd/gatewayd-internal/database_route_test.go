// adr: 1027
package main

import "testing"

func TestValidateDatabaseRoute(t *testing.T) {
	valid := func() Config {
		return Config{
			ServiceProxyListen:      "10.100.0.1:10081",
			DatabaseRouteListen:     "10.100.0.1:10083",
			DatabaseRouteUpstreams:  []string{"100.64.0.20:5432", "100.64.0.21:5432"},
			DatabaseRouteHostSuffix: "db.gregale.test",
		}
	}
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{"valid", func(*Config) {}, false},
		{"off", func(c *Config) { *c = Config{} }, false},
		{"suffix without the forwarder", func(c *Config) { c.DatabaseRouteListen = "" }, true},
		{"wrong port", func(c *Config) { c.DatabaseRouteListen = "10.100.0.1:5432" }, true},
		{"not the bridge", func(c *Config) { c.DatabaseRouteListen = "0.0.0.0:10083" }, true},
		{"no service proxy", func(c *Config) { c.ServiceProxyListen = "" }, true},
		{"no upstreams", func(c *Config) { c.DatabaseRouteUpstreams = nil }, true},
		{"upstream without port", func(c *Config) { c.DatabaseRouteUpstreams = []string{"pgproxyd"} }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid()
			tt.mutate(&cfg)
			if err := validateDatabaseRoute(&cfg); (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
