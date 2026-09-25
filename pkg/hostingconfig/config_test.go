package hostingconfig

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestLoadHostingOverrides(t *testing.T) {
	cfg, ok, err := Load(fstest.MapFS{
		"gregale.yaml": &fstest.MapFile{Data: []byte(`hosting:
  start: "npm run serve"
  port: 8787
  health: /ready
  startup_probe:
    grpc:
      service: grpc.health.v1.Health
    interval_s: 5
    timeout_s: 2
    retries: 3
  readiness_probe:
    path: /traffic-ready
    period_s: 7
    failure_threshold: 4
  liveness_probe:
    path: /alive
    interval_s: 10
    consecutive_failures: 4
triggers: []
`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok || cfg.Start != "npm run serve" || cfg.Port != 8787 || cfg.Health != "/ready" {
		t.Fatalf("config = %+v, present=%v", cfg, ok)
	}
	if cfg.StartupProbe == nil || cfg.StartupProbe.GRPC == nil || cfg.StartupProbe.GRPC.Service != "grpc.health.v1.Health" || cfg.StartupProbe.Retries != 3 {
		t.Fatalf("startup probe = %+v, want gRPC health probe", cfg.StartupProbe)
	}
	if cfg.ReadinessProbe == nil || cfg.ReadinessProbe.Path != "/traffic-ready" || cfg.ReadinessProbe.FailureThreshold != 4 {
		t.Fatalf("readiness probe = %+v, want configured path/threshold", cfg.ReadinessProbe)
	}
	if cfg.LivenessProbe == nil || cfg.LivenessProbe.Path != "/alive" || cfg.LivenessProbe.ConsecutiveFailures != 4 {
		t.Fatalf("liveness probe = %+v, want configured path/threshold", cfg.LivenessProbe)
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
		"hosting:\n  startup_probe:\n    grpc: {}\n    path: /started\n",
		"hosting:\n  readiness_probe:\n    path: ready\n",
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
