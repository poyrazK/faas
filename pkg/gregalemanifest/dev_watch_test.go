package gregalemanifest

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDevWatchConfigYAML(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want DevWatchConfig
		err  string
	}{
		{src: "watch: true", want: DevWatchConfig{Enabled: true}},
		{src: "watch: false", want: DevWatchConfig{}},
		{src: "watch:\n  command: '  next dev  '", want: DevWatchConfig{Enabled: true, Command: "next dev"}},
		{src: "watch: sometimes", err: "dev.watch must be"},
		{src: "watch:\n  cmd: next dev", err: "unknown key"},
		{src: "watch: [1]", err: "dev.watch must be"},
	} {
		var out struct {
			Watch *DevWatchConfig `yaml:"watch"`
		}
		err := yaml.Unmarshal([]byte(tc.src), &out)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Fatalf("%q: error %v, want %q", tc.src, err, tc.err)
			}
			continue
		}
		if err != nil || out.Watch == nil || *out.Watch != tc.want {
			t.Fatalf("%q: %+v, %v; want %+v", tc.src, out.Watch, err, tc.want)
		}
		round, err := yaml.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		var again struct {
			Watch *DevWatchConfig `yaml:"watch"`
		}
		if err := yaml.Unmarshal(round, &again); err != nil || *again.Watch != tc.want {
			t.Fatalf("%q: round trip %q = %+v, %v", tc.src, round, again.Watch, err)
		}
	}
}

func TestDevConfigValidatesWatchCommand(t *testing.T) {
	ok := &DevConfig{Watch: &DevWatchConfig{Enabled: true, Command: "npm run dev"}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := &DevConfig{Watch: &DevWatchConfig{Enabled: true, Command: "npm run dev\nrm -rf /"}}
	if err := bad.Validate(); err == nil {
		t.Fatal("a multi-line watch command was accepted")
	}
}
