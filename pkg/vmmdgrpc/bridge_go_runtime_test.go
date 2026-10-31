package vmmdgrpc

// vmmd's stream_bridge_gomaxprocs and stream_bridge_gogc reach every stream
// bridge it starts, and 0 leaves the bridge on Go's defaults.

import (
	"slices"
	"strings"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

func TestStreamBridgeGoRuntimeEnv(t *testing.T) {
	for _, tc := range []struct {
		name             string
		gomaxprocs, gogc int
		want             []string
	}{
		{name: "defaults leave Go alone"},
		{name: "both set", gomaxprocs: 1, gogc: 200, want: []string{"GOMAXPROCS=1", "GOGC=200"}},
		{name: "GOGC only", gogc: 200, want: []string{"GOGC=200"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newStreamBridgeManager(nil)
			m.setGoRuntime(tc.gomaxprocs, tc.gogc)
			var got []string
			for _, kv := range m.bridgeEnv(&vmmdpb.ForwardHTTPRequestInit{Instance: "i-1"}, nil) {
				if strings.HasPrefix(kv, "GOMAXPROCS=") || strings.HasPrefix(kv, "GOGC=") {
					got = append(got, kv)
				}
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("bridge Go runtime env = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestServerStreamBridgeGoRuntimeReachesEveryManager(t *testing.T) {
	s := NewWithCPUAndNetAndActivity(nil, nil, "", nil, nil, nil, nil)
	if s.streamBridges.gomaxprocs.Load() != 0 || s.streamBridges.gogc.Load() != 0 {
		t.Fatal("bridges must default to Go's runtime settings")
	}
	s.WithStreamBridgeGoRuntime(1, 200)
	for name, m := range map[string]*streamBridgeManager{"constructor": s.streamBridges, "lazy": s.newBridgeManager()} {
		if m.gomaxprocs.Load() != 1 || m.gogc.Load() != 200 {
			t.Errorf("%s manager = GOMAXPROCS %d GOGC %d, want 1 and 200", name, m.gomaxprocs.Load(), m.gogc.Load())
		}
	}
}
