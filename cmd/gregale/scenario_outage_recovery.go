package main

import "fmt"

type testOutageRecoveryCheck struct {
	Request            string   `json:"request,omitempty"`
	BaselineMS         *float64 `json:"baseline_ms,omitempty"`
	CurrentMS          *float64 `json:"current_ms,omitempty"`
	LimitMS            *float64 `json:"limit_ms,omitempty"`
	MaxIncreasePercent float64  `json:"max_increase_percent"`
	Passed             bool     `json:"passed"`
	Error              string   `json:"error,omitempty"`
}

// A successful healthy window alone is insufficient when its configured
// stability observation or its measured recovery time is missing.
func testOutageConfirmedRecovery(r *testLoadRecoveryEvidence, cfg *testLoadRecoveryConfig) (*float64, string) {
	if cfg == nil || r == nil || !r.Passed || r.Error != "" {
		return nil, "recovery is missing or unconfirmed"
	}
	if r.RecoveryMS == nil || !finiteNonnegative(*r.RecoveryMS) || *r.RecoveryMS <= 0 || *r.RecoveryMS > float64(cfg.Within.Milliseconds()) || r.WindowsChecked < 1 {
		return nil, "confirmed recovery time is missing or invalid"
	}
	if r.WithinMS != cfg.Within.Milliseconds() || r.SustainMS != cfg.Sustain.Milliseconds() || r.WindowMS != cfg.Window.Milliseconds() {
		return nil, "recovery evidence does not match the configured budget"
	}
	if cfg.Stability > 0 {
		s := r.Stability
		if s == nil || !s.Passed || s.DurationMS != cfg.Stability.Milliseconds() || s.ObservedMS != s.DurationMS || s.WindowsChecked < 1 || s.RelapseMS != nil || s.StartedMS == nil || !finiteNonnegative(*s.StartedMS) || *s.StartedMS+1e-6 < *r.RecoveryMS+float64(r.SustainMS) {
			return nil, "recovery stability is unconfirmed"
		}
	}
	value := *r.RecoveryMS
	return &value, ""
}

func testOutageCycleRecoveryChecks(report *testLoadEvidence, cfg *testLoadConfig, cycle, abortedCycle int) []testOutageRecoveryCheck {
	if cfg.OutageCycles == nil || cfg.OutageCycles.MaxRecoveryIncreasePercent == nil || len(cfg.ChaosSchedule) < 3 || 2*cycle >= len(cfg.ChaosSchedule) {
		return nil
	}
	baselineCfg := cfg.ChaosSchedule[2]
	currentCfg := cfg.ChaosSchedule[2*cycle]
	targets := make(map[string]*testLoadRecoveryConfig, len(baselineCfg.StepRecovery)+1)
	if baselineCfg.Recovery != nil {
		targets[""] = baselineCfg.Recovery
	}
	for name, budget := range baselineCfg.StepRecovery {
		targets[name] = budget
	}
	// Include any explicitly added target in a matrix variant: it cannot borrow
	// the aggregate or another endpoint as a replacement baseline.
	if currentCfg.Recovery != nil && targets[""] == nil {
		targets[""] = nil
	}
	for name := range currentCfg.StepRecovery {
		if _, ok := targets[name]; !ok {
			targets[name] = nil
		}
	}
	var baseline, current testLoadChaosPhaseEvidence
	if len(report.ChaosPhases) > 2 {
		baseline = report.ChaosPhases[2]
	}
	if len(report.ChaosPhases) > 2*cycle {
		current = report.ChaosPhases[2*cycle]
	}
	baselineEvidence := testPhaseRecoveryEvidence(baseline)
	currentEvidence := testPhaseRecoveryEvidence(current)
	var checks []testOutageRecoveryCheck
	for _, name := range testEndpointRecoveryNames(targets) {
		check := testOutageRecoveryCheck{Request: name, MaxIncreasePercent: *cfg.OutageCycles.MaxRecoveryIncreasePercent}
		base, baseErr := testOutageConfirmedRecovery(baselineEvidence[name], targets[name])
		budget := currentCfg.Recovery
		if name != "" {
			budget = currentCfg.StepRecovery[name]
		}
		now, currentErr := testOutageConfirmedRecovery(currentEvidence[name], budget)
		if baseline.Name != baselineCfg.Name || baseline.StartedAt.IsZero() || !baseline.FinishedAt.After(baseline.StartedAt) || abortedCycle == 1 {
			baseErr = "cycle 1 recovery phase is incomplete"
			base = nil
		}
		if current.Name != currentCfg.Name || current.StartedAt.IsZero() || !current.FinishedAt.After(current.StartedAt) || (abortedCycle > 0 && cycle >= abortedCycle) {
			currentErr = "recovery phase is incomplete"
			now = nil
		}
		check.BaselineMS = base
		check.CurrentMS = now
		if base != nil {
			limit := *base * (1 + check.MaxIncreasePercent/100)
			check.LimitMS = &limit
		}
		switch {
		case baseErr != "":
			check.Error = "cycle 1 baseline: " + baseErr
		case currentErr != "":
			check.Error = currentErr
		case *now > *check.LimitMS+1e-12:
			check.Error = fmt.Sprintf("%.4gms exceeds cycle 1 recovery %.4gms plus %.4g%% (limit %.4gms)", *now, *base, check.MaxIncreasePercent, *check.LimitMS)
		default:
			check.Passed = true
		}
		checks = append(checks, check)
	}
	return checks
}
