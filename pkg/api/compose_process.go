package api

import (
	"fmt"
	"path"
	"slices"
	"strings"
	"time"
)

// ComposeProcess is a prebuilt image service's Compose process overrides
// (ADR-1337): the image's ENTRYPOINT, USER, WORKDIR and stop contract.
// Unset fields inherit the image's values. A non-nil empty Entrypoint clears
// the image ENTRYPOINT, as `entrypoint: []` does in Compose. Setting
// Entrypoint also drops the image CMD; the Compose `command` then supplies
// the arguments.
type ComposeProcess struct {
	Entrypoint       *[]string `json:"entrypoint,omitempty"`
	User             string    `json:"user,omitempty"`
	WorkingDir       string    `json:"working_dir,omitempty"`
	StopSignal       string    `json:"stop_signal,omitempty"`
	StopGracePeriodS *int      `json:"stop_grace_period_s,omitempty"`
}

// ComposeStopSignals are the stop signals guest-init delivers. Any other
// signal would silently fall back to SIGTERM, so Compose declarations naming
// one are rejected.
var ComposeStopSignals = []string{"SIGTERM", "SIGINT", "SIGQUIT", "SIGHUP", "SIGUSR1", "SIGUSR2"}

// CanonicalComposeStopSignal maps "TERM", "SIGTERM" or "15" to "SIGTERM". It
// returns "" for a signal guest-init does not deliver.
func CanonicalComposeStopSignal(raw string) string {
	value := strings.ToUpper(strings.TrimSpace(raw))
	numbers := map[string]string{"15": "SIGTERM", "2": "SIGINT", "3": "SIGQUIT", "1": "SIGHUP", "10": "SIGUSR1", "12": "SIGUSR2"}
	if name, ok := numbers[value]; ok {
		return name
	}
	if !strings.HasPrefix(value, "SIG") {
		value = "SIG" + value
	}
	if slices.Contains(ComposeStopSignals, value) {
		return value
	}
	return ""
}

func (p *ComposeProcess) Clone() *ComposeProcess {
	if p == nil {
		return nil
	}
	clone := *p
	if p.Entrypoint != nil {
		entrypoint := slices.Clone(*p.Entrypoint)
		if entrypoint == nil {
			entrypoint = []string{}
		}
		clone.Entrypoint = &entrypoint
	}
	if p.StopGracePeriodS != nil {
		grace := *p.StopGracePeriodS
		clone.StopGracePeriodS = &grace
	}
	return &clone
}

// IsZero reports a process declaration that overrides nothing.
func (p *ComposeProcess) IsZero() bool {
	return p == nil || (p.Entrypoint == nil && p.User == "" && p.WorkingDir == "" &&
		p.StopSignal == "" && p.StopGracePeriodS == nil)
}

// Validate checks the plan-independent shape. The user is checked again
// against the image's /etc/passwd when the image is materialized, and the
// stop grace period against the plan at admission.
func (p *ComposeProcess) Validate() error {
	if p == nil {
		return nil
	}
	if p.Entrypoint != nil {
		for _, arg := range *p.Entrypoint {
			if strings.ContainsRune(arg, '\x00') {
				return fmt.Errorf("entrypoint must not contain NUL")
			}
		}
		if len(*p.Entrypoint) > 0 && strings.TrimSpace((*p.Entrypoint)[0]) == "" {
			return fmt.Errorf("entrypoint must name an executable")
		}
	}
	if p.User != "" && (strings.ContainsAny(p.User, "\x00\r\n\t ") || strings.HasPrefix(p.User, ":") || strings.HasSuffix(p.User, ":")) {
		return fmt.Errorf("user must be a name or id, optionally with :group")
	}
	if p.WorkingDir != "" && (!strings.HasPrefix(p.WorkingDir, "/") || strings.ContainsRune(p.WorkingDir, '\x00') ||
		path.Clean(p.WorkingDir) != p.WorkingDir) {
		return fmt.Errorf("working_dir must be a clean absolute path")
	}
	if p.StopSignal != "" && CanonicalComposeStopSignal(p.StopSignal) != p.StopSignal {
		return fmt.Errorf("stop_signal must be one of %s", strings.Join(ComposeStopSignals, ", "))
	}
	if p.StopGracePeriodS != nil {
		grace := time.Duration(*p.StopGracePeriodS) * time.Second
		if *p.StopGracePeriodS < 1 || grace > MaxAppManifestStopGracePeriod {
			return fmt.Errorf("stop_grace_period must be between 1s and %s", MaxAppManifestStopGracePeriod)
		}
	}
	return nil
}

// ValidatePlan adds the plan's stop grace ceiling (ADR-138).
func (p *ComposeProcess) ValidatePlan(plan Plan) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if p == nil || p.StopGracePeriodS == nil {
		return nil
	}
	limits, ok := LimitsFor(plan)
	if !ok {
		return fmt.Errorf("unknown plan %q", plan)
	}
	if limits.DefaultStopGracePeriodS > 0 && *p.StopGracePeriodS > limits.DefaultStopGracePeriodS {
		return fmt.Errorf("stop_grace_period %ds exceeds the %s plan's %ds", *p.StopGracePeriodS, plan, limits.DefaultStopGracePeriodS)
	}
	return nil
}
