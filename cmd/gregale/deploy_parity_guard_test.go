package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/deploydiff"
)

// adr: 122
func TestDeployInvalidIntentFailsBeforeNetwork(t *testing.T) {
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")

	cases := []struct {
		name string
		args []string
	}{
		{"function missing runtime", []string{"--name", "valid-app", "--function", "--tarball", "missing.tgz"}},
		{"unsupported runtime", []string{"--name", "valid-app", "--function", "--runtime", "ruby3", "--tarball", "missing.tgz"}},
		{"app with function fields", []string{"--name", "valid-app", "--app", "--runtime", "node22", "--image", validDeployTestImage()}},
		{"invalid protocol", []string{"--name", "valid-app", "--app-protocol", "HTTP2", "--image", validDeployTestImage()}},
		{"invalid traffic", []string{"--name", "valid-app", "--traffic-percent", "101", "--image", validDeployTestImage()}},
		{"no traffic with traffic percent", []string{"--name", "valid-app", "--no-traffic", "--traffic-percent", "0", "--image", validDeployTestImage()}},
		{"no traffic with safe", []string{"--name", "valid-app", "--no-traffic", "--safe", "--image", validDeployTestImage()}},
		{"no traffic with canary", []string{"--name", "valid-app", "--no-traffic", "--canary-preset", "balanced", "--image", validDeployTestImage()}},
		{"unknown canary", []string{"--name", "valid-app", "--canary-preset", "mystery", "--image", validDeployTestImage()}},
		{"stages without custom", []string{"--name", "valid-app", "--canary-stages", "100@0s", "--image", validDeployTestImage()}},
		{"invalid image", []string{"--name", "valid-app", "--image", "registry.example/app:latest"}},
		{"invalid slug", []string{"--name", "Invalid_App", "--image", validDeployTestImage()}},
		{"create only deployment flags", []string{"--create-only", "--name", "valid-app", "--app", "--no-wait", "--reason", "release"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := requests.Load()
			if code := cmdDeployTarball(tc.args); code == 0 {
				t.Fatalf("cmdDeployTarball(%v) returned success", tc.args)
			}
			if got := requests.Load(); got != before {
				t.Fatalf("invalid deploy made %d HTTP request(s)", got-before)
			}
		})
	}
}

func validDeployTestImage() string {
	return "registry.example.com/team/app@sha256:" + fmt.Sprintf("%064x", 1)
}

// adr: 090
func TestValidateSingleAppManifestTargets(t *testing.T) {
	write := func(t *testing.T, body string) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	if err := validateSingleAppManifestTargets(write(t, "triggers:\n  - kind: cron\n    app: typo-app\n    schedule: '* * * * *'\n    path: /tick\n"), "real-app"); err == nil {
		t.Fatal("mismatched trigger target was accepted")
	}
	if err := validateSingleAppManifestTargets(write(t, "triggers:\n  - kind: queue\n    app: real-app\n    slug: jobs\n    config: {mode: queue}\n"), "real-app"); err == nil {
		t.Fatal("non-cron trigger was silently accepted on the cron-only path")
	}
}

func TestValidateSingleAppManifestTargetsRejectsEveryUnifiedKindExplicitly(t *testing.T) {
	tests := []struct {
		kind   string
		config string
	}{
		{"kafka", "slug: orders\n    config: {brokers: ['broker:9092'], topic: orders, group: workers}"},
		{"nats", "slug: telemetry\n    config: {url: 'nats://nats:4222', stream: events, subject: 'events.>', durable: faas}"},
		{"redis_streams", "slug: cache\n    config: {addr: 'redis:6379', stream: events, group: workers}"},
		{"sqs_compat", "slug: external\n    config: {queue_url: 'https://queue.example.test/external'}"},
		{"queue", "slug: internal\n    config: {mode: queue}"},
	}
	for _, test := range tests {
		t.Run(test.kind, func(t *testing.T) {
			dir := t.TempDir()
			body := "triggers:\n  - kind: " + test.kind + "\n    app: real-app\n    " + test.config + "\n"
			if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			err := validateSingleAppManifestTargets(dir, "real-app")
			if err == nil {
				t.Fatal("non-cron manifest trigger was accepted")
			}
			for _, want := range []string{test.kind, "--no-triggers", "gregale triggers add"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

// adr: 122
func TestDiffAdaptersCarryDeploymentIntent(t *testing.T) {
	traffic := 25
	vcpu := 4
	workflow := api.WorkflowSpec{Name: "release", Steps: []api.WorkflowStepSpec{{Name: "ship", Run: "real-app"}}}
	opts := diffCLIOptions{
		AppConfig:      deployDiffAppConfigWithVCPU(vcpu),
		TrafficPercent: &traffic,
		Canary:         nil,
		Workflows:      []api.WorkflowSpec{workflow},
		NoTriggers:     true,
	}
	req := diffRequestFromCLI(opts)
	if req.AppConfig == nil || req.AppConfig.VCPU == nil || *req.AppConfig.VCPU != vcpu {
		t.Fatalf("server diff lost vcpu: %+v", req.AppConfig)
	}
	if req.TrafficPercent == nil || *req.TrafficPercent != traffic || len(req.Workflows) != 1 {
		t.Fatalf("server diff lost rollout/workflow intent: %+v", req)
	}
	pending := buildPending(t.Context(), nil, opts, deployEmptyBaseline())
	if pending.Crons != nil {
		t.Fatalf("--no-triggers projected crons: %+v", pending.Crons)
	}
}

func deployDiffAppConfigWithVCPU(v int) (p deploydiff.AppConfigPatch) {
	p.VCPU = &v
	return p
}

func deployEmptyBaseline() deploydiff.Baseline { return deploydiff.Baseline{} }
