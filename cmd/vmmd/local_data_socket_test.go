package main

// adr: 20261030230157 — vmmd's local data socket for its node's gateway: off
// by default, off removes the socket, and a socket that cannot be set up never
// keeps vmmd from starting.

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/onebox-faas/faas/pkg/wire"
)

func TestLoadConfig_LocalDataSocketSwitch(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LocalDataSocket {
		t.Fatal("the local data socket must default to off")
	}
	path := filepath.Join(t.TempDir(), "vmmd.toml")
	if err := os.WriteFile(path, []byte("local_data_socket = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		env     string
		enabled bool
	}{{"", true}, {"0", false}, {"false", false}, {"1", true}} {
		t.Run("env="+tc.env, func(t *testing.T) {
			if tc.env != "" {
				t.Setenv("FAAS_VMMD_LOCAL_DATA_SOCKET", tc.env)
			}
			cfg, err := LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.LocalDataSocket != tc.enabled {
				t.Fatalf("LocalDataSocket = %v, want %v", cfg.LocalDataSocket, tc.enabled)
			}
		})
	}
	t.Setenv("FAAS_VMMD_LOCAL_DATA_SOCKET", "sometimes")
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "FAAS_VMMD_LOCAL_DATA_SOCKET") {
		t.Fatalf("invalid FAAS_VMMD_LOCAL_DATA_SOCKET error = %v, want named validation error", err)
	}
}

func quietVmmdLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// withGatewayUID points the socket at uid, or at a failed lookup.
func withGatewayUID(t *testing.T, uid int, err error) {
	t.Helper()
	prev := lookupGatewayUID
	lookupGatewayUID = func() (int, error) { return uid, err }
	t.Cleanup(func() { lookupGatewayUID = prev })
}

func TestLocalDataSocketOffRemovesAStaleSocket(t *testing.T) {
	path := filepath.Join(shortDir(t), "vmmd-data.sock")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if s := newLocalDataSocket(context.Background(), false, path, "", nil, quietVmmdLog()); s != nil {
		t.Fatal("the switch is off but a socket was set up")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale socket still at %s (stat err %v); the gateway would keep dialing it", path, err)
	}
}

func TestLocalDataSocketSetupFailureLeavesNoSocket(t *testing.T) {
	withGatewayUID(t, 0, errors.New("unknown user faas"))
	path := filepath.Join(shortDir(t), "vmmd-data.sock")
	if s := newLocalDataSocket(context.Background(), true, path, "", func(*grpc.Server) {}, quietVmmdLog()); s != nil {
		t.Fatal("a socket without the gateway's uid was set up")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket left at %s (stat err %v) though nothing serves it", path, err)
	}
	// A nil socket is the switch off: serve and stop do nothing.
	var off *localDataSocket
	off.serve(quietVmmdLog())
	<-off.stop(false, quietVmmdLog())
}

func TestLocalDataSocketServesAndStops(t *testing.T) {
	t.Setenv(wire.SkipGroupLookupEnv, "1")
	withGatewayUID(t, os.Getuid(), nil)
	path := filepath.Join(shortDir(t), "vmmd-data.sock")
	registered := false
	s := newLocalDataSocket(context.Background(), true, path, "", func(*grpc.Server) { registered = true }, quietVmmdLog())
	if s == nil || !registered {
		t.Fatalf("socket = %v, registered = %v; want vmmd's service on a bound socket", s, registered)
	}
	if info, err := os.Stat(path); err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("stat %s = %v, %v; want a unix socket", path, info, err)
	}
	s.serve(quietVmmdLog())
	select {
	case <-s.stop(false, quietVmmdLog()):
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not finish")
	}
}
