package api

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestProfilingConfigPprofPortValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		port    int
		wantErr string
	}{
		{name: "unset", port: 0},
		{name: "separate debug port", port: 6060},
		{name: "application port", port: 8080},
		{name: "highest port", port: 65535},
		{name: "negative", port: -1, wantErr: "between 1 and 65535"},
		{name: "out of range", port: 65536, wantErr: "between 1 and 65535"},
		{name: "profile bridge", port: 9191, wantErr: "reserved"},
		{name: "trace bridge", port: 4318, wantErr: "reserved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := (&ProfilingConfig{PprofPort: tc.port}).Validate(PlanHobby)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() = %v, want an error containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestProfilePprofReservedPortsNameGuestBridges(t *testing.T) {
	got := ProfilePprofReservedPorts()
	if len(got) != 2 || !slices.Contains(got, 9191) || !slices.Contains(got, 4318) {
		t.Fatalf("ProfilePprofReservedPorts() = %v, want the profile (9191) and trace (4318) bridges", got)
	}
}

func TestProfilingConfigEqualComparesPprofPort(t *testing.T) {
	a := &ProfilingConfig{PprofPort: 6060}
	if a.Equal(&ProfilingConfig{}) || !a.Equal(&ProfilingConfig{PprofPort: 6060}) {
		t.Fatal("Equal must compare pprof_port: changing it needs a profiling policy update")
	}
}

func TestProfilingConfigPprofPortOmittedWhenUnset(t *testing.T) {
	raw, err := json.Marshal(ProfilingConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "pprof_port") {
		t.Fatalf("marshal = %s; an unset pprof_port must be omitted so older guests see unchanged manifests", raw)
	}
	var cfg ProfilingConfig
	if err := json.Unmarshal([]byte(`{"enabled":false,"pprof_port":6060}`), &cfg); err != nil || cfg.PprofPort != 6060 {
		t.Fatalf("unmarshal = %+v, %v; want pprof_port 6060", cfg, err)
	}
}
