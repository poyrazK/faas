package ci

import (
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
)

func TestCIWorkerConfigurationOwnsOnlyLocalWorkersAndPreservesDrain(t *testing.T) {
	account := uuid.NewString()
	cfg := ConfigFromEnv(func(key string) string {
		if key == AccountsEnv {
			return account
		}
		return "1"
	})
	apid, githubd := cfg.WorkerConfiguration("apid"), cfg.WorkerConfiguration("githubd")
	if len(apid) != 4 || len(githubd) != 2 || len(cfg.WorkerConfiguration("invalid")) != 0 {
		t.Fatal("worker ownership is not bounded by daemon")
	}
	for _, kind := range workerKinds() {
		enabled, owned := apid[kind]
		if !owned {
			enabled, owned = githubd[kind]
		}
		if !owned || !enabled {
			t.Fatalf("fully opted-in worker missing: %s", kind)
		}
	}
	cfg.Enabled = false
	apid, githubd = cfg.WorkerConfiguration("apid"), cfg.WorkerConfiguration("githubd")
	if !apid[WorkerDispatcher] || !apid[WorkerRetention] || apid[WorkerMetadataAdmission] || apid[WorkerAutomationAdmission] || githubd[WorkerMetadataChecks] || githubd[WorkerExecutionChecks] {
		t.Fatal("admission removal changed independent drain ownership")
	}
	cfg.Enabled = true
	cfg.DispatchEnabled = false
	if cfg.WorkerConfiguration("apid")[WorkerAutomationAdmission] || !cfg.WorkerConfiguration("apid")[WorkerDispatcher] {
		t.Fatal("stopping fresh admission stopped drain or retained automatic admission")
	}
	cfg.GitHubChecksEnabled = false
	for _, enabled := range cfg.WorkerConfiguration("githubd") {
		if enabled {
			t.Fatal("publication gate did not disable both Check workers")
		}
	}
}

func TestCIHealthMetricsWorkerConfigurationSurvivesFailedReads(t *testing.T) {
	for _, daemon := range []string{"apid", "githubd"} {
		t.Run(daemon, func(t *testing.T) {
			cfg := Config{WorkerEnabled: true}
			if daemon == "githubd" {
				cfg = ConfigFromEnv(func(key string) string {
					if key == AccountsEnv {
						return "cf4a5267-12e5-4fd7-bb0e-a44c9f326c38"
					}
					return "1"
				})
			}
			registry := prometheus.NewPedanticRegistry()
			reader := &healthMetricsReader{fail: true}
			NewHealthMetrics(t.Context(), registry, daemon, reader, cfg.HealthMonitoringEnabled, func() map[WorkerKind]bool {
				out := cfg.WorkerConfiguration(daemon)
				out[WorkerKind("private-unbounded-label")] = true
				return out
			})
			families, err := registry.Gather()
			if err != nil {
				t.Fatal(err)
			}
			seen := 0
			for _, family := range families {
				if family.GetName() != daemon+"_ci_worker_enabled" {
					continue
				}
				for _, metric := range family.Metric {
					seen++
					if len(metric.Label) != 1 || metric.Label[0].GetName() != "worker" {
						t.Fatal("worker configuration emitted identity labels")
					}
					kind := WorkerKind(metric.Label[0].GetValue())
					enabled, owned := cfg.WorkerConfiguration(daemon)[kind]
					expected := 0.0
					if enabled {
						expected = 1
					}
					if !owned || metric.Gauge.GetValue() != expected {
						t.Fatal("failed read erased or expanded boot configuration")
					}
				}
			}
			if reader.calls != 1 || seen != len(cfg.WorkerConfiguration(daemon)) {
				t.Fatal("local configuration evidence missing")
			}
		})
	}
}
