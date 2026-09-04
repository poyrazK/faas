package api

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestManifestDefaults(t *testing.T) {
	m := AppManifest{Entrypoint: []string{"/app/server"}}
	if m.EffectivePort() != DefaultAppPort {
		t.Errorf("port default = %d, want %d", m.EffectivePort(), DefaultAppPort)
	}
	if m.EffectiveUser() != DefaultAppUser {
		t.Errorf("user default = %q, want %q", m.EffectiveUser(), DefaultAppUser)
	}
	if m.EffectiveWorkingDir() != "/" {
		t.Errorf("workdir default = %q, want /", m.EffectiveWorkingDir())
	}
}

func TestManifestExplicitValuesWin(t *testing.T) {
	m := AppManifest{
		Entrypoint: []string{"/app/server"},
		Port:       8080,
		User:       "nobody",
		WorkingDir: "/srv",
	}
	if m.EffectivePort() != 8080 {
		t.Errorf("port override = %d, want 8080", m.EffectivePort())
	}
	if m.EffectiveUser() != "nobody" {
		t.Errorf("user override = %q", m.EffectiveUser())
	}
	if m.EffectiveWorkingDir() != "/srv" {
		t.Errorf("workdir override = %q", m.EffectiveWorkingDir())
	}
}

func TestManifestValidate(t *testing.T) {
	tests := []struct {
		name string
		m    AppManifest
		ok   bool
	}{
		{"valid", AppManifest{Entrypoint: []string{"node", "index.js"}}, true},
		{"empty entrypoint", AppManifest{}, false},
		{"empty argv0", AppManifest{Entrypoint: []string{""}}, false},
		{"bad port", AppManifest{Entrypoint: []string{"x"}, Port: 70000}, false},
		{"neg port", AppManifest{Entrypoint: []string{"x"}, Port: -1}, false},
		// Issue #460 / ADR-053 — env_secrets refs (PR-B wiring). Ref names
		// match ^[A-Z][A-Z0-9_]*$ (same grammar as pkg/api/dto.go's apid
		// validation, mirrored to keep the manifest contract self-contained).
		{"env_secrets well-formed", AppManifest{
			Entrypoint: []string{"x"},
			EnvSecrets: map[string]string{"DB_URL": "secret:DB_URL"},
		}, true},
		{"env_secrets missing prefix", AppManifest{
			Entrypoint: []string{"x"},
			EnvSecrets: map[string]string{"DB_URL": "plaintext"},
		}, false},
		{"env_secrets bad ref name (lowercase)", AppManifest{
			Entrypoint: []string{"x"},
			EnvSecrets: map[string]string{"DB_URL": "secret:lowercase"},
		}, false},
		{"env_secrets empty value", AppManifest{
			Entrypoint: []string{"x"},
			EnvSecrets: map[string]string{"DB_URL": ""},
		}, false},
		{"env_secrets empty map", AppManifest{
			Entrypoint: []string{"x"},
			EnvSecrets: map[string]string{},
		}, true},
		// M-1 (ADR-136) — StopGracePeriod bounded at the manifest side
		// to keep the platform's tail-drain budget sane. M-2 commit
		// 10 tightens the cap per-plan via ValidatePlan; the
		// gross back-compat (5 min) is the absolute ceiling on
		// Scale's per-plan cap of 120 s. These tests pin
		// PlanScale's per-plan cap (DefaultStopGracePeriodS=120)
		// to surface regressions in the back-compat shim.
		{"stop_grace_period zero ok", AppManifest{
			Entrypoint: []string{"x"}, StopGracePeriod: 0,
		}, true},
		{"stop_grace_period under cap ok", AppManifest{
			Entrypoint: []string{"x"}, StopGracePeriod: 30 * time.Second,
		}, true},
		{"stop_grace_period at scale cap ok", AppManifest{
			// 2 min == exactly the Scale per-plan cap
			// (Limits.Scale.DefaultStopGracePeriodS = 120).
			Entrypoint: []string{"x"}, StopGracePeriod: 2 * time.Minute,
		}, true},
		{"stop_grace_period over per-plan cap rejected", AppManifest{
			// 2 min + 1 ns: still under the absolute 5-min
			// ceiling but over the Scale per-plan cap.
			Entrypoint: []string{"x"}, StopGracePeriod: 2*time.Minute + time.Second,
		}, false},
		{"stop_grace_period over absolute cap rejected", AppManifest{
			Entrypoint: []string{"x"}, StopGracePeriod: 10 * time.Minute,
		}, false},
		{"stop_grace_period negative rejected", AppManifest{
			Entrypoint: []string{"x"}, StopGracePeriod: -1 * time.Second,
		}, false},
		// M-2 (ADR-137) — ExecutionMode closed-set.
		{"execution_mode empty defaults to request", AppManifest{
			Entrypoint: []string{"x"},
		}, true},
		{"execution_mode=worker", AppManifest{
			Entrypoint: []string{"x"}, ExecutionMode: ExecutionModeWorker,
		}, true},
		{"execution_mode=job", AppManifest{
			Entrypoint: []string{"x"}, ExecutionMode: ExecutionModeJob,
		}, true},
		{"execution_mode=service with replicas", AppManifest{
			Entrypoint:      []string{"x"},
			ExecutionMode:   ExecutionModeService,
			ServiceReplicas: &ServiceReplicas{Min: 1, Max: 3, Desired: 2},
		}, true},
		{"execution_mode=bogus rejected", AppManifest{
			Entrypoint: []string{"x"}, ExecutionMode: "bogus",
		}, false},
		// M-2 (ADR-137 §Decision 2) — per-mode RestartPolicy defaults.
		{"restart_policy=always on job rejected", AppManifest{
			Entrypoint:    []string{"x"},
			ExecutionMode: ExecutionModeJob,
			RestartPolicy: RestartPolicyAlways,
		}, false},
		{"restart_policy=always on worker ok", AppManifest{
			Entrypoint:    []string{"x"},
			ExecutionMode: ExecutionModeWorker,
			RestartPolicy: RestartPolicyAlways,
		}, true},
		{"restart_policy=no on job ok", AppManifest{
			Entrypoint:    []string{"x"},
			ExecutionMode: ExecutionModeJob,
			RestartPolicy: RestartPolicyNo,
		}, true},
		{"restart_policy=bogus rejected", AppManifest{
			Entrypoint:    []string{"x"},
			RestartPolicy: "bogus",
		}, false},
		// M-2 (ADR-137/138, commit 10) — StartupDeadlineS /
		// MaxRetries per-plan caps. Tests pin PlanScale's
		// per-plan ceiling (120 s / 20 retries) so a regression
		// in the Limits table surfaces here. The gross
		// MaxAppManifest* constants remain as absolute ceilings
		// for the Scale plan and are pinned at the bottom of
		// the table.
		{"startup_deadline_s zero ok", AppManifest{
			Entrypoint: []string{"x"},
		}, true},
		{"startup_deadline_s at scale cap ok", AppManifest{
			Entrypoint:       []string{"x"},
			StartupDeadlineS: 120, // DefaultStartupDeadlineS for Scale
		}, true},
		{"startup_deadline_s over per-plan cap rejected", AppManifest{
			Entrypoint:       []string{"x"},
			StartupDeadlineS: 121,
		}, false},
		{"startup_deadline_s over absolute cap rejected", AppManifest{
			Entrypoint:       []string{"x"},
			StartupDeadlineS: MaxAppManifestStartupDeadlineS + 1,
		}, false},
		{"startup_deadline_s negative rejected", AppManifest{
			Entrypoint:       []string{"x"},
			StartupDeadlineS: -1,
		}, false},
		{"max_retries at scale cap ok", AppManifest{
			Entrypoint: []string{"x"},
			MaxRetries: 20, // DefaultMaxRetries for Scale
		}, true},
		{"max_retries over per-plan cap rejected", AppManifest{
			Entrypoint: []string{"x"},
			MaxRetries: 21,
		}, false},
		{"max_retries over absolute cap rejected", AppManifest{
			Entrypoint: []string{"x"},
			MaxRetries: MaxAppManifestMaxRetries + 1,
		}, false},
		// ServiceReplicas shape (ADR-137 §Decision 3).
		{"service_replicas negative rejected", AppManifest{
			Entrypoint:      []string{"x"},
			ServiceReplicas: &ServiceReplicas{Min: -1, Max: 3, Desired: 2},
		}, false},
		{"service_replicas min>max rejected", AppManifest{
			Entrypoint:      []string{"x"},
			ServiceReplicas: &ServiceReplicas{Min: 5, Max: 3, Desired: 4},
		}, false},
		{"service_replicas desired<min rejected", AppManifest{
			Entrypoint:      []string{"x"},
			ServiceReplicas: &ServiceReplicas{Min: 2, Max: 5, Desired: 1},
		}, false},
		{"service_replicas desired>max rejected", AppManifest{
			Entrypoint:      []string{"x"},
			ServiceReplicas: &ServiceReplicas{Min: 1, Max: 5, Desired: 10},
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.m.Validate()
			if (err == nil) != tt.ok {
				t.Errorf("Validate() err=%v, want ok=%v", err, tt.ok)
			}
		})
	}
}

func TestManifestRoundTrip(t *testing.T) {
	in := AppManifest{
		Entrypoint: []string{"node", "server.js"},
		Env:        map[string]string{"NODE_ENV": "production"},
		EnvSecrets: map[string]string{"DB_URL": "secret:DB_URL", "API_KEY": "secret:API_KEY"},
		Port:       3000,
		Healthz:    "/healthz",
	}
	var buf bytes.Buffer
	if err := WriteManifest(&buf, in); err != nil {
		t.Fatal(err)
	}
	out, err := ReadManifest(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if out.Entrypoint[1] != "server.js" || out.Port != 3000 || out.Env["NODE_ENV"] != "production" {
		t.Errorf("round trip mismatch: %+v", out)
	}
	if out.EnvSecrets["DB_URL"] != "secret:DB_URL" || out.EnvSecrets["API_KEY"] != "secret:API_KEY" {
		t.Errorf("env_secrets round trip mismatch: %+v", out.EnvSecrets)
	}
}

func TestReadManifestRejectsInvalid(t *testing.T) {
	if _, err := ReadManifest(strings.NewReader(`{"port":3000}`)); err == nil {
		t.Error("manifest with no entrypoint should fail validation on read")
	}
}

func TestErrAppLayerTooLarge(t *testing.T) {
	l := MustLimitsFor(PlanFree) // 256 MB cap
	p := ErrAppLayerTooLarge(l, 300*1024*1024)
	if p.Code != CodeAppLayerTooBig {
		t.Errorf("code = %q", p.Code)
	}
	if p.Limit == nil || *p.Limit != 256*1024*1024 {
		t.Errorf("limit not set to plan cap bytes: %v", p.Limit)
	}
	if !strings.Contains(p.Detail, "256 MB") {
		t.Errorf("detail should name the cap: %q", p.Detail)
	}
}

// TestEffectiveExecutionModeAndRestartPolicy pins the per-mode defaults
// (ADR-137 §Decision 1 + §Decision 2). Existing manifests with neither
// ExecutionMode nor RestartPolicy set must default to "request" /
// "on-failure" — preserving today's behaviour for customers who haven't
// opted into the new fields. request → on-failure prevents the
// infinite-restart loop on clean-exit HTTP servers (the supervisor
// re-execs, MaxRetries trips, false crash_loop fires).
func TestEffectiveExecutionModeAndRestartPolicy(t *testing.T) {
	tests := []struct {
		name        string
		m           AppManifest
		wantMode    string
		wantRestart string
	}{
		{"empty → request/on-failure", AppManifest{Entrypoint: []string{"x"}}, ExecutionModeRequest, RestartPolicyOnFailure},
		{"explicit request", AppManifest{Entrypoint: []string{"x"}, ExecutionMode: "request"}, "request", RestartPolicyOnFailure},
		{"service → always", AppManifest{Entrypoint: []string{"x"}, ExecutionMode: "service"}, "service", RestartPolicyAlways},
		{"worker → always", AppManifest{Entrypoint: []string{"x"}, ExecutionMode: "worker"}, "worker", RestartPolicyAlways},
		{"job → no", AppManifest{Entrypoint: []string{"x"}, ExecutionMode: "job"}, "job", RestartPolicyNo},
		{"explicit restart overrides default", AppManifest{Entrypoint: []string{"x"}, ExecutionMode: "worker", RestartPolicy: "on-failure"}, "worker", RestartPolicyOnFailure},
		{"explicit job restart policy wins", AppManifest{Entrypoint: []string{"x"}, ExecutionMode: "job", RestartPolicy: "on-failure"}, "job", RestartPolicyOnFailure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.m.EffectiveExecutionMode(); got != tt.wantMode {
				t.Errorf("EffectiveExecutionMode()=%q, want %q", got, tt.wantMode)
			}
			if got := tt.m.EffectiveRestartPolicy(); got != tt.wantRestart {
				t.Errorf("EffectiveRestartPolicy()=%q, want %q", got, tt.wantRestart)
			}
		})
	}
}

// TestManifestValidate_PerPlanTierTightening pins M-2 / ADR-138
// §Decision 4 / §Decision 5: each plan enforces its own
// StopGracePeriod / StartupDeadlineS / MaxRetries cap. The same
// manifest value that's accepted on Scale is rejected on Hobby
// and Free. A regression here means the per-plan tiers silently
// widened (or narrowed) — both are customer-trust bugs.
func TestManifestValidate_PerPlanTierTightening(t *testing.T) {
	// 45 s is bigger than Hobby's 30 s cap and Free's 15 s cap,
	// but smaller than Scale's 120 s cap. The expected matrix
	// is the core regression test for per-plan tightening.
	const at45s = 45 * time.Second
	cases := []struct {
		name string
		plan Plan
		ok   bool
	}{
		{"free_rejects_45s_stop_grace", PlanFree, false},
		{"hobby_rejects_45s_stop_grace", PlanHobby, false},
		{"pro_rejects_45s_stop_grace", PlanPro, true}, // Pro cap = 60s
		{"scale_accepts_45s_stop_grace", PlanScale, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := AppManifest{Entrypoint: []string{"x"}, StopGracePeriod: at45s}
			err := m.ValidatePlan(tc.plan)
			if (err == nil) != tc.ok {
				t.Errorf("plan=%q ValidatePlan=%v want ok=%v", tc.plan, err, tc.ok)
			}
		})
	}

	// StartupDeadlineS: table-driven per plan × value.
	// Hobby cap = 30 s. Pro cap = 60 s. Scale cap = 120 s.
	// Free cap = 15 s.
	startupCases := []struct {
		name string
		plan Plan
		val  int
		ok   bool
	}{
		{"free_rejects_60s_startup", PlanFree, 60, false},   // Free cap=15
		{"hobby_rejects_60s_startup", PlanHobby, 60, false}, // Hobby cap=30
		{"pro_accepts_60s_startup", PlanPro, 60, true},
		{"scale_accepts_120s_startup", PlanScale, 120, true},
	}
	for _, tc := range startupCases {
		t.Run(tc.name, func(t *testing.T) {
			m := AppManifest{Entrypoint: []string{"x"}, StartupDeadlineS: tc.val}
			err := m.ValidatePlan(tc.plan)
			if (err == nil) != tc.ok {
				t.Errorf("plan=%q StartupDeadlineS=%d ValidatePlan=%v want ok=%v", tc.plan, tc.val, err, tc.ok)
			}
		})
	}

	// MaxRetries per-plan cap: Free=3, Hobby=5, Pro=10, Scale=20.
	retriesCases := []struct {
		name string
		plan Plan
		val  int
		ok   bool
	}{
		{"free_rejects_5_retries", PlanFree, 5, false},
		{"hobby_accepts_5_retries", PlanHobby, 5, true},
		{"pro_rejects_11_retries", PlanPro, 11, false},
		{"scale_accepts_20_retries", PlanScale, 20, true},
	}
	for _, tc := range retriesCases {
		t.Run(tc.name, func(t *testing.T) {
			m := AppManifest{Entrypoint: []string{"x"}, MaxRetries: tc.val}
			err := m.ValidatePlan(tc.plan)
			if (err == nil) != tc.ok {
				t.Errorf("plan=%q MaxRetries=%d ValidatePlan=%v want ok=%v", tc.plan, tc.val, err, tc.ok)
			}
		})
	}
}

// TestManifestValidate_ExecutionModeAllowlist pins the per-mode
// lock (ADR-137 §Decision 3, ADR-069 precedent). Free rejects
// every non-request ExecutionMode; paid plans accept the modes
// their replica cap allows. A regression here means a Free
// customer can smuggle a worker into their fleet.
func TestManifestValidate_ExecutionModeAllowlist(t *testing.T) {
	cases := []struct {
		name string
		plan Plan
		mode string
		ok   bool
	}{
		{"free_accepts_request", PlanFree, ExecutionModeRequest, true},
		{"free_rejects_worker", PlanFree, ExecutionModeWorker, false},
		{"free_rejects_service", PlanFree, ExecutionModeService, false},
		{"free_rejects_job", PlanFree, ExecutionModeJob, false},
		{"hobby_accepts_worker", PlanHobby, ExecutionModeWorker, true},
		{"hobby_accepts_service", PlanHobby, ExecutionModeService, true},
		{"hobby_accepts_job", PlanHobby, ExecutionModeJob, true},
		{"scale_accepts_worker", PlanScale, ExecutionModeWorker, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := AppManifest{Entrypoint: []string{"x"}, ExecutionMode: tc.mode}
			err := m.ValidatePlan(tc.plan)
			if (err == nil) != tc.ok {
				t.Errorf("plan=%q mode=%q ValidatePlan=%v want ok=%v", tc.plan, tc.mode, err, tc.ok)
			}
		})
	}
}

// TestManifestValidate_ServiceReplicasPerPlanCap pins the
// per-plan replica ceiling (ADR-137 §Decision 3). Hobby's
// cap (3) and Pro's cap (5) are the canonical anchors; a
// regression here means a Hobby customer can request more
// replicas than the plan grants.
func TestManifestValidate_ServiceReplicasPerPlanCap(t *testing.T) {
	cases := []struct {
		name        string
		plan        Plan
		desired     int
		maxReplicas int
		ok          bool
	}{
		{"hobby_desired_3_ok", PlanHobby, 3, 3, true}, // Hobby cap=3
		{"hobby_desired_4_over", PlanHobby, 4, 4, false},
		{"pro_desired_5_ok", PlanPro, 5, 5, true}, // Pro cap=5
		{"pro_desired_6_over", PlanPro, 6, 6, false},
		{"scale_desired_20_ok", PlanScale, 20, 20, true}, // Scale cap=20
		{"scale_desired_21_over", PlanScale, 21, 21, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := AppManifest{
				Entrypoint:      []string{"x"},
				ExecutionMode:   ExecutionModeService,
				ServiceReplicas: &ServiceReplicas{Min: 1, Max: tc.maxReplicas, Desired: tc.desired},
			}
			err := m.ValidatePlan(tc.plan)
			if (err == nil) != tc.ok {
				t.Errorf("plan=%q desired=%d ValidatePlan=%v want ok=%v", tc.plan, tc.desired, err, tc.ok)
			}
		})
	}
}

// TestLimits_M2DefaultsPerPlan locks the per-plan numbers in
// pkg/api/limits.go. A regression here is a financial-model
// divergence (§17 G-FinancialDoc-Drift): the limits table and
// docs/financial/M2_EXECUTION_MODE_ADDENDUM.md must agree.
func TestLimits_M2DefaultsPerPlan(t *testing.T) {
	cases := []struct {
		plan        Plan
		wantGrace   int
		wantStartup int
		wantRetries int
		wantWorker  int
		wantService int
		wantJob     int
	}{
		{PlanFree, 15, 15, 3, 0, 0, 0},
		{PlanHobby, 30, 30, 5, 1, 3, 300},
		{PlanPro, 60, 60, 10, 3, 5, 1800},
		{PlanScale, 120, 120, 20, 10, 20, 3600},
	}
	for _, tc := range cases {
		t.Run(string(tc.plan), func(t *testing.T) {
			l, ok := LimitsFor(tc.plan)
			if !ok {
				t.Fatalf("LimitsFor(%q) not found", tc.plan)
			}
			if l.DefaultStopGracePeriodS != tc.wantGrace {
				t.Errorf("DefaultStopGracePeriodS = %d, want %d", l.DefaultStopGracePeriodS, tc.wantGrace)
			}
			if l.DefaultStartupDeadlineS != tc.wantStartup {
				t.Errorf("DefaultStartupDeadlineS = %d, want %d", l.DefaultStartupDeadlineS, tc.wantStartup)
			}
			if l.DefaultMaxRetries != tc.wantRetries {
				t.Errorf("DefaultMaxRetries = %d, want %d", l.DefaultMaxRetries, tc.wantRetries)
			}
			if l.WorkerReplicasMax != tc.wantWorker {
				t.Errorf("WorkerReplicasMax = %d, want %d", l.WorkerReplicasMax, tc.wantWorker)
			}
			if l.ServiceReplicasMax != tc.wantService {
				t.Errorf("ServiceReplicasMax = %d, want %d", l.ServiceReplicasMax, tc.wantService)
			}
			if l.JobMaxRuntimeS != tc.wantJob {
				t.Errorf("JobMaxRuntimeS = %d, want %d", l.JobMaxRuntimeS, tc.wantJob)
			}
		})
	}
}
