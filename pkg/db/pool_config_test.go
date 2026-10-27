package db

import (
	"testing"
	"time"
)

// TestDaemonMinConnectionsNeverExceedsMaximum keeps every warm floor inside
// the budget postgres_capacity provisions, in both notify-hub modes.
func TestDaemonMinConnectionsNeverExceedsMaximum(t *testing.T) {
	for _, hubEnv := range []string{"", "0"} {
		t.Setenv(NotifyHubEnv, hubEnv)
		for daemon, floor := range DaemonMinConnections {
			if limit := daemonMaxConnections(daemon); floor > limit {
				t.Errorf("%s=%q: %s floor %d exceeds its maximum %d", NotifyHubEnv, hubEnv, daemon, floor, limit)
			}
		}
	}
}

// TestPoolConfigWarmFloor pins which pools keep a warm floor and how the
// connections above it age.
func TestPoolConfigWarmFloor(t *testing.T) {
	tests := []struct {
		name       string
		appName    string
		readOnly   bool
		wantMin    int32
		wantJitter time.Duration
	}{
		{name: "schedd keeps a warm floor", appName: "faas-schedd", wantMin: DaemonMinConnections["schedd"], wantJitter: warmFloorLifetimeJitter},
		{name: "unprefixed name resolves the same floor", appName: "schedd", wantMin: DaemonMinConnections["schedd"], wantJitter: warmFloorLifetimeJitter},
		{name: "apid closes idle connections", appName: "faas-apid"},
		{name: "read-only pool without a floor", appName: "bridged", readOnly: true},
		{name: "unknown daemon", appName: "faas-unknown"},
		{name: "empty app name", appName: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(NotifyHubEnv, "")
			t.Setenv(DirectDSNEnv, "")
			t.Setenv(RuntimeRoleEnv, "")
			cfg, err := poolConfig("postgres://faas@127.0.0.1:5432/faas", tc.appName, tc.readOnly)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.MinConns != tc.wantMin {
				t.Errorf("MinConns = %d, want %d", cfg.MinConns, tc.wantMin)
			}
			if cfg.MaxConnLifetimeJitter != tc.wantJitter {
				t.Errorf("MaxConnLifetimeJitter = %s, want %s", cfg.MaxConnLifetimeJitter, tc.wantJitter)
			}
			if want := daemonMaxConnections(tc.appName); cfg.MaxConns != want {
				t.Errorf("MaxConns = %d, want %d", cfg.MaxConns, want)
			}
			if cfg.MaxConnIdleTime != time.Minute {
				t.Errorf("MaxConnIdleTime = %s, want 1m: connections above the floor must still close", cfg.MaxConnIdleTime)
			}
			if got := cfg.ConnConfig.RuntimeParams["default_transaction_read_only"] == "on"; got != tc.readOnly {
				t.Errorf("read-only session = %t, want %t", got, tc.readOnly)
			}
		})
	}
}
