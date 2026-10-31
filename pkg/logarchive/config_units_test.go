package logarchive

import "testing"

// FAAS_LOG_ARCHIVE_LOCAL_BYTES_MAX=20G used to read as 20 bytes: Sscanf("%d")
// stopped at the unit. Units now scale, and anything else falls back.
func TestConfigFromEnv_LocalBytesMaxUnits(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  int64
	}{
		{"10737418240", 10 << 30},
		{"20G", 20 << 30},
		{"20GiB", 20 << 30},
		{"512M", 512 << 20},
		{" 1T ", 1 << 40},
		{"20Gb", DefaultLocalBytesMax},
		{"20 apples", DefaultLocalBytesMax},
		{"9223372036854775807G", DefaultLocalBytesMax},
	} {
		getenv := func(k string) string {
			if k == EnvLocalBytesMax {
				return tc.value
			}
			return ""
		}
		cfg, err := ConfigFromEnv(getenv, nil)
		if err != nil {
			t.Fatalf("%q: %v", tc.value, err)
		}
		if cfg.LocalBytesMax != tc.want {
			t.Errorf("%q: LocalBytesMax = %d, want %d", tc.value, cfg.LocalBytesMax, tc.want)
		}
	}
	getenv := func(k string) string {
		if k == EnvRetentionDays {
			return "7d"
		}
		return ""
	}
	if cfg, _ := ConfigFromEnv(getenv, nil); cfg.RetentionDays != DefaultRetentionDays {
		t.Errorf("retention %q read as %d, want the default", "7d", cfg.RetentionDays)
	}
}
