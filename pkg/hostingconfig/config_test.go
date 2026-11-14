package hostingconfig

import (
	"strings"
	"testing"
	"testing/fstest"
)

// hosting.readiness_probe is the primary app's steady-state traffic gate
// (2026-11-13); its bounds are the deploy override's.
func TestParseReadinessProbe(t *testing.T) {
	cfg, present, err := Parse([]byte("hosting:\n  readiness_probe:\n    path: /ready\n    period_s: 5\n    failure_threshold: 2\n"))
	if err != nil || !present || cfg.ReadinessProbe == nil || cfg.ReadinessProbe.Path != "/ready" || cfg.ReadinessProbe.PeriodS != 5 || cfg.ReadinessProbe.FailureThreshold != 2 {
		t.Fatalf("Parse = %+v present=%v err=%v", cfg, present, err)
	}
	for name, body := range map[string]string{
		"relative path":           "hosting:\n  readiness_probe:\n    path: ready\n",
		"neither path nor grpc":   "hosting:\n  readiness_probe:\n    period_s: 5\n",
		"period too long":         "hosting:\n  readiness_probe:\n    path: /ready\n    period_s: 100000\n",
		"liveness not declarable": "hosting:\n  liveness_probe:\n    path: /live\n",
	} {
		if _, _, err := Parse([]byte(body)); err == nil {
			t.Errorf("%s: Parse accepted %q", name, body)
		}
	}
}

func TestLoadHostingOverrides(t *testing.T) {
	cfg, ok, err := Load(fstest.MapFS{
		"gregale.yaml": &fstest.MapFile{Data: []byte(`hosting:
  start: "npm run serve"
  port: 8787
  health: /ready
triggers: []
`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok || cfg.Start != "npm run serve" || cfg.Port != 8787 || cfg.Health != "/ready" {
		t.Fatalf("config = %+v, present=%v", cfg, ok)
	}
}

func TestParseRejectsUnknownHostingField(t *testing.T) {
	_, _, err := Parse([]byte("hosting:\n  command: node server.js\n"))
	if err == nil || !strings.Contains(err.Error(), "field command not found") {
		t.Fatalf("error = %v, want unknown-field error", err)
	}
}

func TestParseRejectsInvalidValues(t *testing.T) {
	cases := []string{
		"hosting:\n  port: 70000\n",
		"hosting:\n  health: ready\n",
		"hosting:\n  start: \"node\\nserver.js\"\n",
	}
	for _, body := range cases {
		t.Run(body, func(t *testing.T) {
			_, _, err := Parse([]byte(body))
			if err == nil {
				t.Fatal("Parse returned nil error")
			}
		})
	}
}

func TestLoadWithoutHostingIsNoop(t *testing.T) {
	cfg, ok, err := Load(fstest.MapFS{
		"gregale.yaml": &fstest.MapFile{Data: []byte("triggers: []\n")},
	})
	if err != nil || ok || cfg != (Config{}) {
		t.Fatalf("config=%+v present=%v err=%v, want empty no-op", cfg, ok, err)
	}
}
