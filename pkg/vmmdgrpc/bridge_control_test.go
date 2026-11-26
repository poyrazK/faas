package vmmdgrpc

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/instancebridge"
)

func TestCompareBridgeActivity(t *testing.T) {
	busy := instancebridge.Stats{Inflight: 1}
	session := instancebridge.Stats{Sessions: 1}
	for _, tc := range []struct {
		name   string
		bridge bridgeActivity
		vmmd   int64
		want   string
	}{
		{"both idle", bridgeActivity{}, 0, bridgeActivityAgree},
		{"both busy", bridgeActivity{stats: busy}, 1, bridgeActivityAgree},
		{"session counts as busy", bridgeActivity{stats: session}, 2, bridgeActivityAgree},
		{"only the bridge", bridgeActivity{stats: busy}, 0, bridgeActivityBridgeBusy},
		{"only vmmd (an execution)", bridgeActivity{}, 1, bridgeActivityVMMDBusy},
		{"unreadable", bridgeActivity{err: errors.New("closed")}, 0, bridgeActivityError},
	} {
		if got := compareBridgeActivity(tc.bridge, tc.vmmd); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}

func enableBridgeControl(t *testing.T) {
	t.Helper()
	t.Setenv(instanceBridgeControlEnv, "1")
	t.Setenv(instanceBridgeUnitEnv, "1")
	t.Setenv("FAAS_STREAM_BRIDGE_VERSION", "v2")
	t.Setenv(streamBridgePersistentEnv, "1")
}

func TestBridgeControlEnvOnlyWithTheSwitch(t *testing.T) {
	entry := &streamBridgeEntry{instance: "inst-1", socket: "/run/faas-ibridge/inst-1/http.sock"}
	if env := bridgeControlEnv(entry); env != nil {
		t.Fatalf("switch off: env = %v, want none", env)
	}
	enableBridgeControl(t)
	want := []string{"FAAS_BRIDGE_CONTROL_SOCKET=/run/faas-ibridge/inst-1/control.sock", "FAAS_BRIDGE_INSTANCE=inst-1"}
	if env := bridgeControlEnv(entry); !slices.Equal(env, want) {
		t.Fatalf("env = %v, want %v", env, want)
	}
}

// vmmd connects to a unit bridge's control socket, reads its counters in
// the shadow pass, and drops the connection when the entry closes.
func TestBridgeControlConnectReadAndClose(t *testing.T) {
	enableBridgeControl(t)
	dir, err := os.MkdirTemp("", "ibc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	data := filepath.Join(dir, "http.sock")
	ln, err := net.Listen("unix", bridgeControlSocket(data))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	counters := &instancebridge.Counters{}
	go func() { _ = instancebridge.ServeControl(ctx, ln, "inst-1", counters) }()

	m := newStreamBridgeManager(nil)
	entry := &streamBridgeEntry{key: "inst-1", instance: "inst-1", socket: data, unit: &bridgeUnit{name: "faas-ibridge-inst-1-x.service"}}
	m.entries[entry.mapKey()] = entry
	m.connectControl(ctx, entry)
	if entry.control == nil {
		t.Fatal("no control connection after connectControl")
	}
	end := counters.BeginRequest()
	got := m.readBridgeActivity(ctx)
	if len(got) != 1 || got[0].instance != "inst-1" || got[0].err != nil || got[0].stats.Inflight != 1 {
		t.Fatalf("readBridgeActivity = %+v, want inst-1 with 1 in flight", got)
	}
	end()
	m.closeControl(entry)
	if entry.control != nil || len(m.readBridgeActivity(ctx)) != 0 {
		t.Fatal("control connection survived closeControl")
	}
}

// A bridge started before the switch was on has no control socket; vmmd
// leaves it alone instead of counting an error.
func TestBridgeControlSkipsBridgeWithoutSocket(t *testing.T) {
	enableBridgeControl(t)
	m := newStreamBridgeManager(nil)
	entry := &streamBridgeEntry{instance: "inst-1", socket: filepath.Join(t.TempDir(), "http.sock"), unit: &bridgeUnit{}}
	m.connectControl(context.Background(), entry)
	if entry.control != nil {
		t.Fatal("connected to a bridge without a control socket")
	}
}

// A control socket answering for another instance is never trusted.
func TestBridgeControlRefusesWrongInstance(t *testing.T) {
	enableBridgeControl(t)
	dir, err := os.MkdirTemp("", "ibc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	data := filepath.Join(dir, "http.sock")
	ln, err := net.Listen("unix", bridgeControlSocket(data))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = instancebridge.ServeControl(ctx, ln, "inst-other", &instancebridge.Counters{}) }()
	m := newStreamBridgeManager(nil)
	entry := &streamBridgeEntry{instance: "inst-1", socket: data, unit: &bridgeUnit{}}
	m.connectControl(ctx, entry)
	if entry.control != nil {
		t.Fatal("trusted a control socket that names another instance")
	}
}
