package billing

import "testing"

func TestParseMode(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want Mode
	}{
		{name: "unset remains live", want: ModeLive},
		{name: "explicit live", raw: "live", want: ModeLive},
		{name: "disabled", raw: "disabled", want: ModeDisabled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseMode(tc.raw)
			if err != nil || got != tc.want {
				t.Fatalf("ParseMode(%q) = %q, %v; want %q", tc.raw, got, err, tc.want)
			}
		})
	}
	if _, err := ParseMode("off"); err == nil {
		t.Fatal("invalid mode accepted")
	}
}

func TestModeFromEnv(t *testing.T) {
	got, err := ModeFromEnv(func(key string) string {
		if key != BillingModeEnv {
			t.Fatalf("key = %q", key)
		}
		return "disabled"
	})
	if err != nil || got != ModeDisabled || got.Enabled() {
		t.Fatalf("ModeFromEnv = %q, %v", got, err)
	}
}
