package api

import (
	"math"
	"testing"
)

func TestTracingConfigValidate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cfg     *TracingConfig
		plan    Plan
		wantErr bool
	}{
		{name: "nil", cfg: nil, plan: PlanFree},
		{name: "disabled on free", cfg: &TracingConfig{}, plan: PlanFree},
		{name: "enabled on free", cfg: &TracingConfig{Enabled: true}, plan: PlanFree, wantErr: true},
		{name: "enabled on pro", cfg: &TracingConfig{Enabled: true}, plan: PlanPro},
		{name: "ratio", cfg: &TracingConfig{Enabled: true, SampleRatio: 0.25}, plan: PlanPro},
		{name: "ratio one", cfg: &TracingConfig{Enabled: true, SampleRatio: 1}, plan: PlanPro},
		{name: "negative ratio", cfg: &TracingConfig{Enabled: true, SampleRatio: -0.1}, plan: PlanPro, wantErr: true},
		{name: "ratio above one", cfg: &TracingConfig{Enabled: true, SampleRatio: 1.5}, plan: PlanPro, wantErr: true},
		{name: "nan ratio", cfg: &TracingConfig{Enabled: true, SampleRatio: math.NaN()}, plan: PlanPro, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate(tc.plan)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestTracingConfigEffectiveSampleRatio(t *testing.T) {
	if got := (*TracingConfig)(nil).EffectiveSampleRatio(); got != 1 {
		t.Fatalf("nil ratio = %v, want 1", got)
	}
	if got := (&TracingConfig{SampleRatio: 0.5}).EffectiveSampleRatio(); got != 0.5 {
		t.Fatalf("ratio = %v, want 0.5", got)
	}
}

func TestTraceCodecMediaType(t *testing.T) {
	for codec, want := range map[byte][2]string{
		TraceCodecProtobuf:     {"application/x-protobuf", ""},
		TraceCodecJSON:         {"application/json", ""},
		TraceCodecGzipProtobuf: {"application/x-protobuf", "gzip"},
		TraceCodecGzipJSON:     {"application/json", "gzip"},
	} {
		ct, enc, ok := TraceCodecMediaType(codec)
		if !ok || ct != want[0] || enc != want[1] {
			t.Fatalf("codec %#x = (%q, %q, %v), want %v", codec, ct, enc, ok, want)
		}
	}
	if _, _, ok := TraceCodecMediaType(0x7f); ok {
		t.Fatal("unknown codec accepted")
	}
}
