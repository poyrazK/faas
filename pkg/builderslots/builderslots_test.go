package builderslots_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/builderslots"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    builderslots.Config
		wantErr string
	}{
		{name: "compiled default", raw: string(builderslots.DefaultJSON()), want: builderslots.DefaultConfig()},
		{name: "empty object keeps the default", raw: `{}`, want: builderslots.DefaultConfig()},
		{
			name: "per node", raw: `{"default":{"reserved":1,"max":3},"nodes":{"gregale-compute-1":{"reserved":1,"max":8}}}`,
			want: builderslots.Config{Default: builderslots.Slots{Reserved: 1, Max: 3}, Nodes: map[string]builderslots.Slots{"gregale-compute-1": {Reserved: 1, Max: 8}}},
		},
		{
			name: "nodes only", raw: `{"nodes":{"n1":{"reserved":1,"max":1}}}`,
			want: builderslots.Config{Default: builderslots.DefaultSlots, Nodes: map[string]builderslots.Slots{"n1": {Reserved: 1, Max: 1}}},
		},
		{name: "not an object", raw: `2`, wantErr: "JSON object"},
		{name: "unknown field", raw: `{"default":{"reserved":1,"maximum":4}}`, wantErr: "unknown field"},
		{name: "trailing data", raw: `{} {}`, wantErr: "trailing data"},
		{name: "reserved zero", raw: `{"default":{"reserved":0,"max":2}}`, wantErr: "default.reserved is 0"},
		{
			name: "reserved two", raw: `{"default":{"reserved":2,"max":4}}`,
			want: builderslots.Config{Default: builderslots.Slots{Reserved: 2, Max: 4}},
		},
		{name: "reserved above bound", raw: `{"default":{"reserved":5,"max":6}}`, wantErr: "default.reserved is 5"},
		{name: "max below reserved", raw: `{"default":{"reserved":1,"max":0}}`, wantErr: "default.max is 0"},
		{name: "max above bound", raw: `{"nodes":{"n1":{"reserved":1,"max":17}}}`, wantErr: "nodes.n1.max is 17"},
		{name: "bad node name", raw: `{"nodes":{"bad name":{"reserved":1,"max":2}}}`, wantErr: "not a compute node name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := builderslots.Parse(json.RawMessage(tt.raw))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Parse(%s) error = %v, want %q", tt.raw, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%s): %v", tt.raw, err)
			}
			if got.Default != tt.want.Default || len(got.Nodes) != len(tt.want.Nodes) {
				t.Fatalf("Parse(%s) = %+v, want %+v", tt.raw, got, tt.want)
			}
			for name, slots := range tt.want.Nodes {
				if got.Nodes[name] != slots {
					t.Fatalf("Parse(%s).Nodes[%s] = %+v, want %+v", tt.raw, name, got.Nodes[name], slots)
				}
			}
		})
	}
}

func TestParseTooManyNodes(t *testing.T) {
	nodes := map[string]builderslots.Slots{}
	for i := 0; i <= api.BuilderSlotsNodeEntriesMax; i++ {
		nodes["n"+strings.Repeat("x", i)] = builderslots.DefaultSlots
	}
	raw, err := json.Marshal(builderslots.Config{Default: builderslots.DefaultSlots, Nodes: nodes})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := builderslots.Parse(raw); err == nil || !strings.Contains(err.Error(), "nodes named") {
		t.Fatalf("Parse with %d nodes: error = %v", len(nodes), err)
	}
}

func TestForAndClamp(t *testing.T) {
	cfg := builderslots.Config{Default: builderslots.Slots{Reserved: 1, Max: 2}, Nodes: map[string]builderslots.Slots{"big": {Reserved: 1, Max: 8}}}
	tests := []struct {
		node       string
		cpus       int
		want       builderslots.Slots
		wantClamp  bool
		wantBorrow int
	}{
		{node: "big", cpus: 16, want: builderslots.Slots{Reserved: 1, Max: 8}, wantBorrow: 7},
		{node: "big", cpus: 4, want: builderslots.Slots{Reserved: 1, Max: 2}, wantClamp: true, wantBorrow: 1},
		{node: "big", cpus: 1, want: builderslots.Slots{Reserved: 1, Max: 1}, wantClamp: true},
		{node: "other", cpus: 4, want: builderslots.Slots{Reserved: 1, Max: 2}, wantBorrow: 1},
		{node: "", cpus: 4, want: builderslots.Slots{Reserved: 1, Max: 2}, wantBorrow: 1},
	}
	for _, tt := range tests {
		got, clamped := builderslots.Clamp(cfg.For(tt.node), tt.cpus)
		if got != tt.want || clamped != tt.wantClamp || got.Borrowed() != tt.wantBorrow {
			t.Fatalf("Clamp(For(%q), %d) = %+v, %t (borrowed %d); want %+v, %t (borrowed %d)",
				tt.node, tt.cpus, got, clamped, got.Borrowed(), tt.want, tt.wantClamp, tt.wantBorrow)
		}
	}
}

// A node runs the reserved slots it was provisioned with, whatever
// builder_slots asks for, and keeps max at or above them (ADR-20261118154632
// amendment 1).
func TestProvisioned(t *testing.T) {
	tests := []struct {
		in          builderslots.Slots
		provisioned int
		want        builderslots.Slots
		wantChanged bool
	}{
		{in: builderslots.Slots{Reserved: 1, Max: 2}, provisioned: 1, want: builderslots.Slots{Reserved: 1, Max: 2}},
		{in: builderslots.Slots{Reserved: 1, Max: 2}, provisioned: 2, want: builderslots.Slots{Reserved: 2, Max: 2}, wantChanged: true},
		{in: builderslots.Slots{Reserved: 1, Max: 8}, provisioned: 2, want: builderslots.Slots{Reserved: 2, Max: 8}, wantChanged: true},
		{in: builderslots.Slots{Reserved: 3, Max: 3}, provisioned: 1, want: builderslots.Slots{Reserved: 1, Max: 3}, wantChanged: true},
		{in: builderslots.Slots{Reserved: 1, Max: 1}, provisioned: 0, want: builderslots.Slots{Reserved: 1, Max: 1}},
		{in: builderslots.Slots{Reserved: 1, Max: 2}, provisioned: 9, want: builderslots.Slots{Reserved: 4, Max: 4}, wantChanged: true},
	}
	for _, tt := range tests {
		got, changed := builderslots.Provisioned(tt.in, tt.provisioned)
		if got != tt.want || changed != tt.wantChanged {
			t.Fatalf("Provisioned(%+v, %d) = %+v, %t; want %+v, %t", tt.in, tt.provisioned, got, changed, tt.want, tt.wantChanged)
		}
	}
}

func TestMemoryThresholds(t *testing.T) {
	// A production-sized tenant fence: 10,000 MiB.
	const fence = 10_000
	tests := []struct {
		name                     string
		m                        builderslots.Memory
		admit, giveBack, mustKil bool
	}{
		{name: "quiet node", m: builderslots.Memory{TenantMB: 2_000, TenantMaxMB: fence, AvailableMB: 9_000}, admit: true},
		{name: "admission edge", m: builderslots.Memory{TenantMB: 7_000 - api.BorrowedBuildNeedMB, TenantMaxMB: fence, AvailableMB: 9_000}, admit: true},
		{name: "just past admission", m: builderslots.Memory{TenantMB: 7_001 - api.BorrowedBuildNeedMB, TenantMaxMB: fence, AvailableMB: 9_000}},
		{name: "host short for a build", m: builderslots.Memory{TenantMB: 1_000, TenantMaxMB: fence, AvailableMB: api.BorrowedBuildNeedMB + api.BuilderBorrowHostFloorMB - 1}},
		{name: "give back at 86%", m: builderslots.Memory{TenantMB: 5_600, BorrowedMB: 3_000, TenantMaxMB: fence, AvailableMB: 4_000}, giveBack: true},
		{name: "85% exactly holds", m: builderslots.Memory{TenantMB: 5_500, BorrowedMB: 3_000, TenantMaxMB: fence, AvailableMB: 4_000}},
		{name: "host floor gives back", m: builderslots.Memory{TenantMB: 1_000, TenantMaxMB: fence, AvailableMB: api.BuilderBorrowHostFloorMB - 1}, giveBack: true},
		{name: "force at 93%", m: builderslots.Memory{TenantMB: 6_300, BorrowedMB: 3_000, TenantMaxMB: fence, AvailableMB: 2_000}, giveBack: true, mustKil: true},
		{name: "host critical forces", m: builderslots.Memory{TenantMB: 1_000, TenantMaxMB: fence, AvailableMB: api.BuilderBorrowHostCriticalMB - 1}, giveBack: true, mustKil: true},
		{name: "unknown fence is pressure", m: builderslots.Memory{AvailableMB: 9_000}, giveBack: true, mustKil: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := builderslots.CanAdmitBorrowed(tt.m); got != tt.admit {
				t.Errorf("CanAdmitBorrowed(%+v) = %t, want %t", tt.m, got, tt.admit)
			}
			if got := builderslots.ShouldGiveBack(tt.m); got != tt.giveBack {
				t.Errorf("ShouldGiveBack(%+v) = %t, want %t", tt.m, got, tt.giveBack)
			}
			if got := builderslots.MustForce(tt.m); got != tt.mustKil {
				t.Errorf("MustForce(%+v) = %t, want %t", tt.m, got, tt.mustKil)
			}
		})
	}
}

// The give-back must start before the force, and admission must stop before
// either, or a node would admit a build only to cancel it.
func TestThresholdOrdering(t *testing.T) {
	if !(api.BuilderBorrowAdmitPct < api.BuilderBorrowGiveBackPct && api.BuilderBorrowGiveBackPct < api.BuilderBorrowForcePct && api.BuilderBorrowForcePct < 100) {
		t.Fatalf("thresholds out of order: admit %d, give back %d, force %d", api.BuilderBorrowAdmitPct, api.BuilderBorrowGiveBackPct, api.BuilderBorrowForcePct)
	}
	if api.BuilderBorrowHostCriticalMB >= api.BuilderBorrowHostFloorMB {
		t.Fatalf("host critical %d MiB must be below the floor %d MiB", api.BuilderBorrowHostCriticalMB, api.BuilderBorrowHostFloorMB)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReader(t *testing.T) {
	const meminfo = "MemTotal:       16000000 kB\nMemFree:  100 kB\nMemAvailable:    8192000 kB\n"
	tests := []struct {
		name        string
		files       map[string]string
		want        builderslots.Memory
		wantSlice   bool
		wantErrPart string
	}{
		{
			name: "bounded tenant, borrowed slice",
			files: map[string]string{
				"cg/" + builderslots.TenantSlice + "/memory.current":   "2147483648\n",
				"cg/" + builderslots.TenantSlice + "/memory.max":       "10737418240\n",
				"cg/" + builderslots.BorrowedSlice + "/memory.current": "1073741824\n",
			},
			want:      builderslots.Memory{TenantMB: 2048, TenantMaxMB: 10240, BorrowedMB: 1024, AvailableMB: 8000},
			wantSlice: true,
		},
		{
			name: "unbounded tenant, no borrowed slice",
			files: map[string]string{
				"cg/" + builderslots.TenantSlice + "/memory.current": "1048576\n",
				"cg/" + builderslots.TenantSlice + "/memory.max":     "max\n",
			},
			want: builderslots.Memory{TenantMB: 1, TenantMaxMB: 15625, AvailableMB: 8000},
		},
		{
			name:        "missing tenant slice",
			files:       map[string]string{},
			wantErrPart: "faas-tenant.slice/memory.current",
		},
		{
			name: "garbage value",
			files: map[string]string{
				"cg/" + builderslots.TenantSlice + "/memory.current": "lots\n",
				"cg/" + builderslots.TenantSlice + "/memory.max":     "max\n",
			},
			wantErrPart: "not a byte count",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "meminfo"), meminfo)
			if err := os.MkdirAll(filepath.Join(dir, "cg"), 0o755); err != nil {
				t.Fatal(err)
			}
			for rel, body := range tt.files {
				writeFile(t, filepath.Join(dir, rel), body)
			}
			r := builderslots.Reader{CgroupRoot: filepath.Join(dir, "cg"), MemInfoPath: filepath.Join(dir, "meminfo")}
			got, err := r.Read()
			if tt.wantErrPart != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrPart) {
					t.Fatalf("Read() error = %v, want %q", err, tt.wantErrPart)
				}
				return
			}
			if err != nil {
				t.Fatalf("Read(): %v", err)
			}
			if got != tt.want {
				t.Fatalf("Read() = %+v, want %+v", got, tt.want)
			}
			if r.BorrowedSliceExists() != tt.wantSlice {
				t.Fatalf("BorrowedSliceExists() = %t, want %t", !tt.wantSlice, tt.wantSlice)
			}
		})
	}
}
